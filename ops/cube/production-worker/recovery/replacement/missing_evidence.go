package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
)

type missingEvidence struct {
	Purpose      string            `json:"purpose"`
	OldID        string            `json:"old_provider_id"`
	Fixture      string            `json:"fixture"`
	MachineID    string            `json:"worker_machine_id"`
	PreviousBoot string            `json:"previous_boot_id"`
	CurrentBoot  string            `json:"current_boot_id"`
	ArchiveSHA   string            `json:"current_archive_sha256"`
	Files        map[string]string `json:"files_sha256"`
	NoTask       bool              `json:"no_task_verified"`
	NoVMM        bool              `json:"no_owned_vmm_or_disk_fd_verified"`
	Fenced       bool              `json:"management_fenced"`
	Checked      int64             `json:"checked_at"`
	Expires      int64             `json:"expires_at"`
}

func emptyMasterInventory(value string) bool {
	count := regexp.MustCompile(`(?m)^\s*SANDBOX_COUNT\s+(\d+)\s*$`).FindAllStringSubmatch(value, -1)
	nodes := regexp.MustCompile(`(?m)^\s*NODES_SCANNED\s+1/1\s*$`).FindAllString(value, -1)
	return len(count) == 1 && count[0][1] == "0" && len(nodes) == 1 && !regexp.MustCompile(`(?m)^\s*[a-f0-9]{32}\s`).MatchString(value)
}
func emptyCubeTasks(value string) bool {
	return strings.Join(strings.Fields(value), " ") == "TASK PID STATUS"
}
func oldStateBranch(err error, inventory, id string) (string, error) {
	if errors.Is(err, cube.ErrRuntimeUnavailable) && exactOldInventory(inventory, id) {
		return "unavailable-retained", nil
	}
	var upstream *cube.APIError
	if errors.As(err, &upstream) && upstream.StatusCode == 404 && emptyMasterInventory(inventory) {
		return "missing-after-worker-loss", nil
	}
	return "", errors.New("old provider/inventory does not establish reviewed native failure")
}
func validateMissingReceipt(proof missingEvidence, old escrow, archive, boot string, now int64) error {
	return validateSourceReceipt(proof, old, archive, boot, now, "OWNED_CURRENT_DISK_MISSING_PROVIDER")
}
func validateSourceReceipt(proof missingEvidence, old escrow, archive, boot string, now int64, purpose string) error {
	if purpose != "OWNED_CURRENT_DISK_MISSING_PROVIDER" && purpose != "OWNED_CURRENT_DISK_RETAINED_PROVIDER" {
		return errors.New("unsupported source evidence purpose")
	}

	if old.Guest == nil || proof.Purpose != purpose || proof.OldID != old.Guest.SandboxID || proof.Fixture != old.Fixture || proof.MachineID != old.WorkerMachineID || proof.PreviousBoot != old.BootID || proof.CurrentBoot != boot || boot == old.BootID || proof.ArchiveSHA != archive || !proof.NoTask || !proof.NoVMM || !proof.Fenced || proof.Checked > now || proof.Checked < now-300 || proof.Expires <= now || proof.Expires > now+1800 {
		return errors.New("missing provider evidence does not establish fenced current-disk provenance")
	}
	names := []string{"cubebox.json", "storage.json", "plan.json", "rescue-input.json", "fence.json", "export-report.json"}
	if len(proof.Files) != len(names) {
		return errors.New("incomplete recovery evidence files")
	}
	for _, name := range names {
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(proof.Files[name]) {
			return errors.New("invalid recovery evidence digest")
		}
	}
	return nil
}
func validateMissingFiles(dir, proofSHA string, old escrow, archive, boot string) error {
	return validateSourceFiles(dir, proofSHA, old, archive, boot, "OWNED_CURRENT_DISK_MISSING_PROVIDER")
}
func validateSourceFiles(dir, proofSHA string, old escrow, archive, boot, purpose string) error {

	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(proofSHA) {
		return errors.New("explicit missing-provider evidence authorization required")
	}
	path := filepath.Join(dir, "recovery-evidence.json")
	f, _ := openArchive(path, 1<<20)
	defer f.Close()
	if hashFile(f) != proofSHA {
		return errors.New("recovery evidence receipt changed")
	}
	var proof missingEvidence
	if err := privateJSON(path, &proof); err != nil {
		return err
	}
	if err := validateSourceReceipt(proof, old, archive, boot, time.Now().Unix(), purpose); err != nil {
		return err
	}
	objects := map[string]json.RawMessage{}
	for name, want := range proof.Files {
		p := filepath.Join(dir, name)
		f, _ := openArchive(p, 8<<20)
		if hashFile(f) != want {
			f.Close()
			return errors.New("recovery evidence input changed")
		}
		f.Close()
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		objects[name] = b
	}
	var box struct {
		ID        string
		SandboxID string `json:"sandbox_id"`
	}
	var storage struct {
		SandboxID string `json:"sandboxID"`
	}
	var plan struct {
		Purpose string            `json:"purpose"`
		ID      string            `json:"sandbox_id"`
		Source  map[string]string `json:"source_metadata_sha256"`
		Disk    struct {
			FilePath   string
			VolumeName string
		} `json:"current_disk"`
	}
	var input struct {
		Purpose   string            `json:"purpose"`
		ID        string            `json:"sandbox_id"`
		Source    map[string]string `json:"source_metadata_sha256"`
		Artifacts []struct {
			File string `json:"file"`
			SHA  string `json:"sha256"`
		} `json:"artifacts"`
	}
	var fence struct {
		Purpose string `json:"purpose"`
		ID      string `json:"sandbox_id"`
		Machine string `json:"worker_machine_id"`
		Before  string `json:"previous_boot_id"`
		After   string `json:"current_boot_id"`
		NoTask  bool   `json:"no_task_verified"`
		Fenced  bool   `json:"management_fenced"`
	}
	var report struct {
		Purpose string `json:"purpose"`
		ID      string `json:"sandbox_id"`
		Archive string `json:"archive_sha256"`
		Disk    string `json:"captured_disk_sha256"`
	}
	for name, out := range map[string]any{"cubebox.json": &box, "storage.json": &storage, "plan.json": &plan, "rescue-input.json": &input, "fence.json": &fence, "export-report.json": &report} {
		if err := json.Unmarshal(objects[name], out); err != nil {
			return err
		}
	}
	id := old.Guest.SandboxID
	if box.ID != id || box.SandboxID != id || storage.SandboxID != id || plan.ID != id || plan.Purpose != "CUBE_CURRENT_DISK_RESCUE" || input.ID != id || input.Purpose != "CUBE_CURRENT_DISK_RESCUE_INPUT" || fence.ID != id || fence.Purpose != "CUBE_CURRENT_DISK_CAPTURE" || fence.Machine != old.WorkerMachineID || fence.Before != old.BootID || fence.After != boot || !fence.NoTask || !fence.Fenced || report.ID != id || report.Purpose != "CUBE_CURRENT_DISK_HOME_EXPORT" || report.Archive != archive || len(input.Artifacts) < 2 || input.Artifacts[0].File != "current.ext4" || input.Artifacts[0].SHA != report.Disk {
		return errors.New("current disk evidence identity chain mismatch")
	}
	for _, k := range []string{"cubebox", "storage"} {
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(plan.Source[k]) || plan.Source[k] != input.Source[k] {
			return errors.New("current capture metadata binding mismatch")
		}
	}
	return nil
}
