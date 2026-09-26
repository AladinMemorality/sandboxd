package store

import (
	"context"
	"errors"
)

// CheckCubeOnly is a read-only retirement gate. It never converts provider
// metadata: migrations must transfer and verify each workspace first. Apps
// without a current sandbox are valid; their next creation uses Cube globally.
func (s *Store) CheckCubeOnly(ctx context.Context) error {
	var invalid int
	err := s.db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM sandbox s
		 LEFT JOIN runtime_binding b ON b.sandbox_id=s.id
		 LEFT JOIN app a ON a.id=s.app_id
		 LEFT JOIN app_runtime ar ON ar.app_id=a.id
		 LEFT JOIN runtime_task_scope ts ON ts.sandbox_id=s.id
		 WHERE s.runtime_provider!='cube' OR b.provider IS NULL OR b.provider!='cube'
		 OR b.runtime_id='' OR b.template_id='' OR b.domain=''
		 OR length(b.token_ciphertext)=0 OR length(b.token_nonce)=0
		 OR a.id IS NULL OR ar.provider IS NULL OR ar.provider!='cube'
		 OR ts.provider IS NULL OR ts.provider!='cube' OR ts.owner_token!=a.owner_token)
		OR EXISTS(SELECT 1 FROM app_runtime WHERE provider!='cube')`).Scan(&invalid)
	if err != nil {
		return err
	}
	if invalid != 0 {
		return errors.New("Cube controller requires a completely migrated fleet with valid ownership and runtime bindings")
	}
	return nil
}
