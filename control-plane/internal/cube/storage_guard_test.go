package cube

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testStorageConfig(dir string) StorageGuardConfig {
	return StorageGuardConfig{OuterBootID: "66666666-6666-6666-6666-666666666666", ObservationPath: filepath.Join(dir, "observation.json"), ObserverID: "11111111111111111111111111111111", WorkerMachineID: "22222222222222222222222222222222", ExpectedBootID: "33333333-3333-3333-3333-333333333333", InnerFSUUID: "44444444-4444-4444-4444-444444444444", OuterFSUUID: "55555555-5555-5555-5555-555555555555"}
}
func TestStorageObservationRequiresTrustedBoundedFile(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned observation integration runs in Linux root test container")
	}
	now := time.Now()
	cfg := testStorageConfig(t.TempDir())
	o := StorageObservation{OuterBootID: cfg.OuterBootID, Version: 1, ObserverID: cfg.ObserverID, Generation: 1, StartedNS: now.Add(-time.Second).UnixNano(), CompletedNS: now.UnixNano(), WorkerMachineID: cfg.WorkerMachineID, WorkerBootID: cfg.ExpectedBootID, InnerFSUUID: cfg.InnerFSUUID, OuterFSUUID: cfg.OuterFSUUID, InnerFreeBytes: StorageBaseline, OuterFreeBytes: StorageBaseline}
	b, _ := json.Marshal(o)
	for _, kind := range []string{"valid", "symlink", "hardlink", "writable", "untrusted_owner", "directory", "oversize", "unknown_field", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			os.Remove(cfg.ObservationPath)
			if e := os.WriteFile(cfg.ObservationPath, b, 0600); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "symlink":
				os.Rename(cfg.ObservationPath, cfg.ObservationPath+".target")
				os.Symlink(cfg.ObservationPath+".target", cfg.ObservationPath)
			case "hardlink":
				os.Link(cfg.ObservationPath, cfg.ObservationPath+".hard")
			case "writable":
				os.Chmod(cfg.ObservationPath, 0666)
			case "untrusted_owner":
				os.Chown(cfg.ObservationPath, 65534, 65534)
			case "directory":
				os.Remove(cfg.ObservationPath)
				os.Mkdir(cfg.ObservationPath, 0700)
			case "oversize":
				os.WriteFile(cfg.ObservationPath, make([]byte, 8193), 0600)
			case "unknown_field":
				os.WriteFile(cfg.ObservationPath, append([]byte(`{"extra":true,`), b[1:]...), 0600)
			case "trailing":
				os.WriteFile(cfg.ObservationPath, append(b, []byte(` {}`)...), 0600)
			}
			_, e := ReadStorageObservation(cfg, StorageClock{BootID: cfg.OuterBootID, NS: now.UnixNano()})
			if kind == "valid" && e != nil {
				t.Fatal(e)
			}
			if kind != "valid" && !errors.Is(e, ErrStorageUnavailable) {
				t.Fatalf("accepted %s: %v", kind, e)
			}
		})
	}
}
func TestStorageGuardProductionContract(t *testing.T) {
	cfg := AdmissionConfig{MaxActive: 4, WritableDiskMB: 10240}
	if cfg.RequireStorageGuard() == nil {
		t.Fatal("unguarded production")
	}
	g := testStorageConfig("/run/cube")
	cfg.StorageGuard = &g
	if cfg.RequireStorageGuard() != nil {
		t.Fatal("valid guard rejected")
	}
	cfg.MaxActive = 12
	if cfg.RequireStorageGuard() == nil {
		t.Fatal("historical 12 slots accepted")
	}
}

func TestStorageGuardUsesKernelBootClock(t *testing.T) {
	clock, e := ReadStorageClock()
	if e != nil {
		t.Skip("Linux kernel clock checked in Linux container")
	}
	cfg := testStorageConfig("/run/cube")
	cfg.OuterBootID = clock.BootID
	o := StorageObservation{Version: 1, OuterBootID: clock.BootID, ObserverID: cfg.ObserverID, Generation: 1, StartedNS: clock.NS, CompletedNS: clock.NS, WorkerMachineID: cfg.WorkerMachineID, WorkerBootID: cfg.ExpectedBootID, InnerFSUUID: cfg.InnerFSUUID, OuterFSUUID: cfg.OuterFSUUID}
	later, e := ReadStorageClock()
	if e != nil || later.NS < clock.NS || o.Validate(cfg, later) != nil {
		t.Fatal("shared kernel observation clock invalid", e)
	}
	// Wall-clock timestamps cannot be substituted for boottime freshness.
	o.StartedNS = time.Now().UnixNano()
	o.CompletedNS = o.StartedNS
	if o.Validate(cfg, later) == nil {
		t.Fatal("wall clock accepted as monotonic time")
	}
}
