// Package workerstop is a fixed offline operator coordinator. It never changes
// routing, stops the controller, quiesces workspaces, deletes guests or kills QEMU.
package workerstop

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const ConfigPath = "/etc/baarcha-cube/worker-stop.json"
const helper = "/usr/local/libexec/baarcha-cube-worker-lifecycle.py"
const root = "/opt/baarcha-cube/worker-01/"

var idPattern = regexp.MustCompile(`^[a-f0-9-]{32,64}$`)
var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Config struct {
	Version           int                  `json:"version"`
	Database          string               `json:"database"`
	Migrations        string               `json:"migrations"`
	Receipt           string               `json:"receipt"`
	EvidenceDirectory string               `json:"evidence_directory"`
	ControllerID      string               `json:"controller_id"`
	APIURL            string               `json:"api_url"`
	APIKey            string               `json:"api_key"`
	Admission         cube.AdmissionConfig `json:"admission"`
	WorkerMachineID   string               `json:"worker_machine_id"`
	WorkerBootID      string               `json:"worker_boot_id"`
	DataUUID          string               `json:"data_uuid"`
	QEMUPID           int                  `json:"qemu_pid"`
	QEMUStartTime     string               `json:"qemu_start_time"`
}
type DrainReceipt struct {
	Version                  int       `json:"version"`
	GeneratedAt              time.Time `json:"generated_at"`
	ControllerID             string    `json:"controller_id"`
	WorkerBootID             string    `json:"worker_boot_id"`
	QEMUPID                  int       `json:"qemu_pid"`
	QEMUStartTime            string    `json:"qemu_start_time"`
	InventorySHA256          string    `json:"inventory_sha256"`
	CaddyConfigurationSHA256 string    `json:"caddy_configuration_sha256"`
	EvidenceSHA256           string    `json:"evidence_sha256"`
	TrafficFenced            bool      `json:"traffic_fenced"`
	ExistingRequestsDrained  bool      `json:"existing_requests_drained"`
	DirectWritersFenced      bool      `json:"direct_writers_fenced"`
	ProviderJobsDrained      bool      `json:"provider_jobs_drained"`
}
type Proof struct {
	WorkerMachineID string            `json:"worker_machine_id"`
	DataUUID        string            `json:"data_uuid"`
	Version         int               `json:"version"`
	Verified        bool              `json:"verified"`
	QEMUPID         int               `json:"qemu_pid"`
	QEMUStartTime   string            `json:"qemu_start_time"`
	WorkerBootID    string            `json:"worker_boot_id"`
	GeneratedAt     time.Time         `json:"generated_at"`
	InventorySHA256 string            `json:"inventory_sha256"`
	ReceiptSHA256   string            `json:"receipt_sha256"`
	ProviderJobs    int               `json:"provider_jobs"`
	GuestStates     map[string]string `json:"guest_states"`
}
type Provider interface {
	Inventory(context.Context) ([]cube.Sandbox, error)
	Get(context.Context, string) (*cube.Sandbox, error)
	Pause(context.Context, string) error
}
type Held struct {
	lock         *os.File
	db           *store.Store
	config       Config
	receipt      DrainReceipt
	proof        *Proof
	marker       bool
	evidencePath string
}

func privateJSON(path string, out any) error {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	real, e := filepath.EvalSymlinks(absolute)
	if e != nil || real != absolute {
		return errors.New("canonical private configuration required")
	}
	file, e := os.OpenFile(real, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return e
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 131072 {
		return errors.New("root0600 bounded configuration required")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 131073))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(out); e != nil {
		return errors.New("invalid private configuration")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("extra private configuration data")
	}
	return nil
}
func LoadConfig(path string) (Config, error) {
	var c Config
	if os.Geteuid() != 0 {
		return c, errors.New("native host root required")
	}
	if e := privateJSON(path, &c); e != nil {
		return c, e
	}
	if c.Version != 1 || !shaPattern.MatchString(c.ControllerID) || !idPattern.MatchString(c.WorkerMachineID) || !idPattern.MatchString(c.WorkerBootID) || !idPattern.MatchString(c.DataUUID) || c.QEMUPID < 2 {
		return c, errors.New("reviewed fixed worker/controller identity required")
	}
	if n, e := strconv.ParseUint(c.QEMUStartTime, 10, 64); e != nil || n == 0 {
		return c, errors.New("invalid QEMU process generation")
	}
	if c.APIURL != "http://127.0.0.1:20300" {
		return c, errors.New("fixed private worker API origin required")
	}
	for _, path := range []string{c.Database, c.Migrations, c.EvidenceDirectory} {
		real, e := filepath.EvalSymlinks(path)
		if e != nil || real != path || !filepath.IsAbs(path) {
			return c, errors.New("canonical operator paths required")
		}
	}
	if !filepath.IsAbs(c.Receipt) || filepath.Clean(c.Receipt) != c.Receipt {
		return c, errors.New("canonical receipt path required")
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(c.Receipt))
	if e != nil || parent != filepath.Dir(c.Receipt) {
		return c, errors.New("canonical receipt parent required")
	}
	info, e := os.Stat(c.EvidenceDirectory)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		return c, errors.New("private evidence directory required")
	}
	return c, nil
}
func hash(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func validateReceipt(c Config, r DrainReceipt, now time.Time) error {
	age := now.Sub(r.GeneratedAt)
	if r.Version != 1 || age < 0 || age > 2*time.Minute || r.ControllerID != c.ControllerID || r.WorkerBootID != c.WorkerBootID || r.QEMUPID != c.QEMUPID || r.QEMUStartTime != c.QEMUStartTime || !shaPattern.MatchString(r.InventorySHA256) || !shaPattern.MatchString(r.EvidenceSHA256) || !shaPattern.MatchString(r.CaddyConfigurationSHA256) || !r.TrafficFenced || !r.ExistingRequestsDrained || !r.DirectWritersFenced || !r.ProviderJobsDrained {
		return errors.New("fresh independently verified pre-drain receipt required")
	}
	return nil
}
func fixedCommand(ctx context.Context, args ...string) ([]byte, error) {
	out, e := exec.CommandContext(ctx, args[0], args[1:]...).Output()
	if e != nil {
		return nil, errors.New("fixed operator identity/readiness command failed")
	}
	if len(out) > 1<<20 {
		return nil, errors.New("operator response exceeds bound")
	}
	return out, nil
}
func verifyController(ctx context.Context, c Config) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, e := fixedCommand(bounded, "/usr/bin/docker", "inspect", "--type", "container", "src-sandboxd-1", "--format", `{"Id":{{json .Id}},"State":{{json .State}},"HostConfig":{"RestartPolicy":{{json .HostConfig.RestartPolicy}}}}`)
	if e != nil {
		return e
	}
	var v struct {
		ID    string `json:"Id"`
		State struct {
			Running, Restarting, Paused bool
			Status                      string
		}
		HostConfig struct{ RestartPolicy struct{ Name string } }
	}
	if json.Unmarshal(raw, &v) != nil || v.ID != c.ControllerID || v.State.Running || v.State.Restarting || v.State.Paused || (v.State.Status != "exited" && v.State.Status != "created") || v.HostConfig.RestartPolicy.Name != "no" {
		return errors.New("exact controller must be inactive with restart disabled")
	}
	return nil
}
func workerSync(ctx context.Context, c Config) error {
	bounded, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Every interpolated value is restricted to numeric/hex identifiers by config
	// validation; there are no user-controlled shell programs, URLs or paths.
	command := "/usr/bin/python3 " + helper + " sync-data --machine-id " + c.WorkerMachineID + " --boot-id " + c.WorkerBootID + " --data-uuid " + c.DataUUID
	raw, e := fixedCommand(bounded, "/usr/bin/ssh", "-i", root+"operator-key", "-p", "20222", "-oBatchMode=yes", "-oConnectTimeout=5", "-oStrictHostKeyChecking=yes", "-oUserKnownHostsFile="+root+"known_hosts", "root@127.0.0.1", command)
	if e != nil {
		return e
	}
	var v struct {
		Synced bool   `json:"synced"`
		BootID string `json:"boot_id"`
	}
	if json.Unmarshal(raw, &v) != nil || !v.Synced || v.BootID != c.WorkerBootID {
		return errors.New("worker identity/syncfs proof failed")
	}
	return nil
}

// Open returns a Held even on errors after exclusive-lock acquisition. The
// caller must keep that lock until the exact QEMU process is independently gone.
func Open(ctx context.Context, c Config) (*Held, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("native root required")
	}
	if e := verifyQEMU(c); e != nil {
		return nil, e
	}
	var receipt DrainReceipt
	if e := privateJSON(c.Receipt, &receipt); e != nil {
		return nil, e
	}
	if e := validateReceipt(c, receipt, time.Now()); e != nil {
		return nil, e
	}
	if e := verifyController(ctx, c); e != nil {
		return nil, e
	}
	lock, e := maintenance.Acquire(c.Database, true)
	if e != nil {
		return nil, e
	}
	held := &Held{lock: lock, config: c, receipt: receipt}
	if e = maintenance.CheckDatabaseUsers(c.Database); e != nil {
		return held, e
	}
	if e = maintenance.CheckWorkerStop(c.Database); e != nil {
		return held, e
	}
	held.db, e = store.Open(ctx, "file:"+c.Database+"?_journal=WAL&_busy_timeout=5000&_fk=1", c.Migrations)
	if e != nil {
		return held, e
	}
	frozen, e := held.snapshot(ctx)
	if e != nil {
		return held, e
	}
	if frozen.SHA256 != receipt.InventorySHA256 {
		return held, errors.New("bindings changed after pre-drain inventory")
	}
	// A complete exact binding/config snapshot, not just its hash, survives every
	// failure from here onward. No provider mutation precedes durable publication.
	if e = maintenance.WriteWorkerStop(c.Database, map[string]any{"version": 1, "phase": "preparing", "receipt_sha256": hash(receipt), "inventory_sha256": frozen.SHA256, "bindings": frozen.Bindings, "qemu_pid": c.QEMUPID, "qemu_start_time": c.QEMUStartTime, "worker_boot_id": c.WorkerBootID, "worker_machine_id": c.WorkerMachineID, "data_uuid": c.DataUUID}); e != nil {
		return held, e
	}
	held.marker = true
	return held, nil
}
func exactInventory(expected store.WorkerStopSnapshot, actual []cube.Sandbox, paused bool) error {
	if len(expected.Bindings) != len(actual) {
		return errors.New("provider inventory differs from owned bindings")
	}
	index := map[string]store.WorkerStopBinding{}
	for _, b := range expected.Bindings {
		if _, ok := index[b.RuntimeID]; ok {
			return errors.New("duplicate owned provider identity")
		}
		index[b.RuntimeID] = b
	}
	seen := map[string]bool{}
	for _, v := range actual {
		b, ok := index[v.SandboxID]
		if !ok || seen[v.SandboxID] || v.TemplateID != b.TemplateID || v.Metadata["sandboxd_id"] != b.SandboxID || v.Metadata["sandboxd_app_id"] != b.AppID || (v.Domain != "" && v.Domain != b.Domain) || (v.State != "paused" && v.State != "running") || (paused && v.State != "paused") {
			return errors.New("unknown, incomplete or nonpaused provider identity")
		}
		seen[v.SandboxID] = true
	}
	return nil
}
func (h *Held) snapshot(ctx context.Context) (store.WorkerStopSnapshot, error) {
	if h.db == nil {
		return store.WorkerStopSnapshot{}, errors.New("controller database unavailable")
	}
	if pending, e := h.db.HasIncompleteRuntimeMigrations(ctx); e != nil || pending {
		return store.WorkerStopSnapshot{}, errors.New("unfinished Docker migration prevents stop")
	}
	return h.db.WorkerStopInventory(ctx)
}
func (h *Held) Prepare(ctx context.Context) (*Proof, error) {
	if h.db == nil {
		return nil, errors.New("controller database unavailable")
	}
	if e := validateReceipt(h.config, h.receipt, time.Now()); e != nil {
		return nil, e
	}
	client, e := cube.New(cube.Config{APIURL: h.config.APIURL, APIKey: h.config.APIKey})
	if e != nil {
		return nil, e
	}
	if e = client.ConfigureAdmission(ctx, h.db, h.config.Admission); e != nil {
		return nil, e
	}
	return h.prepare(ctx, client, verifyController, workerSync)
}
func (h *Held) prepare(ctx context.Context, provider Provider, controller func(context.Context, Config) error, syncData func(context.Context, Config) error) (*Proof, error) {
	bounded, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	ctx = bounded
	if !h.marker || h.lock == nil {
		return nil, errors.New("durable startup/maintenance fence required")
	}
	if e := controller(ctx, h.config); e != nil {
		return nil, e
	}
	before, e := h.snapshot(ctx)
	if e != nil {
		return nil, e
	}
	if before.SHA256 != h.receipt.InventorySHA256 {
		return nil, errors.New("owned bindings changed after pre-drain review")
	}
	inventory, e := provider.Inventory(ctx)
	if e != nil {
		return nil, e
	}
	if e = exactInventory(before, inventory, false); e != nil {
		return nil, e
	}
	for _, b := range before.Bindings {
		if e = controller(ctx, h.config); e != nil {
			return nil, e
		}
		if e = maintenance.CheckDatabaseUsers(h.config.Database); e != nil {
			return nil, e
		}
		current, e := h.snapshot(ctx)
		if e != nil || current.SHA256 != before.SHA256 {
			return nil, errors.New("controller state changed during pause")
		}
		actual, e := provider.Get(ctx, b.RuntimeID)
		if e != nil {
			return nil, e
		}
		if e = exactInventory(store.WorkerStopSnapshot{Bindings: []store.WorkerStopBinding{b}}, []cube.Sandbox{*actual}, false); e != nil {
			return nil, e
		}
		if actual.State == "running" {
			if e = provider.Pause(ctx, b.RuntimeID); e != nil {
				return nil, e
			}
		}
		actual, e = provider.Get(ctx, b.RuntimeID)
		if e != nil {
			return nil, e
		}
		if e = exactInventory(store.WorkerStopSnapshot{Bindings: []store.WorkerStopBinding{b}}, []cube.Sandbox{*actual}, true); e != nil {
			return nil, e
		}
	}
	inventory, e = provider.Inventory(ctx)
	if e != nil {
		return nil, e
	}
	if e = exactInventory(before, inventory, true); e != nil {
		return nil, e
	}
	current, e := h.snapshot(ctx)
	if e != nil || current.SHA256 != before.SHA256 {
		return nil, errors.New("controller changed before worker sync")
	}
	if e = controller(ctx, h.config); e != nil {
		return nil, e
	}
	if e = syncData(ctx, h.config); e != nil {
		return nil, e
	}
	// A fresh full inventory after sync must still agree; not merely an HTTP200.
	inventory, e = provider.Inventory(ctx)
	if e != nil {
		return nil, e
	}
	if e = exactInventory(before, inventory, true); e != nil {
		return nil, e
	}
	if e = h.db.WorkerStopAllReleased(ctx); e != nil {
		return nil, e
	}
	if e = maintenance.CheckDatabaseUsers(h.config.Database); e != nil {
		return nil, e
	}
	if e = controller(ctx, h.config); e != nil {
		return nil, e
	}
	current, e = h.snapshot(ctx)
	if e != nil || current.SHA256 != before.SHA256 {
		return nil, errors.New("controller changed before powerdown proof")
	}
	proof := &Proof{WorkerMachineID: h.config.WorkerMachineID, DataUUID: h.config.DataUUID, Version: 1, Verified: true, QEMUPID: h.config.QEMUPID, QEMUStartTime: h.config.QEMUStartTime, WorkerBootID: h.config.WorkerBootID, GeneratedAt: time.Now().UTC(), InventorySHA256: before.SHA256, ReceiptSHA256: hash(h.receipt), GuestStates: map[string]string{}}
	for _, b := range before.Bindings {
		proof.GuestStates[b.RuntimeID] = "paused"
	}
	raw, _ := json.Marshal(proof)
	sum := sha256.Sum256(raw)
	path := filepath.Join(h.config.EvidenceDirectory, "pause-"+hex.EncodeToString(sum[:])+".json")
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	_, e = file.Write(append(raw, '\n'))
	if e == nil {
		e = file.Sync()
	}
	closeErr := file.Close()
	if e != nil || closeErr != nil {
		return nil, errors.New("pause evidence persistence failed")
	}
	dir, e := os.Open(h.config.EvidenceDirectory)
	if e != nil {
		return nil, e
	}
	e = dir.Sync()
	dir.Close()
	if e != nil {
		return nil, e
	}
	h.evidencePath = path
	h.proof = proof
	return proof, nil
}

// QEMUAlive matches process starttime, never PID alone. Unknown /proc failures
// are treated as alive so they cannot release controller exclusion.
func QEMUAlive(c Config) bool {
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", c.QEMUPID))
	if errors.Is(e, os.ErrNotExist) {
		return false
	}
	if e != nil {
		return true
	}
	end := strings.LastIndex(string(raw), ")")
	if end < 0 {
		return true
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return true
	}
	return fields[19] == c.QEMUStartTime
}

// HoldUntilQEMUExit ignores pipe EOF and caller cancellation; a killed CLI still
// leaves the durable startup marker. Only observed exact-generation exit frees
// this live lock. It never clears the marker or starts the controller.
func (h *Held) HoldUntilQEMUExit() {
	for QEMUAlive(h.config) {
		time.Sleep(time.Second)
	}
	if h.db != nil {
		_ = h.db.Close()
	}
	_ = h.lock.Close()
}

func verifyQEMU(c Config) error {
	if !QEMUAlive(c) {
		return errors.New("QEMU generation unavailable")
	}
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", c.QEMUPID))
	if e != nil {
		return errors.New("QEMU command unavailable")
	}
	args := strings.Split(string(raw), "\x00")
	if len(args) == 0 || args[0] != "/usr/bin/qemu-system-x86_64" {
		return errors.New("unexpected worker executable")
	}
	required := map[string]bool{"baarcha-cube-worker-01": false, "file=/opt/baarcha-cube/worker-01/root.qcow2,if=virtio,format=qcow2": false, "file=/mnt/nvme/baarcha-cube/worker-01/data.qcow2,if=virtio,format=qcow2": false}
	for _, a := range args {
		if _, ok := required[a]; ok {
			required[a] = true
		}
	}
	for _, present := range required {
		if !present {
			return errors.New("unexpected worker disk identity")
		}
	}
	return nil
}

// ReadInventory is the non-mutating-provider preparation command used after the
// controller has been stopped. It performs no Pause and writes no stop marker.
func ReadInventory(ctx context.Context, c Config) (store.WorkerStopSnapshot, error) {
	var empty store.WorkerStopSnapshot
	if e := verifyController(ctx, c); e != nil {
		return empty, e
	}
	lock, e := maintenance.Acquire(c.Database, true)
	if e != nil {
		return empty, e
	}
	defer lock.Close()
	if e = maintenance.CheckDatabaseUsers(c.Database); e != nil {
		return empty, e
	}
	uri := url.URL{Scheme: "file", Path: c.Database, RawQuery: "mode=ro&_query_only=1&_busy_timeout=5000"}
	db, e := sql.Open("sqlite3", uri.String())
	if e != nil {
		return empty, e
	}
	defer db.Close()
	return store.WorkerStopInventoryDB(ctx, db)
}
