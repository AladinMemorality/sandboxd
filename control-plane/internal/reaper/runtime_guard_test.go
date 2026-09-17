package reaper

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestReapersLeaveCubeStateUntouched(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, "file::memory:?_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sb := &store.Sandbox{ID: "cube-maintenance", Status: "running", RuntimeProvider: "cube", CgroupPath: sql.NullString{String: "/must-not-read-host", Valid: true}, RuntimeBinding: &store.RuntimeBinding{Provider: "cube", RuntimeID: "vm-maintenance", TemplateID: "tpl", Domain: "cube.test", TokenCiphertext: []byte("sealed"), TokenNonce: []byte("nonce")}}
	if err = st.Create(ctx, sb); err != nil {
		t.Fatal(err)
	}
	candidates, err := st.ListIdleCandidates(ctx, time.Now().Add(time.Hour))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("fixture not eligible: %v %v", candidates, err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Nil Docker clients deliberately make any accidental Docker operation fail.
	idle := &Idle{Store: st, Log: log}
	if err = idle.tick(ctx); err != nil {
		t.Fatal(err)
	}
	pressure := &Pressure{Store: st, Log: log}
	pressure.stopOldestIdle(ctx, "test", 10)
	pressure.stopHeaviestRSS(ctx, "test", 1)
	got, err := st.Get(ctx, sb.ID)
	if err != nil || got.Status != "running" {
		t.Fatalf("remote state changed: %+v %v", got, err)
	}
}
