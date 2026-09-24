package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeAdmissionWiringRequiresReviewedProfileOnlyWhenEnabled(t *testing.T) {
	ctx := context.Background()
	if err := configureCubeAdmission(ctx, cubeConfig{}, nil); err != nil {
		t.Fatal("Docker-only startup changed", err)
	}
	client, err := cube.New(cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := cubeConfig{client: client, templates: map[string]string{"react-pro": "tpl-reviewed"}}
	st, err := store.Open(ctx, "file:"+filepath.Join(t.TempDir(), "state.db")+"?_journal=WAL", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t.Setenv("SANDBOXD_CUBE_ADMISSION", "")
	if err = configureCubeAdmission(ctx, cfg, st); err == nil {
		t.Fatal("enabled Cube has no hard admission")
	}
	t.Setenv("SANDBOXD_CUBE_ADMISSION", `{"max_active":12,"cpu_count":2,"memory_mb":2048,"templates":{"tpl-reviewed":{"cpu_count":2,"memory_mb":2048}}}`)
	if err = configureCubeAdmission(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}
	cfg.templates["nextjs"] = "unreviewed"
	if err = configureCubeAdmission(ctx, cfg, st); err == nil {
		t.Fatal("preset missing capacity contract accepted")
	}
}
