//go:build linux

package workerstop

import (
	"context"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type nodeProvider struct {
	*cube.Client
	node     string
	retained map[string]bool
}

func (p nodeProvider) Inventory(ctx context.Context) ([]cube.Sandbox, error) {
	all, err := p.Client.InventoryOnNode(ctx, p.node)
	if err != nil {
		return nil, err
	}
	owned, err := filterRetained(all, p.retained)
	if err != nil {
		return nil, err
	}
	for i, v := range owned {
		full, err := p.Client.ObserveOnNode(ctx, v.SandboxID, p.node)
		if err != nil {
			return nil, err
		}
		owned[i] = *full
	}
	return owned, nil
}

// Only explicitly reviewed, unbound and inactive records may be retained
// without mutation. A running/unknown record always blocks the entire stop.
func filterRetained(all []cube.Sandbox, retained map[string]bool) ([]cube.Sandbox, error) {
	out := []cube.Sandbox{}
	for _, v := range all {
		if retained[v.SandboxID] {
			if v.State != "paused" && v.State != "stopped" {
				return nil, errors.New("retained runtime is not inactive")
			}
			continue
		}
		out = append(out, v)
	}
	return out, nil
}
func scopeProvider(client *cube.Client, c Config) (Provider, error) {
	if c.WorkerID == "" {
		return client, nil
	}
	if c.WorkerID != "vps" || c.Admission.NodeID != "10.0.2.15" || c.MasterURL == "" {
		return nil, errors.New("VPS lifecycle scope requires placement identity")
	}
	if err := client.ConfigurePlacement(c.MasterURL, "cubebox"); err != nil {
		return nil, err
	}
	retained := map[string]bool{}
	{
		db, err := readDB(c)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		completed, err := store.RetainedRelocationSources(context.Background(), db, c.WorkerID)
		if err != nil {
			return nil, err
		}
		for _, id := range completed {
			retained[id] = true
		}
		for _, id := range c.RetainedInactive {
			var n int
			if err = db.QueryRow(`SELECT COUNT(*) FROM runtime_binding WHERE runtime_id=?`, id).Scan(&n); err != nil {
				return nil, err
			}
			if n != 0 {
				return nil, errors.New("retained inventory intersects canonical binding")
			}
			retained[id] = true
		}
	}
	return nodeProvider{Client: client, node: c.Admission.NodeID, retained: retained}, nil
}
