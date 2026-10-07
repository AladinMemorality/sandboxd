package cube

import (
	"context"
	"errors"
	"net/http"
)

// Inventory includes retained/stopped guests across the entire fleet. The v1
// endpoint's default page is too small even when each worker is below capacity.
// v2 accepts an explicit bound; refuse a full page rather than reconcile against
// a potentially truncated identity set. No state or metadata filter is allowed.
const inventoryLimit = 4096

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
