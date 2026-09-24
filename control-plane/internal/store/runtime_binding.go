package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RuntimeBinding is administrator-managed routing metadata, separate from the
// durable Baarcha sandbox ID. Supervisor credentials never appear in API JSON.
type RuntimeBinding struct {
	SandboxID             string
	Provider              string
	RuntimeID             string
	TemplateID            string
	Domain                string
	ConfigRevision        int64
	ConfigAppliedRevision int64
	TokenCiphertext       []byte `json:"-"`
	TokenNonce            []byte `json:"-"`
}

func insertRuntimeBinding(ctx context.Context, tx *sql.Tx, id string, b *RuntimeBinding) error {
	if b.Provider != "cube" || b.RuntimeID == "" || b.TemplateID == "" || b.Domain == "" || len(b.TokenCiphertext) == 0 || len(b.TokenNonce) == 0 {
		return fmt.Errorf("incomplete runtime binding")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO runtime_binding (sandbox_id,provider,runtime_id,template_id,domain,token_ciphertext,token_nonce) VALUES (?,?,?,?,?,?,?)`, id, b.Provider, b.RuntimeID, b.TemplateID, b.Domain, b.TokenCiphertext, b.TokenNonce)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runtime_binding SET config_revision=1 WHERE sandbox_id=? AND EXISTS (SELECT 1 FROM sandbox s JOIN app_config c ON c.app_id=s.app_id WHERE s.id=? AND c.access_policy IN ('runtime_access','both'))`, id, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO app_runtime(app_id,provider) SELECT app_id,'cube' FROM sandbox WHERE id=? AND app_id IS NOT NULL`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_task_scope(sandbox_id,owner_token,provider) SELECT s.id,a.owner_token,'cube' FROM sandbox s JOIN app a ON a.id=s.app_id WHERE s.id=?`, id)
	return err
}

// CubeTaskOwner preserves the tenant boundary for results after VM deletion.
func (s *Store) CubeTaskOwner(ctx context.Context, id string) (string, error) {
	var owner string
	err := s.db.QueryRowContext(ctx, `SELECT owner_token FROM runtime_task_scope WHERE sandbox_id=?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return owner, err
}

func (s *Store) GetRuntimeBinding(ctx context.Context, id string) (*RuntimeBinding, error) {
	b := &RuntimeBinding{}
	err := s.db.QueryRowContext(ctx, `SELECT sandbox_id,provider,runtime_id,template_id,domain,token_ciphertext,token_nonce,config_revision,config_applied_revision FROM runtime_binding WHERE sandbox_id=?`, id).Scan(&b.SandboxID, &b.Provider, &b.RuntimeID, &b.TemplateID, &b.Domain, &b.TokenCiphertext, &b.TokenNonce, &b.ConfigRevision, &b.ConfigAppliedRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *Store) IsCube(ctx context.Context, id string) (bool, error) {
	var provider string
	err := s.db.QueryRowContext(ctx, `SELECT runtime_provider FROM sandbox WHERE id=?`, id).Scan(&provider)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return provider == "cube", err
}

// MarkCubeConfigApplied never acknowledges an older revision over a newer write.
func (s *Store) MarkCubeConfigApplied(ctx context.Context, id string, revision int64) error {
	return s.submit(ctx, func(db *sql.DB) error {
		result, err := db.ExecContext(ctx, `UPDATE runtime_binding SET config_applied_revision=? WHERE sandbox_id=? AND config_revision=?`, revision, id, revision)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrConflict
		}
		return nil
	})
}
