// Owned synthetic replacement recovery. Never issues /commit or changes app bindings.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

const pidPath = ".baarcha-postgres/data/postmaster.pid"

func hashBytes(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func hashFile(f *os.File) string {
	_, e := f.Seek(0, 0)
	must(e)
	h := sha256.New()
	_, e = io.Copy(h, f)
	must(e)
	_, e = f.Seek(0, 0)
	must(e)
	return hex.EncodeToString(h.Sum(nil))
}
func openArchive(path string, max int64) (*os.File, int64) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	must(e)
	f := os.NewFile(uintptr(fd), path)
	st, e := f.Stat()
	must(e)
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Uid != 0 || st.Size() <= 0 || st.Size() > max {
		f.Close()
		panic("private bounded archive required")
	}
	return f, st.Size()
}
func zipMember(r *zip.Reader, name string, limit uint64) ([]byte, error) {
	var found *zip.File
	for _, f := range r.File {
		if f.Name == name {
			if found != nil {
				return nil, errors.New("duplicate evidence member")
			}
			found = f
		}
	}
	if found == nil || !found.Mode().IsRegular() || found.UncompressedSize64 > limit {
		return nil, errors.New("missing regular bounded evidence member")
	}
	reader, e := found.Open()
	if e != nil {
		return nil, e
	}
	defer reader.Close()
	b, e := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if uint64(len(b)) > limit {
		return nil, errors.New("evidence member too large")
	}
	return b, e
}
func validateOldPID(b []byte, ack time.Time) error {
	if len(b) > 4096 || bytes.ContainsRune(b, 0) {
		return errors.New("invalid old PostgreSQL PID lock")
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) < 8 || len(lines) > 16 || lines[1] != "/home/sandbox/.baarcha-postgres/data" || strings.TrimSpace(lines[7]) != "ready" {
		return errors.New("old PID lock does not match reviewed PGDATA and ready state")
	}
	pid, e := strconv.Atoi(lines[0])
	if e != nil || pid <= 0 {
		return errors.New("invalid old PID")
	}
	started, e := strconv.ParseInt(lines[2], 10, 64)
	if e != nil || started <= 0 || started > ack.Unix() || !ack.Before(time.Now()) {
		return errors.New("old PID lock start does not predate acknowledgement")
	}
	return nil
}
func deriveHome(source *os.File, size int64, dest string, manifest rt.HomeManifest, ack time.Time) (string, error) {
	if _, e := rt.PrivateHomeDigest(manifest, source, size); e != nil {
		return "", e
	}
	z, e := zip.NewReader(source, size)
	if e != nil {
		return "", e
	}
	pid, e := zipMember(z, pidPath, 4096)
	if e != nil {
		return "", e
	}
	if e = validateOldPID(pid, ack); e != nil {
		return "", e
	}
	out, e := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	defer out.Close()
	w := zip.NewWriter(out)
	for _, f := range z.File {
		if f.Name == pidPath {
			continue
		}
		if f.FileInfo().IsDir() {
			// Python emits valid deflated empty directories. zip.Writer.Copy
			// feeds their compressed bytes to Go's directory writer, which
			// rejects writes. Re-emit the empty directory, preserving its mode.
			header := f.FileHeader
			header.Method = zip.Store
			header.CRC32 = 0
			header.CompressedSize, header.UncompressedSize = 0, 0
			header.CompressedSize64, header.UncompressedSize64 = 0, 0
			if _, e = w.CreateHeader(&header); e != nil {
				w.Close()
				return "", e
			}
			continue
		}
		if e = w.Copy(f); e != nil {
			w.Close()
			return "", e
		}
	}
	if e = w.Close(); e != nil {
		return "", e
	}
	if e = out.Sync(); e != nil {
		return "", e
	}
	st, e := out.Stat()
	if e != nil {
		return "", e
	}
	if _, e = rt.PrivateHomeDigest(manifest, out, st.Size()); e != nil {
		return "", e
	}
	return hashBytes(pid), nil
}
func validateArchiveMarker(z *zip.Reader, path string, want marker) error {
	b, e := zipMember(z, path, 4096)
	if e != nil {
		return e
	}
	var got marker
	if json.Unmarshal(b, &got) != nil || got != want {
		return errors.New("current archive marker does not match latest acknowledgement")
	}
	return nil
}
func exactOldInventory(value, id string) bool {
	count := regexp.MustCompile(`(?m)^\s*SANDBOX_COUNT\s+(\d+)\s*$`).FindAllStringSubmatch(value, -1)
	nodes := regexp.MustCompile(`(?m)^\s*NODES_SCANNED\s+1/1\s*$`).FindAllString(value, -1)
	if len(count) != 1 || count[0][1] != "1" || len(nodes) != 1 {
		return false
	}
	n := 0
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == id {
			n++
		}
	}
	return n == 1
}
func quiesced(c *coordinator) {
	must(c.guest.QuiesceWorkspace(c.ctx))
	for attempt := 0; attempt < 30; attempt++ {
		s, e := c.guest.Status(c.ctx)
		if e == nil && s.ActiveTask == nil && s.Preview.Pid == 0 && len(s.Processes) > 0 {
			inactive := true
			for _, p := range s.Processes {
				if p.Running || p.Pid != 0 {
					inactive = false
				}
			}
			if inactive {
				return
			}
		}
		select {
		case <-c.ctx.Done():
			panic("quiescence deadline")
		case <-time.After(100 * time.Millisecond):
		}
	}
	panic("replacement writers are not confirmed inactive")
}
func runReplacement() (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("owned replacement verification stopped; inspect private journal, no automatic cleanup")
		}
	}()
	if len(os.Args) != 4 || os.Geteuid() != 0 {
		return errors.New("usage: replacement-verifier ORIGINAL_CRASH_STAGE CONVERTED_DIR NEW_REPLACEMENT_STAGE")
	}
	oldDir, input, stage := os.Args[1], os.Args[2], os.Args[3]
	for _, p := range []string{oldDir, input, stage} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			panic("absolute private paths required")
		}
		s, e := os.Lstat(p)
		must(e)
		if !s.IsDir() || s.Mode().Perm() != 0700 || s.Sys().(*syscall.Stat_t).Uid != 0 {
			panic("root0700 private stage required")
		}
	}
	if !strings.HasPrefix(oldDir, "/opt/baarcha-bench/cube-crash-") || !strings.HasPrefix(stage, "/opt/baarcha-bench/cube-replacement-") {
		panic("synthetic fixture stages required")
	}
	if _, e := os.Lstat(filepath.Join(stage, "report.json")); !errors.Is(e, os.ErrNotExist) {
		panic("prior attempt exists; never automatically recreate")
	}
	lock, e := os.OpenFile("/run/lock/cube-operator-acceptance.lock", os.O_RDWR|os.O_CREATE, 0600)
	must(e)
	defer lock.Close()
	must(syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	release := holdWorkerLease(ctx)
	defer release()
	var old escrow
	must(privateJSON(filepath.Join(oldDir, "escrow.private.json"), &old))
	hex32 := regexp.MustCompile(`^[a-f0-9]{32}$`)
	if old.Guest == nil || old.Guest.TemplateID != template || !hex32.MatchString(old.Fixture) || old.Latest.Fixture != old.Fixture || old.Latest.Phase != "latest" || !hex32.MatchString(old.Latest.Nonce) || old.Latest.Nonce == old.Baseline.Nonce {
		panic("invalid original synthetic latest evidence")
	}
	var oldReport struct {
		Checkpoint bool      `json:"checkpoint_ready"`
		Latest     marker    `json:"latest"`
		Ack        time.Time `json:"latest_acknowledged_at"`
		Native     bool      `json:"post_power_loss_latest_app_home_sql_preserved"`
	}
	must(privateJSON(filepath.Join(oldDir, "report.json"), &oldReport))
	if !oldReport.Checkpoint || oldReport.Latest != old.Latest || oldReport.Native || oldReport.Ack.IsZero() {
		panic("native failure/latest acknowledgement evidence required")
	}
	var handoff struct {
		Purpose             string `json:"purpose"`
		Fixture             string `json:"fixture"`
		OldID               string `json:"old_provider_id"`
		WorkerMachineID     string `json:"worker_machine_id"`
		SourceSHA           string `json:"current_archive_sha256"`
		RecoveryEvidenceSHA string `json:"recovery_evidence_sha256"`
		Failed              bool   `json:"native_recovery_failed"`
		Expires             int64  `json:"expires_at"`
	}
	must(privateJSON(filepath.Join(stage, "handoff.json"), &handoff))
	now := time.Now().Unix()
	if handoff.Purpose != "DISPOSABLE_CURRENT_DISK_REPLACEMENT" || handoff.Fixture != old.Fixture || handoff.OldID != old.Guest.SandboxID || handoff.WorkerMachineID != old.WorkerMachineID || !handoff.Failed || handoff.Expires <= now || handoff.Expires > now+1800 {
		panic("current recovery handoff required")
	}
	var converted struct {
		SourceSHA string `json:"source_archive_sha256"`
		AppSHA    string `json:"app_sha256"`
		HomeSHA   string `json:"home_sha256"`
		MarkerSHA string `json:"expected_marker_sha256"`
		Validated bool   `json:"go_import_contract_validated"`
		PID       bool   `json:"postmaster_pid_preserved"`
	}
	must(privateJSON(filepath.Join(input, "conversion.json"), &converted))
	markerJSON := fmt.Sprintf(`{"fixture": "%s", "nonce": "%s", "phase": "latest"}`, old.Fixture, old.Latest.Nonce)
	if converted.SourceSHA != handoff.SourceSHA || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(handoff.SourceSHA) || converted.MarkerSHA != hashBytes([]byte(markerJSON)) || !converted.Validated || !converted.PID {
		panic("conversion not tied to current acknowledged marker/archive")
	}
	app, appSize := openArchive(filepath.Join(input, "app.zip"), rt.MaxPrivateWorkspaceStreamBytes)
	defer app.Close()
	home, homeSize := openArchive(filepath.Join(input, "home.zip"), rt.MaxPrivateHomeBytes)
	defer home.Close()
	if hashFile(app) != converted.AppSHA || hashFile(home) != converted.HomeSHA {
		panic("archive hash mismatch")
	}
	var manifest rt.HomeManifest
	must(privateJSON(filepath.Join(input, "home-manifest.json"), &manifest))
	_, e = rt.PrivateWorkspaceFileDigest(app, appSize)
	must(e)
	_, e = rt.PrivateHomeDigest(manifest, home, homeSize)
	must(e)
	az, e := zip.NewReader(app, appSize)
	must(e)
	hz, e := zip.NewReader(home, homeSize)
	must(e)
	must(validateArchiveMarker(az, "crash-fixture-data/marker.json", old.Latest))
	must(validateArchiveMarker(hz, ".cube-crash-fixture/marker.json", old.Latest))
	capability, e := zipMember(az, "crash-fixture-token", 128)
	must(e)
	if string(capability) != old.Capability {
		panic("old probe capability mismatch")
	}
	probe, e := zipMember(az, "probe.mjs", 1<<20)
	must(e)
	originalProbe, e := os.ReadFile(filepath.Join(oldDir, "probe.mjs"))
	must(e)
	if hashBytes(probe) != hashBytes(originalProbe) {
		panic("probe differs from reviewed read-only verification code")
	}
	hostname, e := worker(ctx, "hostname")
	must(e)
	uuid, e := worker(ctx, "cat /etc/machine-id")
	must(e)
	dataUUID, e := worker(ctx, "findmnt -n -o UUID /data")
	must(e)
	currentBoot := bootID(ctx)
	if hostname != "baarcha-cube-worker-01" || uuid != old.WorkerMachineID || dataUUID != old.DataUUID || currentBoot == old.BootID {
		panic("post-crash worker identity mismatch")
	}
	inventory, e := worker(ctx, "cubemastercli -a 127.0.0.1 list --all --wide")
	must(e)
	env, e := os.ReadFile("/opt/baarcha-cube/worker-01/staging/cube-install.env")
	must(e)
	key := ""
	for _, line := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(line, "CUBE_API_KEY=") {
			key = strings.Trim(strings.TrimPrefix(line, "CUBE_API_KEY="), "\"'")
		}
	}
	api, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: key})
	must(e)
	_, e = api.Get(ctx, old.Guest.SandboxID)
	branch, branchErr := oldStateBranch(e, inventory, old.Guest.SandboxID)
	must(branchErr)
	if branch == "missing-after-worker-loss" {
		must(validateMissingFiles(stage, handoff.RecoveryEvidenceSHA, old, converted.SourceSHA, currentBoot))
		tasks, taskErr := worker(ctx, "timeout 5s ctr --address /data/cubelet/cubelet.sock --namespace default tasks list 2>/dev/null")
		must(taskErr)
		if !emptyCubeTasks(tasks) {
			panic("missing provider still has tasks or incomplete task inventory")
		}
	}
	c := &coordinator{ctx: ctx, stage: stage, cube: api, saved: escrow{Fixture: nonce(16), Supervisor: nonce(32), Capability: old.Capability, Latest: old.Latest, WorkerMachineID: old.WorkerMachineID, DataUUID: old.DataUUID}, report: map[string]any{"production_accepted": false, "binding_changed": false, "native_same_id_recovery_passed": false, "old_provider_state_branch": branch, "recovery_evidence_sha256": handoff.RecoveryEvidenceSHA, "old_provider_id": old.Guest.SandboxID, "original_fixture": old.Fixture, "current_archive_sha256": converted.SourceSHA, "original_home_zip_sha256": converted.HomeSHA, "original_app_zip_sha256": converted.AppSHA, "latest": old.Latest}}
	defer c.detach()
	defer func() {
		if p := recover(); p != nil {
			c.report["failed_after_stage"] = c.report["stage"]
			c.report["owned_replacement_retained_for_review"] = c.saved.Guest != nil
			c.record("replacement-failed")
			panic(p)
		}
	}()
	save(filepath.Join(stage, "intent.private.json"), c.saved, true)
	c.record("create-intent-durable")
	c.saved.Guest, e = api.Create(ctx, cube.CreateRequest{TemplateID: template, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": c.saved.Supervisor}, Metadata: map[string]string{"operator-crash-fixture": c.saved.Fixture, "operator-recovery-of": old.Guest.SandboxID}, TimeoutSeconds: 1800, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}})
	must(e)
	if c.saved.Guest.SandboxID == old.Guest.SandboxID {
		panic("replacement reused source identity")
	}
	save(filepath.Join(stage, "escrow.private.json"), c.saved, true)
	c.report["new_provider_id"] = c.saved.Guest.SandboxID
	c.record("replacement-id-durable")
	c.ownership(ctx)
	c.remote()
	c.attach()
	c.ready()
	quiesced(c)
	c.report["replacement_quiesced_before_import"] = true
	pidHash, e := deriveHome(home, homeSize, filepath.Join(stage, "home-without-stale-pid.zip"), manifest, oldReport.Ack)
	must(e)
	derived, derivedSize := openArchive(filepath.Join(stage, "home-without-stale-pid.zip"), rt.MaxPrivateHomeBytes)
	defer derived.Close()
	c.report["removed_exact_stale_pid_sha256"] = pidHash
	c.report["derived_home_zip_sha256"] = hashFile(derived)
	c.record("old-pid-evidence-preserved-derived-import-prepared")
	must(c.guest.ImportPrivateHome(ctx, manifest, derived, derivedSize))
	before, e := c.guest.Status(ctx)
	must(e)
	must(c.guest.ImportPrivateWorkspaceFile(ctx, app, appSize))
	restarted := false
	for i := 0; i < 60; i++ {
		s, e := c.guest.Status(ctx)
		if e == nil && !s.Runtimed.BootedAt.Equal(before.Runtimed.BootedAt) {
			restarted = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !restarted {
		panic("import did not reexec supervisor")
	}
	c.attach()
	quiesced(c)
	c.record("imported-quiesced-after-reexec")
	must(c.guest.ResumeWorkspace(ctx))
	c.ready()
	probeReady := false
	for i := 0; i < 40; i++ {
		b, e := c.request(3006, "/status", nil)
		if e == nil && bytes.Contains(b, []byte(old.Fixture)) {
			probeReady = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !probeReady {
		panic("retained old probe not ready")
	}
	c.probe("/verify", old.Latest)
	c.report["replacement_latest_app_home_sql_preserved"] = true
	c.report["old_guest_deleted"] = false
	c.report["new_guest_retained_for_review"] = true
	c.record("replacement-recovery-pass-pending-operator-review")
	fmt.Println("PASS: explicit replacement preserved latest app/home/SQL; original retained, no production binding changed")
	return nil
}
func main() {
	if e := runReplacement(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
