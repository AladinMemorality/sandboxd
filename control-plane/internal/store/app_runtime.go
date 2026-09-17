package store

import "context"

// AppUsesCube is durable provider identity, independent of the current sandbox
// or operator rollout allowlist. Removing a pilot flag never changes a Cube app
// back into a Docker app after its VM has been deleted.
func (s *Store) AppUsesCube(ctx context.Context, id string) (bool, error) {
	var yes int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM app_runtime WHERE app_id=? AND provider='cube')`, id).Scan(&yes)
	return yes == 1, err
}
