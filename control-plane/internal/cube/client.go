package cube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxResponseBytes = 2 << 20
	maxRequestBytes  = 1 << 20
	standardTimeout  = 45 * time.Second
	lifecycleTimeout = 130 * time.Second
	snapshotTimeout  = 250 * time.Second
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Client struct {
	base      *url.URL
	key       string
	http      *http.Client
	admission *admissionGuard
}

// APIError never includes upstream bodies, URLs, credentials or caller values.
// Inspect StatusCode for 404/409/503 handling; retries are the caller's decision.
// Mutations are deliberately never retried automatically.
type APIError struct {
	Operation         string
	StatusCode        int
	RetryAfterSeconds int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cube %s: upstream HTTP %d", e.Operation, e.StatusCode)
}

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.APIURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(u.Host, "\\ \t\r\n") {
		return nil, errors.New("cube: invalid API URL")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "\\\x00\r\n") {
			return nil, errors.New("cube: invalid API URL path")
		}
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("cube: invalid API URL port")
		}
	}
	if cfg.APIKey == "" || strings.TrimSpace(cfg.APIKey) != cfg.APIKey || strings.ContainsAny(cfg.APIKey, "\r\n\x00") {
		return nil, errors.New("cube: API key is required and must be a valid header value")
	}
	for _, c := range cfg.APIKey {
		if c < 32 || c > 126 {
			return nil, errors.New("cube: invalid API key header value")
		}
	}
	hc := http.Client{}
	if cfg.HTTPClient != nil {
		hc = *cfg.HTTPClient
	}
	// Go forwards nonstandard authentication headers across redirects. Disable
	// redirects even when the injected client has a permissive redirect policy.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Jar = nil
	u.Path = strings.TrimRight(u.Path, "/")
	return &Client{base: u, key: cfg.APIKey, http: &hc}, nil
}

func validateID(id string) error {
	if !identifier.MatchString(id) {
		return errors.New("cube: invalid identifier")
	}
	return nil
}

func validateTimeout(seconds int) error {
	if seconds < 0 || int64(seconds) > 2147483647 {
		return errors.New("cube: invalid idle timeout")
	}
	return nil
}

func prepareCreate(in CreateRequest) (CreateRequest, error) {
	if err := validateID(in.TemplateID); err != nil {
		return in, err
	}
	if err := validateTimeout(in.TimeoutSeconds); err != nil {
		return in, err
	}
	if in.Network == nil {
		return in, errors.New("cube: explicit network policy is required")
	}
	if in.Lifecycle == nil {
		in.Lifecycle = &Lifecycle{OnTimeout: "pause", AutoResume: true}
	}
	if in.Lifecycle.OnTimeout != "pause" {
		return in, errors.New("cube: lifecycle must pause on idle timeout")
	}
	if in.Backend != "" && in.Backend != "xfs" && in.Backend != "s3" {
		return in, errors.New("cube: unsupported snapshot backend")
	}
	// Cube promotes this metadata field into a host-directory mount annotation.
	for key := range in.Metadata {
		if strings.EqualFold(key, "host-mount") {
			return in, errors.New("cube: host mount metadata is forbidden")
		}
	}
	for key, value := range in.EnvVars {
		if !envName.MatchString(key) || strings.ContainsRune(value, '\x00') {
			return in, errors.New("cube: invalid environment variable")
		}
	}
	policy := *in.Network
	// Always serialize arrays, not null: policy is explicit even when empty.
	if policy.AllowOut == nil {
		policy.AllowOut = []string{}
	}
	if policy.DenyOut == nil {
		policy.DenyOut = []string{}
	}
	in.Network = &policy
	return in, nil
}

func (c *Client) Create(ctx context.Context, in CreateRequest) (*Sandbox, error) {
	in, err := prepareCreate(in)
	if err != nil {
		return nil, err
	}
	if c.admission != nil {
		return c.admittedCreate(ctx, in)
	}
	return c.createRaw(ctx, in)
}
func (c *Client) createRaw(ctx context.Context, in CreateRequest) (*Sandbox, error) {
	var out Sandbox
	if err := c.do(ctx, "create", http.MethodPost, "/sandboxes", in, &out, standardTimeout, http.StatusCreated); err != nil {
		return nil, err
	}
	if err := validateSandbox(&out, ""); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateFromSnapshot restores a private snapshot, including memory and secrets.
// This primitive MUST NOT be used for a cross-owner remix without sanitization.
// Cube snapshots are templates; the wire operation is POST /sandboxes.
func (c *Client) CreateFromSnapshot(ctx context.Context, snapshotID string, in CreateRequest) (*Sandbox, error) {
	if err := validateID(snapshotID); err != nil {
		return nil, err
	}
	in.TemplateID = snapshotID
	return c.Create(ctx, in)
}

func (c *Client) Get(ctx context.Context, id string) (*Sandbox, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	if c.admission == nil {
		return c.getRaw(ctx, id)
	}
	old, lookup := c.admission.store.AdmissionLookup(ctx, id)
	out, err := c.getRaw(ctx, id)
	var upstream *APIError
	if lookup == nil && ((old.State == "active" && err == nil && out.State == "paused") || ((old.State == "active" || old.State == "released") && errors.As(err, &upstream) && upstream.StatusCode == 404)) {
		if releaseErr := c.admission.store.AdmissionObserveReleased(ctx, old, err != nil); releaseErr != nil {
			return nil, releaseErr
		}
	}
	return out, err
}
func (c *Client) getRaw(ctx context.Context, id string) (*Sandbox, error) {
	var out Sandbox
	if err := c.do(ctx, "get", http.MethodGet, "/sandboxes/"+id, nil, &out, standardTimeout, http.StatusOK); err != nil {
		return nil, err
	}
	if err := validateSandbox(&out, id); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Connect(ctx context.Context, id string, in ConnectRequest) (*Sandbox, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	if err := validateTimeout(in.TimeoutSeconds); err != nil {
		return nil, err
	}
	if c.admission != nil {
		return c.admittedConnect(ctx, id, in)
	}
	return c.connectRaw(ctx, id, in)
}
func (c *Client) connectRaw(ctx context.Context, id string, in ConnectRequest) (*Sandbox, error) {
	var out Sandbox
	if err := c.do(ctx, "connect", http.MethodPost, "/sandboxes/"+id+"/connect", in, &out, lifecycleTimeout, http.StatusOK); err != nil {
		return nil, err
	}
	if err := validateSandbox(&out, id); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Pause(ctx context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	if c.admission != nil {
		return c.admittedRelease(ctx, id, "pause")
	}
	return c.pauseRaw(ctx, id)
}
func (c *Client) pauseRaw(ctx context.Context, id string) error {
	return c.do(ctx, "pause", http.MethodPost, "/sandboxes/"+id+"/pause", struct{}{}, nil, lifecycleTimeout, http.StatusNoContent)
}

func (c *Client) Delete(ctx context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	if c.admission != nil {
		return c.admittedRelease(ctx, id, "delete")
	}
	return c.deleteRaw(ctx, id)
}
func (c *Client) deleteRaw(ctx context.Context, id string) error {
	return c.do(ctx, "delete", http.MethodDelete, "/sandboxes/"+id, nil, nil, standardTimeout, http.StatusNoContent)
}

func (c *Client) CreateSnapshot(ctx context.Context, id string, in SnapshotRequest) (*Snapshot, error) {
	if c.admission != nil {
		return nil, errors.New("Cube memory snapshots are not supported by the bounded admission profile; use scoped source publication")
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	if len(in.Name) > 256 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return nil, errors.New("cube: invalid snapshot name")
	}
	if in.Backend != "" && in.Backend != "xfs" && in.Backend != "s3" {
		return nil, errors.New("cube: unsupported snapshot backend")
	}
	var out Snapshot
	if err := c.do(ctx, "snapshot", http.MethodPost, "/sandboxes/"+id+"/snapshots", in, &out, snapshotTimeout, http.StatusCreated); err != nil {
		return nil, err
	}
	if validateID(out.SnapshotID) != nil {
		return nil, errors.New("cube snapshot: invalid response identifier")
	}
	return &out, nil
}

// DeleteSnapshot uses Cube's /templates dispatcher (there is no DELETE /snapshots).
// The caller must supply a known owned snapshot ID, never an arbitrary template ID.
func (c *Client) DeleteSnapshot(ctx context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	var out struct {
		TemplateID  string `json:"templateID"`
		OperationID string `json:"operationID"`
		Status      string `json:"status"`
	}
	if err := c.do(ctx, "delete snapshot", http.MethodDelete, "/templates/"+id, nil, &out, snapshotTimeout, http.StatusOK); err != nil {
		return err
	}
	if out.TemplateID != id || out.Status != "READY" || out.OperationID == "" {
		return errors.New("cube delete snapshot: incomplete response")
	}
	return nil
}

func validateSandbox(out *Sandbox, expected string) error {
	if validateID(out.SandboxID) != nil || validateID(out.TemplateID) != nil || (expected != "" && out.SandboxID != expected) {
		return errors.New("cube: invalid sandbox response identifier")
	}
	if out.State != "" && out.State != "running" && out.State != "paused" && out.State != "pausing" {
		return errors.New("cube: invalid sandbox response state")
	}
	return nil
}

func (c *Client) do(ctx context.Context, operation, method, path string, in, out any, timeout time.Duration, expected int) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("cube %s: invalid request", operation)
		}
		if len(data) > maxRequestBytes {
			return fmt.Errorf("cube %s: request too large", operation)
		}
		body = bytes.NewReader(data)
	}
	u := *c.base
	u.Path += path
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return fmt.Errorf("cube %s: invalid request", operation)
	}
	req.Header.Set("X-API-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return requestError(ctx, operation, "transport failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expected {
		retry, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if retry < 0 || retry > 86400 {
			retry = 0
		}
		return &APIError{Operation: operation, StatusCode: resp.StatusCode, RetryAfterSeconds: retry}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return requestError(ctx, operation, "response read failed", err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("cube %s: response too large", operation)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("cube %s: invalid JSON response", operation)
		}
	}
	return nil
}

func requestError(ctx context.Context, operation, fallback string, err error) error {
	// Never wrap arbitrary transport errors: they can include URLs, request
	// headers or server-controlled text. Preserve only cancellation semantics.
	if ctx.Err() != nil {
		return fmt.Errorf("cube %s: %w", operation, ctx.Err())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("cube %s: %w", operation, context.DeadlineExceeded)
	}
	var timeoutError net.Error
	if errors.As(err, &timeoutError) && timeoutError.Timeout() {
		return fmt.Errorf("cube %s: %w", operation, context.DeadlineExceeded)
	}
	return fmt.Errorf("cube %s: %s", operation, fallback)
}
