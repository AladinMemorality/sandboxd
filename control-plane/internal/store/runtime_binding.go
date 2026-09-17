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
	SandboxID       string
	Provider        string
	RuntimeID       string
	TemplateID      string
	Domain          string
	TokenCiphertext []byte `json:"-"`
	TokenNonce      []byte `json:"-"`
}

func insertRuntimeBinding(ctx context.Context, tx *sql.Tx, id string, b *RuntimeBinding) error {
	if b.Provider != "cube" || b.RuntimeID == "" || b.TemplateID == "" || b.Domain == "" || len(b.TokenCiphertext) == 0 || len(b.TokenNonce) == 0 {
		return fmt.Errorf("incomplete runtime binding")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO runtime_binding (sandbox_id,provider,runtime_id,template_id,domain,token_ciphertext,token_nonce) VALUES (?,?,?,?,?,?,?)`, id, b.Provider, b.RuntimeID, b.TemplateID, b.Domain, b.TokenCiphertext, b.TokenNonce)
	return err
}

func (s *Store) GetRuntimeBinding(ctx context.Context, id string) (*RuntimeBinding, error) {
	b := &RuntimeBinding{}
	err := s.db.QueryRowContext(ctx, `SELECT sandbox_id,provider,runtime_id,template_id,domain,token_ciphertext,token_nonce FROM runtime_binding WHERE sandbox_id=?`, id).Scan(&b.SandboxID, &b.Provider, &b.RuntimeID, &b.TemplateID, &b.Domain, &b.TokenCiphertext, &b.TokenNonce)
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
