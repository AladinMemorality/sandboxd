package cube

import (
	"context"
	"errors"
	"net/http"
)

// Inventory reads pinned v0.7.1's unfiltered all-state endpoint. The worker total
// limit is128; v1 truncates at200. Hitting that bound is not complete inventory.
func (c *Client) Inventory(ctx context.Context) ([]Sandbox, error) {
	var out []Sandbox
	if e := c.do(ctx, "inventory", http.MethodGet, "/sandboxes", nil, &out, standardTimeout, http.StatusOK); e != nil {
		return nil, e
	}
	if out == nil || len(out) >= 200 {
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
