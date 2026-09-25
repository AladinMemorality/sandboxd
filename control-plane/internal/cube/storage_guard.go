package cube

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const (
	StorageGiB               int64 = 1 << 30
	StorageBaseline                = 96 * StorageGiB
	StorageReserve                 = 48 * StorageGiB
	StorageGrant                   = 12 * StorageGiB // fixed 10GiB writable disk + 2GiB pause RAM
	StorageObservationMaxAge       = 30 * time.Second
)

var ErrStorageUnavailable = fmt.Errorf("%w: Cube storage observation or reserve unavailable", ErrCapacityUnavailable)

// StorageGuardConfig contains operator-pinned identities, never guest input.
// Both daemon and migration CLI must use the same read-only observation path.
// ExpectedBootID changes only through reviewed operator configuration after reboot.
type StorageClock struct {
	BootID string
	NS     int64
}

type StorageGuardConfig struct {
	OuterBootID     string `json:"outer_boot_id"`
	ObservationPath string `json:"observation_path"`
	ObserverID      string `json:"observer_id"`
	WorkerMachineID string `json:"worker_machine_id"`
	ExpectedBootID  string `json:"expected_boot_id"`
	InnerFSUUID     string `json:"inner_fs_uuid"`
	OuterFSUUID     string `json:"outer_fs_uuid"`
}
type StorageObservation struct {
	OuterBootID     string `json:"outer_boot_id"`
	Version         int    `json:"version"`
	ObserverID      string `json:"observer_id"`
	Generation      int64  `json:"generation"`
	StartedNS       int64  `json:"started_boottime_ns"`
	CompletedNS     int64  `json:"completed_boottime_ns"`
	WorkerMachineID string `json:"worker_machine_id"`
	WorkerBootID    string `json:"worker_boot_id"`
	InnerFSUUID     string `json:"inner_fs_uuid"`
	OuterFSUUID     string `json:"outer_fs_uuid"`
	InnerFreeBytes  int64  `json:"inner_free_bytes"`
	OuterFreeBytes  int64  `json:"outer_free_bytes"`
}

var storageHex = regexp.MustCompile(`^[0-9a-f]{32}$`)
var storageUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (c StorageGuardConfig) Validate() error {
	if !filepath.IsAbs(c.ObservationPath) || filepath.Clean(c.ObservationPath) != c.ObservationPath || !storageHex.MatchString(c.ObserverID) || !storageHex.MatchString(c.WorkerMachineID) || c.WorkerMachineID == "00000000000000000000000000000000" || !storageUUID.MatchString(c.OuterBootID) || !storageUUID.MatchString(c.ExpectedBootID) || !storageUUID.MatchString(c.InnerFSUUID) || !storageUUID.MatchString(c.OuterFSUUID) {
		return errors.New("invalid pinned Cube storage guard configuration")
	}
	return nil
}

// Contract excludes the two explicit boot pins, which may be explicitly changed after
// an operator-verified reboot. The monotonic observation epoch still persists.
func (c StorageGuardConfig) Contract() string {
	c.ExpectedBootID = ""
	c.OuterBootID = ""
	b, _ := json.Marshal(c)
	return string(b)
}
func (o StorageObservation) Validate(c StorageGuardConfig, now StorageClock) error {
	if c.Validate() != nil || o.Version != 1 || now.BootID != c.OuterBootID || o.OuterBootID != c.OuterBootID || o.ObserverID != c.ObserverID || o.WorkerMachineID != c.WorkerMachineID || o.WorkerBootID != c.ExpectedBootID || o.InnerFSUUID != c.InnerFSUUID || o.OuterFSUUID != c.OuterFSUUID || o.Generation < 1 || o.StartedNS <= 0 || o.CompletedNS < o.StartedNS || o.CompletedNS > now.NS || now.NS-o.StartedNS > int64(StorageObservationMaxAge) || o.InnerFreeBytes < 0 || o.OuterFreeBytes < 0 {
		return ErrStorageUnavailable
	}
	return nil
}

// ReadStorageObservation uses a single opened descriptor and bounded strict
// JSON. The root-owned parent is mounted read-only into the controller; no
// request causes SSH, filesystem probing, or writes to the observation file.
func ReadStorageObservation(c StorageGuardConfig, now StorageClock) (StorageObservation, error) {
	var o StorageObservation
	if c.Validate() != nil {
		return o, ErrStorageUnavailable
	}
	p, err := os.Lstat(filepath.Dir(c.ObservationPath))
	if err != nil || !p.IsDir() || p.Mode().Perm()&0022 != 0 {
		return o, ErrStorageUnavailable
	}
	ps, ok := p.Sys().(*syscall.Stat_t)
	if !ok || ps.Uid != 0 {
		return o, ErrStorageUnavailable
	}
	fd, err := syscall.Open(c.ObservationPath, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return o, ErrStorageUnavailable
	}
	f := os.NewFile(uintptr(fd), c.ObservationPath)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0022 != 0 || st.Size() > 8192 {
		return o, ErrStorageUnavailable
	}
	ss, ok := st.Sys().(*syscall.Stat_t)
	if !ok || ss.Uid != 0 || ss.Nlink != 1 {
		return o, ErrStorageUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(b) > 8192 {
		return o, ErrStorageUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&o) != nil {
		return o, ErrStorageUnavailable
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return o, ErrStorageUnavailable
	}
	return o, o.Validate(c, now)
}

// RequireStorageGuard is used by production entrypoints. Library-only synthetic
// fixtures can omit the guard on a DB where it has never been enrolled.
func (c AdmissionConfig) RequireStorageGuard() error {
	if c.StorageGuard == nil || c.MaxActive > 4 || c.WritableDiskMB != 10240 {
		return errors.New("Cube production admission requires storage guard and at most four active guests")
	}
	return c.StorageGuard.Validate()
}
