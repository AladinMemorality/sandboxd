package main

import (
	"encoding/json"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMissingBranchNeverTreatsAnyErrorOrInventoryAsAbsence(t *testing.T) {
	empty := "NODES_SCANNED 1/1\nSANDBOX_COUNT 0\n"
	if branch, err := oldStateBranch(&cube.APIError{StatusCode: 404}, empty, "owned"); err != nil || branch != "missing-after-worker-loss" {
		t.Fatal(branch, err)
	}
	for _, err := range []error{nil, errors.New("connection refused"), &cube.APIError{StatusCode: 500}, cube.ErrRuntimeUnavailable} {
		if _, e := oldStateBranch(err, empty, "owned"); e == nil {
			t.Fatal("false absence")
		}
	}
	for _, bad := range []string{"", strings.Replace(empty, "1/1", "0/1", 1), strings.Replace(empty, "COUNT 0", "COUNT 1", 1), empty + "0123456789abcdef0123456789abcdef unknown\n"} {
		if _, e := oldStateBranch(&cube.APIError{StatusCode: 404}, bad, "owned"); e == nil {
			t.Fatal("incomplete inventory")
		}
	}
	if !emptyCubeTasks("TASK    PID    STATUS\n") || emptyCubeTasks("") || emptyCubeTasks("TASK PID STATUS\nowned 12 RUNNING") {
		t.Fatal("task proof not exact")
	}
}
func TestMissingReceiptBindsExactCrashCaptureAndCurrentAbsence(t *testing.T) {
	old := escrow{Guest: &cube.Sandbox{SandboxID: "owned"}, Fixture: "fixture", WorkerMachineID: "machine", BootID: "old"}
	p := missingEvidence{Purpose: "OWNED_CURRENT_DISK_MISSING_PROVIDER", OldID: "owned", Fixture: "fixture", MachineID: "machine", PreviousBoot: "old", CurrentBoot: "new", ArchiveSHA: "archive", NoTask: true, NoVMM: true, Fenced: true, Checked: 990, Expires: 1100, Files: map[string]string{}}
	for _, n := range []string{"cubebox.json", "storage.json", "plan.json", "rescue-input.json", "fence.json", "export-report.json"} {
		p.Files[n] = strings.Repeat("a", 64)
	}
	if err := validateMissingReceipt(p, old, "archive", "new", 1000); err != nil {
		t.Fatal(err)
	}
	bads := []missingEvidence{p, p, p, p, p, p, p, p, p}
	bads[0].OldID = "other"
	bads[1].MachineID = "other"
	bads[2].CurrentBoot = "old"
	bads[3].ArchiveSHA = "baseline"
	bads[4].NoTask = false
	bads[5].NoVMM = false
	bads[6].Fenced = false
	bads[7].Checked = 600
	bads[8].Expires = 999
	for _, bad := range bads {
		if validateMissingReceipt(bad, old, "archive", "new", 1000) == nil {
			t.Fatal("unbound evidence accepted")
		}
	}
	delete(p.Files, "storage.json")
	if validateMissingReceipt(p, old, "archive", "new", 1000) == nil {
		t.Fatal("missing precrash metadata accepted")
	}
}

func TestMissingFilesRequireLinkedCurrentDiskAndExactMetadata(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("private root-owned evidence test runs in Linux build container")
	}
	dir := t.TempDir()
	old := escrow{Guest: &cube.Sandbox{SandboxID: "owned"}, Fixture: "fixture", WorkerMachineID: "machine", BootID: "old"}
	source := map[string]string{"cubebox": strings.Repeat("a", 64), "storage": strings.Repeat("b", 64)}
	values := map[string]any{
		"cubebox.json":       map[string]any{"ID": "owned", "sandbox_id": "owned"},
		"storage.json":       map[string]any{"sandboxID": "owned"},
		"plan.json":          map[string]any{"purpose": "CUBE_CURRENT_DISK_RESCUE", "sandbox_id": "owned", "source_metadata_sha256": source},
		"rescue-input.json":  map[string]any{"purpose": "CUBE_CURRENT_DISK_RESCUE_INPUT", "sandbox_id": "owned", "source_metadata_sha256": source, "artifacts": []any{map[string]string{"file": "current.ext4", "sha256": "disk-current"}, map[string]string{"file": "lower-000.ext4", "sha256": "lower"}}},
		"fence.json":         map[string]any{"purpose": "CUBE_CURRENT_DISK_CAPTURE", "sandbox_id": "owned", "worker_machine_id": "machine", "previous_boot_id": "old", "current_boot_id": "new", "no_task_verified": true, "management_fenced": true},
		"export-report.json": map[string]any{"purpose": "CUBE_CURRENT_DISK_HOME_EXPORT", "sandbox_id": "owned", "archive_sha256": "archive-current", "captured_disk_sha256": "disk-current"},
	}
	p := missingEvidence{Purpose: "OWNED_CURRENT_DISK_MISSING_PROVIDER", OldID: "owned", Fixture: "fixture", MachineID: "machine", PreviousBoot: "old", CurrentBoot: "new", ArchiveSHA: "archive-current", NoTask: true, NoVMM: true, Fenced: true, Checked: time.Now().Unix(), Expires: time.Now().Unix() + 300, Files: map[string]string{}}
	for name, v := range values {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
		p.Files[name] = hashBytes(b)
	}
	b, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(dir, "recovery-evidence.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateMissingFiles(dir, hashBytes(b), old, "archive-current", "new"); err != nil {
		t.Fatal(err)
	}
	if err := validateMissingFiles(dir, hashBytes(b), old, "archive-baseline", "new"); err == nil {
		t.Fatal("baseline accepted")
	}
	// Preserve the original capture receipt, and explicitly bind a later
	// clean worker boot to the same captured/current disk and metadata.
	p.Purpose = "OWNED_CURRENT_DISK_RETAINED_PROVIDER"
	p.CurrentBoot = "newer"
	reboot := postCaptureReboot{Purpose: "CUBE_CAPTURE_POST_REBOOT_CONTINUITY", ID: old.Guest.SandboxID, Machine: old.WorkerMachineID, DataUUID: old.DataUUID,
		CaptureBoot: "new", ExecutionBoot: "newer", CapturedDisk: "disk-current", CurrentDisk: "disk-current", Manifest: p.Files["rescue-input.json"], Fence: p.Files["fence.json"], Plan: p.Files["plan.json"], Identity: true, Orderly: true, NoTask: true, NoVMM: true, Drained: true}
	rb, _ := json.Marshal(reboot)
	os.WriteFile(filepath.Join(dir, "post-capture-reboot.json"), rb, 0600)
	p.Files["post-capture-reboot.json"] = hashBytes(rb)
	b, _ = json.Marshal(p)
	os.WriteFile(filepath.Join(dir, "recovery-evidence.json"), b, 0600)
	if err := validateSourceFiles(dir, hashBytes(b), old, "archive-current", "newer", "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); err != nil {
		t.Fatal(err)
	}
	if err := validateMissingFiles(dir, hashBytes(b), old, "archive-current", "newer"); err == nil {
		t.Fatal("historical missing branch accepted retained reboot receipt")
	}
	reboot.CurrentDisk = "baseline"
	rb, _ = json.Marshal(reboot)
	os.WriteFile(filepath.Join(dir, "post-capture-reboot.json"), rb, 0600)
	p.Files["post-capture-reboot.json"] = hashBytes(rb)
	b, _ = json.Marshal(p)
	os.WriteFile(filepath.Join(dir, "recovery-evidence.json"), b, 0600)
	if err := validateSourceFiles(dir, hashBytes(b), old, "archive-current", "newer", "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); err == nil {
		t.Fatal("recertified receipt accepted changed current disk")
	}
	reboot.CurrentDisk = "disk-current"
	rb, _ = json.Marshal(reboot)
	os.WriteFile(filepath.Join(dir, "post-capture-reboot.json"), rb, 0600)
	p.Files["post-capture-reboot.json"] = hashBytes(rb)
	b, _ = json.Marshal(p)
	os.WriteFile(filepath.Join(dir, "recovery-evidence.json"), b, 0600)
	if err := validateSourceFiles(dir, hashBytes(b), old, "archive-current", "newer", "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); err != nil {
		t.Fatal(err)
	}

	// A repaired export must retain the receipt and exact diagnostic bytes.
	for _, name := range []string{"repair-fsck.log", "verify-fsck.log"} {
		raw := []byte("private fsck diagnostic for " + name)
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		p.Files[name] = hashBytes(raw)
	}
	repaired := strings.Repeat("c", 64)
	receipt := map[string]any{"purpose": "CUBE_CURRENT_DISK_EXPLICIT_CLONE_REPAIR", "source_sha256": "disk-current", "clone_before_sha256": "disk-current", "clone_after_sha256": repaired, "clean_verified": true, "steps": []map[string]any{{"option": "-fy", "returncode": 1, "log": "repair-fsck.log", "log_sha256": p.Files["repair-fsck.log"]}, {"option": "-fn", "returncode": 0, "log": "verify-fsck.log", "log_sha256": p.Files["verify-fsck.log"]}}}
	raw, _ := json.Marshal(receipt)
	os.WriteFile(filepath.Join(dir, "repair-receipt.json"), raw, 0600)
	p.Files["repair-receipt.json"] = hashBytes(raw)
	exportReport := values["export-report.json"].(map[string]any)
	exportReport["explicit_repair_receipt_sha256"] = p.Files["repair-receipt.json"]
	exportReport["explicit_repair_clone_sha256"] = repaired
	raw, _ = json.Marshal(exportReport)
	os.WriteFile(filepath.Join(dir, "export-report.json"), raw, 0600)
	p.Files["export-report.json"] = hashBytes(raw)
	b, _ = json.Marshal(p)
	os.WriteFile(filepath.Join(dir, "recovery-evidence.json"), b, 0600)
	if err := validateSourceFiles(dir, hashBytes(b), old, "archive-current", "newer", "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "verify-fsck.log")
	original, _ := os.ReadFile(logPath)
	os.WriteFile(logPath, []byte("substituted log"), 0600)
	if err := validateSourceFiles(dir, hashBytes(b), old, "archive-current", "newer", "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); err == nil {
		t.Fatal("changed clean-check log accepted")
	}
	os.WriteFile(logPath, original, 0600)
	wrong := []byte(`{"sandboxID":"other"}`)
	os.WriteFile(filepath.Join(dir, "storage.json"), wrong, 0600)
	if err := validateSourceFiles(dir, hashBytes(b), old, "archive-current", "newer", "OWNED_CURRENT_DISK_RETAINED_PROVIDER"); err == nil {
		t.Fatal("changed escrow accepted")
	}
	p.Files["storage.json"] = hashBytes(wrong)
	b, _ = json.Marshal(p)
	os.WriteFile(filepath.Join(dir, "recovery-evidence.json"), b, 0600)
	if err := validateMissingFiles(dir, hashBytes(b), old, "archive-current", "new"); err == nil {
		t.Fatal("other sandbox storage accepted")
	}
}
