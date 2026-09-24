package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type CubeModelScope struct {
	TaskID     string
	SandboxID  string
	BridgeHash []byte `json:"-"`
	ExpiresAt  time.Time
}

func (s *Store) CreateCubeModelScope(ctx context.Context, scope CubeModelScope) error {
	return s.submit(ctx, func(db *sql.DB) error {
		result, err := db.ExecContext(ctx, `INSERT INTO cube_model_scope(task_id,sandbox_id,bridge_hash,expires_at)
   SELECT t.task_id,t.sandbox_id,?,? FROM task t JOIN sandbox s ON s.id=t.sandbox_id
   WHERE t.task_id=? AND t.sandbox_id=? AND t.status='running' AND s.runtime_provider='cube'`, scope.BridgeHash, scope.ExpiresAt.Unix(), scope.TaskID, scope.SandboxID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrNotFound
		}
		return nil
	})
}

// CubeModelScopeFor authenticates the durable lifecycle every time; scopes are
// deleted by a database trigger at task completion and a FK at VM deletion.
func (s *Store) CubeModelScopeFor(ctx context.Context, taskID, sandboxID string) (*CubeModelScope, error) {
	out := &CubeModelScope{}
	var expires int64
	err := s.db.QueryRowContext(ctx, `SELECT m.task_id,m.sandbox_id,m.bridge_hash,m.expires_at
 FROM cube_model_scope m JOIN task t ON t.task_id=m.task_id JOIN sandbox s ON s.id=m.sandbox_id
 WHERE m.task_id=? AND m.sandbox_id=? AND t.status='running' AND s.status='running' AND s.runtime_provider='cube'`, taskID, sandboxID).Scan(&out.TaskID, &out.SandboxID, &out.BridgeHash, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out.ExpiresAt = time.Unix(expires, 0)
	if !time.Now().Before(out.ExpiresAt) {
		return nil, ErrNotFound
	}
	return out, nil
}
