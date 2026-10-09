package cube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"time"
)

// CubeMaster keys its worker gRPC pools by X-Caller. Keep latency-sensitive
// observations and resume calls off the default connection used by bulk fleet
// inventory. This fixed label is not authentication and never caches state.
const masterControlCaller = "baarcha-controller"

// One authoritative CubeMaster read supplies both runtime state/resources and
// placement. Unlike CubeAPI GET, it does not enumerate the worker for a summary.
// Never cache this observation across operations or follow guest-provided URLs.
func (c *Client) getPlaced(ctx context.Context, id string) (*Sandbox, error) {
	if c.observation != nil && c.admission != nil && c.admission.config.NodeID != "" {
		out, err := c.observation(ctx, id)
		if err != nil {
			return nil, err
		}
		if out.ClientID != c.admission.config.NodeID {
			return nil, ErrAdmissionPending
		}
		return out, nil
	}
	out, err := c.getRaw(ctx, id)
	if err == nil && c.admission != nil && c.admission.config.NodeID != "" && c.placement(ctx, id, c.admission.config.NodeID) != nil {
		return nil, ErrAdmissionPending
	}
	return out, err
}

func decodeMasterObservation(raw []byte, id string) (*Sandbox, error) {
	var result struct {
		Ret struct {
			Code *int `json:"ret_code"`
		} `json:"ret"`
		Data []struct {
			ID         string            `json:"sandbox_id"`
			Host       string            `json:"host_id"`
			Template   string            `json:"template_id"`
			Status     *int              `json:"status"`
			EndAt      int64             `json:"end_at"`
			Labels     map[string]string `json:"labels"`
			Containers []struct {
				ID     string `json:"container_id"`
				CPU    int    `json:"cpu_milli"`
				Memory int    `json:"memory_mib"`
			} `json:"containers"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Ret.Code == nil {
		return nil, errors.New("invalid Cube runtime observation")
	}
	if *result.Ret.Code == 130404 || (*result.Ret.Code == 200 && len(result.Data) == 0) {
		return nil, &APIError{Operation: "get", StatusCode: 404}
	}
	if *result.Ret.Code != 200 || len(result.Data) != 1 {
		return nil, errors.New("invalid Cube runtime observation")
	}
	row := result.Data[0]
	if row.ID != id || row.Host == "" || row.Status == nil {
		return nil, errors.New("invalid Cube runtime identity")
	}
	out := &Sandbox{SandboxID: row.ID, TemplateID: row.Template, ClientID: row.Host, Metadata: row.Labels}
	switch *row.Status {
	case 1:
		out.State = "running"
	case 4:
		out.State = "pausing"
	case 5:
		out.State = "paused"
	default:
		out.State = "unknown"
	}
	if err := validateSandbox(out, id); err != nil {
		return nil, err
	}
	for _, container := range row.Containers {
		if container.ID == id {
			if out.CPUCount != 0 || container.CPU <= 0 || container.CPU%1000 != 0 || container.Memory <= 0 {
				return nil, errors.New("invalid Cube runtime resources")
			}
			out.CPUCount = container.CPU / 1000
			out.MemoryMB = container.Memory
		}
	}
	if out.CPUCount == 0 {
		return nil, errors.New("missing Cube runtime resource observation")
	}
	if row.EndAt > 0 {
		end := time.UnixMilli(row.EndAt)
		out.EndAt = &end
	}
	return out, nil
}

// Admission has already read this paused runtime. Use CubeMaster's same resume
// operation without CubeAPI repeating the before/after reads. The caller still
// verifies state/resources/placement afterward and retains uncertain admission.
var errNativeResumeBusy = errors.New("Cube native resume concurrency limit")

func (c *Client) nativeResume(origin *url.URL, instance string) func(context.Context, string, ConnectRequest) error {
	return func(ctx context.Context, id string, in ConnectRequest) error {
		if validateID(id) != nil {
			return ErrAdmissionUnknown
		}
		bounded, cancel := context.WithTimeout(ctx, lifecycleTimeout)
		defer cancel()
		target := *origin
		target.Path = "/cube/sandbox/update"
		for attempt := 0; ; attempt++ {
			if bounded.Err() != nil {
				return errNativeResumeBusy
			}
			requestID, err := admissionToken()
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]any{"requestID": requestID, "sandbox_id": id, "instance_type": instance, "action": "resume", "timeout": in.TimeoutSeconds})
			req, err := http.NewRequestWithContext(bounded, http.MethodPost, target.String(), bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Caller", masterControlCaller)
			response, err := c.http.Do(req)
			// A transport failure may have happened after allocation. Never replay it.
			if err != nil {
				return errors.New("Cube native resume unavailable")
			}
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
			response.Body.Close()
			if readErr != nil || len(raw) > maxResponseBytes || response.StatusCode != http.StatusOK {
				return errors.New("Cube native resume rejected")
			}
			var result struct {
				Ret struct {
					Code    *int   `json:"ret_code"`
					Message string `json:"ret_msg"`
				} `json:"ret"`
			}
			if json.Unmarshal(raw, &result) != nil || result.Ret.Code == nil {
				return errors.New("Cube native resume unconfirmed")
			}
			if *result.Ret.Code == 200 {
				return nil
			}
			// Cubelet's workflow Engine.run returns ConcurrentFailed before executing
			// any create/resume step (Limiter.TryAcquire). Retain one admission lease
			// while waiting; no other error, including a timeout, is safe to replay.
			if *result.Ret.Code != 130513 || result.Ret.Message != "flow [create] exceed limited" {
				return errors.New("Cube native resume unconfirmed")
			}
			limit := 250 * (attempt + 1)
			if limit > 1500 {
				limit = 1500
			}
			timer := time.NewTimer(time.Duration(limit+rand.Intn(250)) * time.Millisecond)
			select {
			case <-bounded.Done():
				timer.Stop()
				return errNativeResumeBusy
			case <-timer.C:
			}
		}
	}
}
