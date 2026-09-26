package cubeconfig

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestCubeAdmissionWiringRequiresReviewedProfileOnlyWhenEnabled(t *testing.T) {
	ctx := context.Background()
	if err := ConfigureAdmission(ctx, Config{}, nil); err != nil {
		t.Fatal("Docker-only startup changed", err)
	}
	Client, err := cube.New(cube.Config{APIURL: "http://127.0.0.1:1", APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Client: Client, Templates: map[string]string{"react-pro": "tpl-reviewed"}}
	st, err := store.Open(ctx, "file:"+filepath.Join(t.TempDir(), "state.db")+"?_journal=WAL", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t.Setenv("SANDBOXD_CUBE_ADMISSION", "")
	if err = ConfigureAdmission(ctx, cfg, st); err == nil {
		t.Fatal("enabled Cube has no hard admission")
	}
	t.Setenv("SANDBOXD_CUBE_ADMISSION", `{"max_active":12,"cpu_count":2,"memory_mb":2048,"templates":{"tpl-reviewed":{"cpu_count":2,"memory_mb":2048}}}`)
	if err = ConfigureAdmission(ctx, cfg, st); err == nil {
		t.Fatal("production startup accepted missing disk guard")
	}
	admission := cube.AdmissionConfig{MaxActive: 4, WritableDiskMB: 10240, CPUCount: 2, MemoryMB: 2048, Templates: map[string]cube.AdmissionResources{"tpl-reviewed": {CPUCount: 2, MemoryMB: 2048}}, StorageGuard: &cube.StorageGuardConfig{OuterBootID: "66666666-6666-6666-6666-666666666666", ObservationPath: "/run/cube-storage/observation.json", ObserverID: "11111111111111111111111111111111", WorkerMachineID: "22222222222222222222222222222222", ExpectedBootID: "33333333-3333-3333-3333-333333333333", InnerFSUUID: "44444444-4444-4444-4444-444444444444", OuterFSUUID: "55555555-5555-5555-5555-555555555555"}}
	raw, _ := json.Marshal(admission)
	t.Setenv("SANDBOXD_CUBE_ADMISSION", string(raw))
	if err = ConfigureAdmission(ctx, cfg, st); err != nil {
		t.Fatal(err)
	}
	cfg.Templates["nextjs"] = "unreviewed"
	if err = ConfigureAdmission(ctx, cfg, st); err == nil {
		t.Fatal("preset missing capacity contract accepted")
	}
}
