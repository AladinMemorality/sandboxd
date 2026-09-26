package cube

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrCapacityUnavailable = errors.New("Cube active capacity unavailable")
	ErrAdmissionCapacity   = ErrCapacityUnavailable
	ErrCreationBusy        = fmt.Errorf("%w: Cube creation pipeline busy", ErrCapacityUnavailable)
	ErrAdmissionPending    = errors.New("Cube allocation outcome pending operator reconciliation")
	ErrAdmissionUnknown    = errors.New("Cube runtime has no durable admission reservation")
)

type AdmissionRecord struct {
	Key, RuntimeID, TemplateID, Operation, Token, State string
	Charged                                             int
}
type AdmissionStore interface {
	AdmissionPolicy(context.Context, int, string) error
	AdmissionLookup(context.Context, string) (AdmissionRecord, error)
	AdmissionLookupKey(context.Context, string) (AdmissionRecord, error)
	AdmissionBegin(context.Context, string, string, string, string, string) (AdmissionRecord, error)
	AdmissionFinish(context.Context, AdmissionRecord, string, string) error
	AdmissionObserveReleased(context.Context, AdmissionRecord, bool) error
}
type AdmissionResources struct {
	CPUCount int `json:"cpu_count"`
	MemoryMB int `json:"memory_mb"`
}

// Production supports one uniform profile; tagged operator tests enumerate additional
// profiles without changing production entrypoint policy. max_active is an
// active-reservation bound, not a limit on the number of stored/paused apps.
type AdmissionConfig struct {
	MaxActive      int                           `json:"max_active"`
	WritableDiskMB int                           `json:"writable_disk_mb,omitempty"`
	StorageGuard   *StorageGuardConfig           `json:"storage_guard,omitempty"`
	CPUCount       int                           `json:"cpu_count"`
	MemoryMB       int                           `json:"memory_mb"`
	Templates      map[string]AdmissionResources `json:"templates"`
}

func ParseAdmissionConfig(raw string) (AdmissionConfig, error) {
	var cfg AdmissionConfig
	if len(raw) == 0 || len(raw) > 64<<10 {
		return cfg, errors.New("reviewed SANDBOXD_CUBE_ADMISSION JSON is required")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return cfg, errors.New("invalid Cube admission JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return cfg, errors.New("multiple admission JSON values")
	}
	return cfg, cfg.validate()
}
func (cfg AdmissionConfig) validate() error {
	if cfg.StorageGuard != nil {
		if err := cfg.validateStorageGuard(); err != nil {
			return err
		}
	}
	if cfg.MaxActive < 1 || cfg.MaxActive > 12 || !admissionProfileAllowed(cfg.CPUCount, cfg.MemoryMB) || len(cfg.Templates) == 0 {
		return errors.New("Cube admission requires a compiled reviewed uniform profile with at most 12 active reservations")
	}
	for id, r := range cfg.Templates {
		if validateID(id) != nil || r.CPUCount != cfg.CPUCount || r.MemoryMB != cfg.MemoryMB {
			return errors.New("Cube admission template differs from uniform resource profile")
		}
	}
	return nil
}

// ConfigureAdmission is called once before sharing a client with handlers.
// The DB adapter is shared by daemon and offline CLI under maintenance fencing.
func (c *Client) ConfigureAdmission(ctx context.Context, db AdmissionStore, cfg AdmissionConfig) error {
	if db == nil {
		return errors.New("Cube admission store missing")
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	// Template sets may grow after exact image review; the uniform resource and
	// capacity contract itself remains immutable in the durable policy row.
	profile := fmt.Sprintf("cpu=%d;memory_mb=%d", cfg.CPUCount, cfg.MemoryMB)
	if err := db.AdmissionPolicy(ctx, cfg.MaxActive, profile); err != nil {
		return err
	}
	if guarded, ok := db.(interface {
		ConfigureStorageGuard(context.Context, *StorageGuardConfig) error
	}); ok {
		if err := guarded.ConfigureStorageGuard(ctx, cfg.StorageGuard); err != nil {
			return err
		}
	} else if cfg.StorageGuard != nil {
		return errors.New("admission store cannot enforce storage guard")
	}
	templates := make(map[string]AdmissionResources, len(cfg.Templates))
	for id, r := range cfg.Templates {
		templates[id] = r
	}
	cfg.Templates = templates
	c.admission = &admissionGuard{store: db, config: cfg, createGate: make(chan struct{}, 1)}
	return nil
}

type admissionGuard struct {
	createGate chan struct{}
	store      AdmissionStore
	config     AdmissionConfig
}

func (g *admissionGuard) validRemote(remote *Sandbox) error {
	if remote == nil {
		return ErrAdmissionUnknown
	}
	expected, ok := g.config.Templates[remote.TemplateID]
	if !ok || remote.CPUCount != expected.CPUCount || remote.MemoryMB != expected.MemoryMB {
		return errors.New("Cube allocated runtime violates admission template/resource contract")
	}
	if remote.State != "running" && remote.State != "paused" {
		return errors.New("Cube admission requires authoritative running/paused state")
	}
	return nil
}
func admissionToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func (c *Client) admittedCreate(ctx context.Context, in CreateRequest) (*Sandbox, error) {
	g := c.admission
	if _, ok := g.config.Templates[in.TemplateID]; !ok {
		return nil, errors.New("Cube create template has no admission resource contract")
	}
	key := in.Metadata["sandboxd_id"]
	appID := in.Metadata["sandboxd_app_id"]
	if validateID(key) != nil || validateID(appID) != nil {
		return nil, errors.New("Cube admission requires stable sandboxd_id and sandboxd_app_id metadata")
	}
	if in.Lifecycle == nil || in.Lifecycle.AutoResume {
		return nil, errors.New("Cube admission requires provider autoResume disabled")
	}
	// The pinned worker permits only one create workflow at a time. Queue
	// locally before reserving; the DB separately fences independent clients and
	// uncertain creates across restart. A canceled waiter never reaches provider.
	select {
	case g.createGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-g.createGate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token, err := admissionToken()
	if err != nil {
		return nil, err
	}
	lease, err := g.store.AdmissionBegin(ctx, "app:"+appID, "", in.TemplateID, "create", token)
	if err != nil {
		return nil, err
	}
	metadata := make(map[string]string, len(in.Metadata)+1)
	for k, v := range in.Metadata {
		metadata[k] = v
	}
	metadata["sandboxd_admission_operation"] = token
	in.Metadata = metadata
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*standardTimeout)
	defer cancel()
	ctx = operationCtx
	// Every failure after reservation deliberately retains it. No POST retries,
	// no timeout release: even an error response can race a completed allocation.
	remote, err := c.createRaw(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("%w: create outcome requires review", ErrAdmissionPending)
	}
	actual, err := c.getRaw(ctx, remote.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("%w: create verification unavailable", ErrAdmissionPending)
	}
	if err = g.validRemote(actual); err != nil {
		return nil, fmt.Errorf("%w: resource/state verification failed", ErrAdmissionPending)
	}
	if actual.TemplateID != in.TemplateID || actual.Metadata["sandboxd_id"] != key || actual.Metadata["sandboxd_app_id"] != appID || actual.Metadata["sandboxd_admission_operation"] != token {
		return nil, fmt.Errorf("%w: creation identity verification failed", ErrAdmissionPending)
	}
	if err = g.store.AdmissionFinish(ctx, lease, remote.SandboxID, "active"); err != nil {
		return nil, fmt.Errorf("%w: cannot acknowledge create", ErrAdmissionPending)
	}
	return remote, nil
}
func (c *Client) admittedConnect(ctx context.Context, id string, in ConnectRequest) (*Sandbox, error) {
	// Get observes auto-pauses with generation fencing; no allocation happens.
	remote, err := c.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	g := c.admission
	if err = g.validRemote(remote); err != nil {
		return nil, err
	}
	old, err := g.store.AdmissionLookup(ctx, id)
	if err != nil {
		return nil, ErrAdmissionUnknown
	}
	token, err := admissionToken()
	if err != nil {
		return nil, err
	}
	lease, err := g.store.AdmissionBegin(ctx, old.Key, id, remote.TemplateID, "connect", token)
	if err != nil {
		return nil, err
	}
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lifecycleTimeout+standardTimeout)
	defer cancel()
	ctx = operationCtx
	out, err := c.connectRaw(ctx, id, in)
	if err != nil {
		return nil, fmt.Errorf("%w: connect outcome requires review", ErrAdmissionPending)
	}
	actual, err := c.getRaw(ctx, id)
	if err != nil || g.validRemote(actual) != nil || actual.TemplateID != remote.TemplateID || actual.State != "running" {
		return nil, fmt.Errorf("%w: connect state verification failed", ErrAdmissionPending)
	}
	if err = g.store.AdmissionFinish(ctx, lease, id, "active"); err != nil {
		return nil, fmt.Errorf("%w: cannot acknowledge connect", ErrAdmissionPending)
	}
	return out, nil
}
func (c *Client) admittedRelease(ctx context.Context, id, operation string) error {
	g := c.admission
	remote, err := c.Get(ctx, id)
	if err != nil {
		var upstream *APIError
		if operation == "delete" && errors.As(err, &upstream) && upstream.StatusCode == 404 {
			row, lookup := g.store.AdmissionLookup(ctx, id)
			if lookup != nil {
				return ErrAdmissionUnknown
			}
			if row.State == "deleted" {
				return nil
			}
			return ErrAdmissionPending
		}
		return err
	}
	if err = g.validRemote(remote); err != nil {
		return err
	}
	if operation == "pause" && remote.State == "paused" {
		return nil
	}
	old, err := g.store.AdmissionLookup(ctx, id)
	if err != nil {
		return ErrAdmissionUnknown
	}
	token, err := admissionToken()
	if err != nil {
		return err
	}
	lease, err := g.store.AdmissionBegin(ctx, old.Key, id, remote.TemplateID, operation, token)
	if err != nil {
		return err
	}
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lifecycleTimeout+standardTimeout)
	defer cancel()
	ctx = operationCtx
	if operation == "pause" {
		err = c.pauseRaw(ctx, id)
	} else {
		err = c.deleteRaw(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("%w: %s outcome requires review", ErrAdmissionPending, operation)
	}
	actual, observed := c.getRaw(ctx, id)
	released := observed == nil && actual.TemplateID == remote.TemplateID && actual.State == "paused"
	if operation == "delete" {
		var apiErr *APIError
		released = errors.As(observed, &apiErr) && apiErr.StatusCode == 404
	}
	if !released {
		return fmt.Errorf("%w: release not authoritatively observed", ErrAdmissionPending)
	}
	if err = g.store.AdmissionFinish(ctx, lease, id, releaseState(operation)); err != nil {
		return fmt.Errorf("%w: cannot acknowledge release", ErrAdmissionPending)
	}
	return nil
}

// AdoptAdmission resolves only a positively identified pending create. It does
// not issue another POST or clear unknown allocations. Operator migration
// adoption additionally verifies the retained supervisor credential.
func (c *Client) AdoptAdmission(ctx context.Context, id string) error {
	if c.admission == nil {
		return nil
	}
	actual, err := c.getRaw(ctx, id)
	if err != nil {
		return err
	}
	g := c.admission
	if err = g.validRemote(actual); err != nil {
		return err
	}
	app := actual.Metadata["sandboxd_app_id"]
	if validateID(app) != nil {
		return ErrAdmissionUnknown
	}
	a, err := g.store.AdmissionLookupKey(ctx, "app:"+app)
	if err != nil {
		return ErrAdmissionUnknown
	}
	if a.State == "active" && a.RuntimeID == id && a.TemplateID == actual.TemplateID {
		return nil
	}
	if a.State != "pending" || a.Operation != "create" || a.RuntimeID != "" || a.TemplateID != actual.TemplateID || actual.Metadata["sandboxd_admission_operation"] != a.Token {
		return ErrAdmissionPending
	}
	return g.store.AdmissionFinish(ctx, a, id, "active")
}

func releaseState(operation string) string {
	if operation == "delete" {
		return "deleted"
	}
	return "released"
}

// ReconcileAdmission repairs a pending known-runtime mutation only during
// offline maintenance AFTER the operator has drained provider requests. A GET
// alone cannot rule out a timed-out remote Connect completing later.
func (c *Client) ReconcileAdmission(ctx context.Context, key string, providerRequestsDrained bool) error {
	if c.admission == nil || !providerRequestsDrained {
		return errors.New("offline provider request-drain fence required")
	}
	a, err := c.admission.store.AdmissionLookupKey(ctx, key)
	if err != nil {
		return ErrAdmissionUnknown
	}
	if a.State != "pending" || a.RuntimeID == "" || a.Operation == "create" {
		return ErrAdmissionPending
	}
	remote, err := c.getRaw(ctx, a.RuntimeID)
	state := "active"
	if err != nil {
		var upstream *APIError
		if !errors.As(err, &upstream) || upstream.StatusCode != 404 {
			return err
		}
		state = "deleted"
	} else {
		if err = c.admission.validRemote(remote); err != nil {
			return err
		}
		if remote.TemplateID != a.TemplateID {
			return ErrAdmissionPending
		}
		if remote.State == "paused" {
			state = "released"
		}
	}
	return c.admission.store.AdmissionFinish(ctx, a, a.RuntimeID, state)
}
