package workerstop

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const StartConfigPath = "/etc/baarcha-cube/worker-start.json"

type StartConfig struct {
	Version                int    `json:"version"`
	PauseProof             string `json:"pause_proof"`
	CleanReceipt           string `json:"clean_receipt"`
	ExternalVerifierSHA256 string `json:"external_verifier_sha256,omitempty"`
}
type StopMarker struct {
	Version         int                       `json:"version"`
	Phase           string                    `json:"phase"`
	ReceiptSHA256   string                    `json:"receipt_sha256"`
	InventorySHA256 string                    `json:"inventory_sha256"`
	Bindings        []store.WorkerStopBinding `json:"bindings"`
	QEMUPID         int                       `json:"qemu_pid"`
	QEMUStartTime   string                    `json:"qemu_start_time"`
	WorkerBootID    string                    `json:"worker_boot_id"`
	WorkerMachineID string                    `json:"worker_machine_id"`
	DataUUID        string                    `json:"data_uuid"`
}
type CleanReceipt struct {
	Version     int                    `json:"version"`
	State       string                 `json:"state"`
	GeneratedAt float64                `json:"generated_at"`
	Proof       Proof                  `json:"proof"`
	External    *ExternalCleanEvidence `json:"external,omitempty"`
}

const externalVerifierPath = "/usr/local/libexec/baarcha-cube-external-clean.py"
const externalCleanMethod = "qmp-guest-shutdown+supervisor-wait4"

type ExternalCleanEvidence struct {
	Version             int    `json:"version"`
	Method              string `json:"method"`
	OuterBootID         string `json:"outer_boot_id"`
	SupervisorPID       int    `json:"supervisor_pid"`
	SupervisorStartTime string `json:"supervisor_start_time"`
	QEMUPID             int    `json:"qemu_pid"`
	QEMUStartTime       string `json:"qemu_start_time"`
	WorkerBootID        string `json:"worker_boot_id"`
	EvidenceDirectory   string `json:"evidence_directory"`
	EvidenceSHA256      string `json:"evidence_sha256"`
}

func validCleanReceiptKind(r CleanReceipt) bool {
	if r.State == "stopped-clean" {
		return r.External == nil
	}
	e := r.External
	return r.State == "externally-stopped-clean" && e != nil && e.Version == 1 && e.Method == externalCleanMethod &&
		e.SupervisorPID > 1 && e.SupervisorPID != e.QEMUPID && e.SupervisorStartTime != "" && idPattern.MatchString(e.OuterBootID) &&
		e.QEMUPID == r.Proof.QEMUPID && e.QEMUStartTime == r.Proof.QEMUStartTime && e.WorkerBootID == r.Proof.WorkerBootID &&
		filepath.IsAbs(e.EvidenceDirectory) && shaPattern.MatchString(e.EvidenceSHA256)
}

// Only the separately pinned fixed verifier understands the raw wait4/QMP
// closure. State names/booleans alone never approve an external recovery receipt.
func verifyExternalClean(ctx context.Context, sc StartConfig, r CleanReceipt) error {
	return verifyExternalCleanWith(ctx, sc, r, externalVerifierPath, fixedCommand)
}

func verifyExternalCleanWith(ctx context.Context, sc StartConfig, r CleanReceipt, verifierPath string, command func(context.Context, ...string) ([]byte, error)) error {
	if r.State != "externally-stopped-clean" {
		return nil
	}
	if !validCleanReceiptKind(r) || !shaPattern.MatchString(sc.ExternalVerifierSHA256) || filepath.Join(r.External.EvidenceDirectory, "receipt.json") != sc.CleanReceipt {
		return errors.New("explicit external clean evidence configuration required")
	}
	path, e := filepath.EvalSymlinks(verifierPath)
	if e != nil || path != verifierPath {
		return errors.New("fixed external verifier unavailable")
	}
	file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 131072 {
		return errors.New("untrusted external verifier")
	}
	raw, e := io.ReadAll(io.LimitReader(file, 131073))
	if e != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != sc.ExternalVerifierSHA256 {
		return errors.New("external verifier hash differs")
	}
	before, e := os.ReadFile(sc.CleanReceipt)
	if e != nil {
		return e
	}
	var same CleanReceipt
	if json.Unmarshal(before, &same) != nil || !reflect.DeepEqual(same, r) {
		return errors.New("external receipt changed")
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, e := command(bounded, "/usr/bin/python3", verifierPath, "--verify", sc.CleanReceipt)
	if e != nil {
		return errors.New("external clean evidence verification refused")
	}
	var out struct {
		Version       int    `json:"version"`
		Verified      bool   `json:"verified"`
		ReceiptSHA256 string `json:"receipt_sha256"`
		Method        string `json:"method"`
		QEMUPID       int    `json:"qemu_pid"`
		QEMUStartTime string `json:"qemu_start_time"`
		WorkerBootID  string `json:"worker_boot_id"`
	}
	if json.Unmarshal(result, &out) != nil || out.Version != 1 || !out.Verified || out.ReceiptSHA256 != fmt.Sprintf("%x", sha256.Sum256(before)) || out.Method != externalCleanMethod || out.QEMUPID != r.Proof.QEMUPID || out.QEMUStartTime != r.Proof.QEMUStartTime || out.WorkerBootID != r.Proof.WorkerBootID {
		return errors.New("external clean verifier receipt mismatch")
	}
	return nil
}

type StartEvidence struct {
	Version          int       `json:"version"`
	TenantReady      bool      `json:"tenant_ready"`
	RoutingChanged   bool      `json:"routing_changed"`
	GuestsWoken      int       `json:"guests_woken"`
	GeneratedAt      time.Time `json:"generated_at"`
	WorkerBootID     string    `json:"worker_boot_id"`
	QEMUPID          int       `json:"qemu_pid"`
	QEMUStartTime    string    `json:"qemu_start_time"`
	InventorySHA256  string    `json:"inventory_sha256"`
	StopMarkerSHA256 string    `json:"stop_marker_sha256"`
}

func readDB(c Config) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: c.Database}
	u.RawQuery = "mode=ro&_query_only=1&_busy_timeout=5000"
	return sql.Open("sqlite3", u.String())
}
func ValidateObservation(o store.WorkerObservation, actual []cube.Sandbox, cfg cube.AdmissionConfig, paused bool) error {
	if cfg.MaxActive != 4 || cfg.CPUCount != 2 || cfg.MemoryMB != 2048 || o.MaxActive != 4 || o.Profile != "cpu=2;memory_mb=2048" || o.PendingRecovery != 0 {
		return errors.New("reviewed four-slot policy or complete recovery required")
	}
	if e := exactInventory(store.WorkerStopSnapshot{Bindings: o.Bindings}, actual, paused); e != nil {
		return e
	}
	if len(o.Admissions) != len(o.Bindings) {
		return errors.New("exact existing admission set required")
	}
	admissions := map[string]cube.AdmissionRecord{}
	for _, a := range o.Admissions {
		if _, ok := admissions[a.RuntimeID]; ok {
			return errors.New("duplicate admission")
		}
		admissions[a.RuntimeID] = a
	}
	remotes := map[string]cube.Sandbox{}
	for _, a := range actual {
		remotes[a.SandboxID] = a
	}
	charged := 0
	for _, b := range o.Bindings {
		a, ok := admissions[b.RuntimeID]
		r := remotes[b.RuntimeID]
		profile, approved := cfg.Templates[b.TemplateID]
		if !ok || a.Key != "app:"+b.AppID || a.TemplateID != b.TemplateID || a.Token == "" || !approved || profile.CPUCount != 2 || profile.MemoryMB != 2048 || r.CPUCount != 2 || r.MemoryMB != 2048 {
			return errors.New("binding, admission or template resource mismatch")
		}
		if r.State == "paused" {
			if a.State != "released" || a.Charged != 0 {
				return errors.New("paused guest reservation is not released")
			}
		} else if a.State != "active" || a.Charged != 1 {
			return errors.New("running guest reservation is not active")
		}
		charged += a.Charged
	}
	if charged > 4 {
		return errors.New("active allocations exceed tested limit")
	}
	return nil
}
func validateStartProof(c Config, m StopMarker, p Proof, r CleanReceipt, now time.Time) error {
	if m.Version != 1 || m.Phase != "preparing" || !p.Verified || p.Version != 1 || r.Version != 1 || !validCleanReceiptKind(r) || hash(r.Proof) != hash(p) || p.ProviderJobs != 0 {
		return errors.New("retained exact clean-stop proof required")
	}
	if m.WorkerMachineID != c.WorkerMachineID || m.DataUUID != c.DataUUID || p.WorkerMachineID != c.WorkerMachineID || p.DataUUID != c.DataUUID || m.WorkerBootID == c.WorkerBootID || p.WorkerBootID != m.WorkerBootID || p.QEMUPID != m.QEMUPID || p.QEMUStartTime != m.QEMUStartTime || (m.QEMUPID == c.QEMUPID && m.QEMUStartTime == c.QEMUStartTime) {
		return errors.New("same worker/data and a new boot/process generation required")
	}
	if p.InventorySHA256 != m.InventorySHA256 || hash(m.Bindings) != m.InventorySHA256 || p.ReceiptSHA256 != m.ReceiptSHA256 || !shaPattern.MatchString(m.ReceiptSHA256) || len(p.GuestStates) != len(m.Bindings) {
		return errors.New("stop proof differs from durable marker")
	}
	if p.GeneratedAt.After(now) || p.GeneratedAt.IsZero() || r.GeneratedAt < float64(p.GeneratedAt.Unix()) || r.GeneratedAt > float64(now.Unix()+1) {
		return errors.New("invalid clean-stop chronology")
	}
	for _, b := range m.Bindings {
		if p.GuestStates[b.RuntimeID] != "paused" {
			return errors.New("stop receipt lacks exact paused guest")
		}
	}
	return nil
}
func workerReady(ctx context.Context, c Config) error { return workerCheck(ctx, c, "verify-start") }
func workerCheck(ctx context.Context, c Config, action string) error {
	bounded, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := "/usr/bin/python3 " + helper + " " + action + " --machine-id " + c.WorkerMachineID + " --boot-id " + c.WorkerBootID + " --data-uuid " + c.DataUUID
	raw, e := fixedCommand(bounded, "/usr/bin/ssh", "-i", root+"operator-key", "-p", "20222", "-oBatchMode=yes", "-oConnectTimeout=5", "-oStrictHostKeyChecking=yes", "-oUserKnownHostsFile="+root+"known_hosts", "root@127.0.0.1", command)
	if e != nil {
		return e
	}
	var v struct {
		Verified bool   `json:"verified"`
		BootID   string `json:"boot_id"`
	}
	if json.Unmarshal(raw, &v) != nil || !v.Verified || v.BootID != c.WorkerBootID {
		return errors.New("worker startup identity/readiness refused")
	}
	return nil
}

// startupStorage verifies readiness without allocating, refunding or resetting any
// durable admission epoch. Safe shutdown deliberately does not require this check.
func startupStorage(c Config) error {
	if err := c.Admission.RequireStorageGuard(); err != nil {
		return err
	}
	g := c.Admission.StorageGuard
	if g.ExpectedBootID != c.WorkerBootID || g.WorkerMachineID != c.WorkerMachineID || g.InnerFSUUID != c.DataUUID {
		return cube.ErrStorageUnavailable
	}
	now, err := cube.ReadStorageClock()
	if err != nil {
		return err
	}
	o, err := cube.ReadStorageObservation(*g, now)
	if err != nil {
		return err
	}
	if o.InnerFreeBytes < cube.StorageBaseline || o.OuterFreeBytes < cube.StorageBaseline {
		return cube.ErrStorageUnavailable
	}
	return nil
}

func ReconcileStart(ctx context.Context, c Config, sc StartConfig) (*StartEvidence, error) {
	if err := startupStorage(c); err != nil {
		return nil, err
	}
	if sc.Version != 1 {
		return nil, errors.New("startup configuration version required")
	}
	if e := verifyQEMU(c); e != nil {
		return nil, e
	}
	if e := verifyController(ctx, c); e != nil {
		return nil, e
	}
	lock, e := maintenance.Acquire(c.Database, true)
	if e != nil {
		return nil, e
	}
	defer lock.Close()
	if e = maintenance.CheckDatabaseUsers(c.Database); e != nil {
		return nil, e
	}
	markerPath, e := maintenance.WorkerStopMarker(c.Database)
	if e != nil {
		return nil, e
	}
	var m StopMarker
	var p Proof
	var receipt CleanReceipt
	for _, entry := range []struct {
		path string
		out  any
	}{{markerPath, &m}, {sc.PauseProof, &p}, {sc.CleanReceipt, &receipt}} {
		if e = privateJSON(entry.path, entry.out); e != nil {
			return nil, e
		}
	}
	if e = verifyExternalClean(ctx, sc, receipt); e != nil {
		return nil, e
	}
	if e = validateStartProof(c, m, p, receipt, time.Now()); e != nil {
		return nil, e
	}
	db, e := readDB(c)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	client, e := cube.New(cube.Config{APIURL: c.APIURL, APIKey: c.APIKey})
	if e != nil {
		return nil, e
	}
	return reconcileStart(ctx, c, m, db, client, func() error { return workerReady(ctx, c) }, func() error {
		if e := verifyQEMU(c); e != nil {
			return e
		}
		return verifyController(ctx, c)
	}, maintenance.CheckDatabaseUsers)
}
func reconcileStart(ctx context.Context, c Config, m StopMarker, db *sql.DB, provider Provider, ready, controller func() error, databaseUsers func(string) error) (*StartEvidence, error) {
	if databaseUsers == nil {
		return nil, errors.New("database writer checkpoint required")
	}
	for round := 0; round < 2; round++ {
		if e := controller(); e != nil {
			return nil, e
		}
		if e := databaseUsers(c.Database); e != nil {
			return nil, e
		}
		snapshot, e := store.WorkerStopInventoryDB(ctx, db)
		if e != nil {
			return nil, e
		}
		if snapshot.SHA256 != m.InventorySHA256 {
			return nil, errors.New("bindings/config changed since stop")
		}
		observation, e := store.WorkerObservationDB(ctx, db)
		if e != nil {
			return nil, e
		}
		actual, e := provider.Inventory(ctx)
		if e != nil {
			return nil, e
		}
		if e = ValidateObservation(observation, actual, c.Admission, true); e != nil {
			return nil, e
		}
		for _, b := range snapshot.Bindings {
			r, e := provider.Get(ctx, b.RuntimeID)
			if e != nil {
				return nil, e
			}
			if r == nil {
				return nil, errors.New("provider detail missing")
			}
			if e = ValidateObservation(store.WorkerObservation{Bindings: []store.WorkerStopBinding{b}, Admissions: matchingAdmission(observation, b.RuntimeID), MaxActive: observation.MaxActive, Profile: observation.Profile}, []cube.Sandbox{*r}, c.Admission, true); e != nil {
				return nil, e
			}
		}
		if e = ready(); e != nil {
			return nil, e
		}
	}
	// Final local checks follow the potentially slow remote readiness read.
	if e := controller(); e != nil {
		return nil, e
	}
	if e := databaseUsers(c.Database); e != nil {
		return nil, e
	}
	final, e := store.WorkerStopInventoryDB(ctx, db)
	if e != nil || final.SHA256 != m.InventorySHA256 {
		return nil, errors.New("controller changed before marker clearance")
	}
	observed, e := store.WorkerObservationDB(ctx, db)
	if e != nil {
		return nil, e
	}
	actual, e := provider.Inventory(ctx)
	if e != nil {
		return nil, e
	}
	if e = ValidateObservation(observed, actual, c.Admission, true); e != nil {
		return nil, e
	}
	if e := startupStorage(c); e != nil {
		return nil, e
	}
	out := &StartEvidence{Version: 1, TenantReady: true, GeneratedAt: time.Now().UTC(), WorkerBootID: c.WorkerBootID, QEMUPID: c.QEMUPID, QEMUStartTime: c.QEMUStartTime, InventorySHA256: m.InventorySHA256, StopMarkerSHA256: hash(m)}
	// Persist evidence first. A crash before exact marker removal remains fenced.
	if e := publishJSON(filepath.Join(c.EvidenceDirectory, fmt.Sprintf("startup-%s.json", hash(out))), out); e != nil {
		return nil, e
	}
	if e := publishCurrent(filepath.Join(c.EvidenceDirectory, "startup-current.json"), out); e != nil {
		return nil, e
	}
	if e := maintenance.ClearWorkerStop(c.Database, m); e != nil {
		return nil, e
	}
	return out, nil
}
func matchingAdmission(o store.WorkerObservation, id string) []cube.AdmissionRecord {
	for _, a := range o.Admissions {
		if a.RuntimeID == id {
			return []cube.AdmissionRecord{a}
		}
	}
	return nil
}
func LoadStartConfig() (StartConfig, error) {
	var c StartConfig
	e := privateJSON(StartConfigPath, &c)
	return c, e
}
func publishJSON(path string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return errors.Join(e, closeErr)
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

// Observe is read-only: plain authenticated GETs and SQLite mode=ro. It never
// invokes the guarded client's observation mutation or performs reconciliation.
func Observe(ctx context.Context, c Config) (map[string]any, error) {
	if err := startupStorage(c); err != nil {
		return nil, err
	}
	if e := verifyQEMU(c); e != nil {
		return nil, e
	}
	if e := maintenance.CheckWorkerStop(c.Database); e != nil {
		return nil, e
	}
	var evidence StartEvidence
	if e := privateJSON(filepath.Join(c.EvidenceDirectory, "startup-current.json"), &evidence); e != nil {
		return nil, e
	}
	if !evidence.TenantReady || evidence.WorkerBootID != c.WorkerBootID || evidence.QEMUPID != c.QEMUPID || evidence.QEMUStartTime != c.QEMUStartTime {
		return nil, errors.New("current startup evidence missing")
	}
	db, e := readDB(c)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	before, e := store.WorkerObservationDB(ctx, db)
	if e != nil {
		return nil, e
	}
	client, e := cube.New(cube.Config{APIURL: c.APIURL, APIKey: c.APIKey})
	if e != nil {
		return nil, e
	}
	if e = workerCheck(ctx, c, "observe-worker"); e != nil {
		return nil, e
	}
	actual, e := client.Inventory(ctx)
	if e != nil {
		return nil, e
	}
	if e = ValidateObservation(before, actual, c.Admission, false); e != nil {
		return nil, e
	}
	after, e := store.WorkerObservationDB(ctx, db)
	if e != nil || hash(before) != hash(after) {
		return nil, errors.New("controller changed during observation; retry next scheduled check")
	}
	if e = maintenance.CheckWorkerStop(c.Database); e != nil {
		return nil, e
	}
	if e := startupStorage(c); e != nil {
		return nil, e
	}
	active := 0
	for _, r := range actual {
		if r.State == "running" {
			active++
		}
	}
	return map[string]any{"version": 1, "consistent": true, "observation_only": true, "bindings": len(actual), "active": active, "max_active": 4, "worker_boot_id": c.WorkerBootID}, nil
}
func publishCurrent(path string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".startup-ready-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return errors.Join(e, ce)
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
