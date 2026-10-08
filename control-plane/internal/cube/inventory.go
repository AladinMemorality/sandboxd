package cube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Inventory includes retained/stopped guests across the entire fleet. The v1
// endpoint's default page is too small even when each worker is below capacity.
// v2 accepts an explicit bound; refuse a full page rather than reconcile against
// a potentially truncated identity set. No state or metadata filter is allowed.
const inventoryLimit = 4096

// CheckReadiness checks the workers accepting new allocations. Draining
// workers retain their bindings for recovery, but their availability must not
// stop healthy workers from serving requests or prevent controller startup.
func (c *Client) CheckReadiness(ctx context.Context) error {
	if c.fleet != nil {
		if len(c.fleet.order) == 0 {
			return errors.New("no worker accepts allocations")
		}
		for _, id := range c.fleet.order {
			worker := c.fleet.workers[id]
			if worker == nil || worker.admission == nil {
				return errors.New("worker admission missing")
			}
			if _, err := worker.InventoryOnNode(ctx, worker.admission.config.NodeID); err != nil {
				return err
			}
		}
		return nil
	}
	if c.admission != nil && c.admission.config.NodeID != "" {
		_, err := c.InventoryOnNode(ctx, c.admission.config.NodeID)
		return err
	}
	_, err := c.Inventory(ctx)
	return err
}

func (c *Client) Inventory(ctx context.Context) ([]Sandbox, error) {
	var out []Sandbox
	if e := c.doQuery(ctx, "inventory", http.MethodGet, "/v2/sandboxes", "limit=4096", nil, &out, standardTimeout, http.StatusOK); e != nil {
		return nil, e
	}
	if out == nil || len(out) >= inventoryLimit {
		return nil, errors.New("Cube inventory is incomplete")
	}
	seen := map[string]bool{}
	for _, v := range out {
		if validateID(v.SandboxID) != nil || seen[v.SandboxID] {
			return nil, errors.New("invalid Cube inventory identity")
		}
		seen[v.SandboxID] = true
	}
	return out, nil
}

// InventoryOnNode uses the fail-closed single-node Master endpoint. Unlike
// fleet inventory it never enumerates, observes or contacts another worker.
func (c *Client) InventoryOnNode(ctx context.Context, node string) ([]Sandbox, error) {
	if node == "" || c.inventoryNode == nil {
		return nil, errors.New("worker inventory requires pinned placement observer")
	}
	return c.inventoryNode(ctx, node)
}

// ObserveOnNode enriches an already scoped identity without changing admission
// state. Pause-only inventory rows may omit resources and labels.
func (c *Client) ObserveOnNode(ctx context.Context, id, node string) (*Sandbox, error) {
	if c.observation == nil {
		return nil, errors.New("placement observer missing")
	}
	value, err := c.observation(ctx, id)
	if err != nil {
		return nil, err
	}
	if value.ClientID != node {
		return nil, errors.New("runtime moved outside observed worker")
	}
	return value, nil
}

func (c *Client) masterNodeInventory(ctx context.Context, origin *url.URL, node string) ([]Sandbox, error) {
	target := *origin
	target.Path = "/cube/sandbox/inventory"
	target.RawQuery = url.Values{"host_id": {node}}.Encode()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Caller", masterControlCaller)
	response, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("node inventory unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("node inventory rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return nil, errors.New("node inventory exceeds bound")
	}
	var result struct {
		Ret struct {
			Code *int `json:"ret_code"`
		} `json:"ret"`
		Data []struct {
			ID        string            `json:"sandbox_id"`
			Host      string            `json:"host_id"`
			Template  string            `json:"template_id"`
			Status    int               `json:"status"`
			CPU       int               `json:"cpu_count"`
			Memory    int               `json:"memory_mb"`
			CPUMilli  int               `json:"cpu_milli"`
			MemoryMiB int               `json:"memory_mib"`
			Labels    map[string]string `json:"labels"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Ret.Code == nil || *result.Ret.Code != 200 || result.Data == nil || len(result.Data) >= inventoryLimit {
		return nil, errors.New("incomplete node inventory")
	}
	out := make([]Sandbox, 0, len(result.Data))
	seen := map[string]bool{}
	for _, v := range result.Data {
		if validateID(v.ID) != nil || seen[v.ID] || v.Host != node {
			return nil, errors.New("invalid node inventory identity")
		}
		seen[v.ID] = true
		state := "unknown"
		switch v.Status {
		case 1:
			state = "running"
		case 2:
			state = "stopped"
		case 5:
			state = "paused"
		}
		cpu, memory := v.CPU, v.Memory
		if cpu == 0 && v.CPUMilli > 0 && v.CPUMilli%1000 == 0 {
			cpu = v.CPUMilli / 1000
		}
		if memory == 0 {
			memory = v.MemoryMiB
		}
		out = append(out, Sandbox{SandboxID: v.ID, ClientID: v.Host, TemplateID: v.Template, State: state, CPUCount: cpu, MemoryMB: memory, Metadata: v.Labels})
	}
	return out, nil
}
