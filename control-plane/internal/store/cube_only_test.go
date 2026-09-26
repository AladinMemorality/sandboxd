package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestCubeOnlyRequiresMigratedBindingsAndOwnership(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.CheckCubeOnly(ctx); err != nil {
		t.Fatal(err)
	}
	legacy := &Sandbox{ID: "legacy", Status: "stopped"}
	if err := st.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckCubeOnly(ctx); err == nil {
		t.Fatal("unmigrated Docker accepted")
	}
	if err := st.Delete(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	app := &App{ID: "app", OwnerToken: "owner", Name: "Existing project"}
	if err := st.CreateApp(ctx, app); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckCubeOnly(ctx); err != nil {
		t.Fatal("app awaiting its first runtime", err)
	}
	sb := &Sandbox{ID: "cube", Status: "stopped", RuntimeProvider: "cube", AppID: sql.NullString{String: app.ID, Valid: true},
		RuntimeBinding: &RuntimeBinding{Provider: "cube", RuntimeID: "vm", TemplateID: "tpl", Domain: "cube.test", TokenCiphertext: []byte("cipher"), TokenNonce: []byte("nonce")}}
	if err := st.Create(ctx, sb); err != nil {
		t.Fatal(err)
	}
	if err := st.CheckCubeOnly(ctx); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{
		`UPDATE runtime_task_scope SET owner_token='foreign'`,
		`DELETE FROM runtime_task_scope`,
		`DELETE FROM app_runtime`,
		`DELETE FROM runtime_binding`,
	} {
		tx, err := st.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Commit changes because CheckCubeOnly intentionally uses an independent
		// connection. Restore each fixture from the original schema contracts.
		if _, err = tx.ExecContext(ctx, change); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = st.CheckCubeOnly(ctx); err == nil {
			t.Fatalf("corrupt binding accepted: %s", change)
		}
		if _, err = st.DB().ExecContext(ctx, `INSERT OR REPLACE INTO runtime_task_scope(sandbox_id,owner_token,provider) VALUES('cube','owner','cube'); INSERT OR REPLACE INTO app_runtime(app_id,provider) VALUES('app','cube')`); err != nil {
			t.Fatal(err)
		}
	}
}
