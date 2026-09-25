package maintenance

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

// WorkerStopMarker deliberately has no expiry. Only separately reviewed boot
// reconciliation may remove it; EOF, age, or a successful pause is insufficient.
func WorkerStopMarker(database string) (string, error) {
	path, e := filepath.Abs(database)
	if e != nil {
		return "", e
	}
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", e
	}
	return canonical + ".worker-stop.json", nil
}
func CheckWorkerStop(database string) error {
	path, e := WorkerStopMarker(database)
	if e != nil {
		return e
	}
	_, e = os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	return errors.New("worker stop journal requires offline boot reconciliation")
}

// WriteWorkerStop must be called under the exclusive controller maintenance lock
// before any provider mutation. Never overwrite an older unfinished operation.
func WriteWorkerStop(database string, value any) error {
	path, e := WorkerStopMarker(database)
	if e != nil {
		return e
	}
	data, e := json.Marshal(value)
	if e != nil {
		return e
	}
	if len(data) > 1<<20 {
		return errors.New("worker stop marker too large")
	}
	absolute, e := filepath.Abs(database)
	if e != nil {
		return e
	}
	real, e := filepath.EvalSymlinks(absolute)
	if e != nil || real != absolute {
		return errors.New("canonical database path required")
	}
	file, e := os.CreateTemp(filepath.Dir(path), ".worker-stop-stage-")
	if e != nil {
		return e
	}
	temp := file.Name()
	defer os.Remove(temp)
	_, e = file.Write(append(bytes.TrimSpace(data), '\n'))
	if e == nil {
		e = file.Sync()
	}
	closeErr := file.Close()
	if e != nil || closeErr != nil {
		return errors.Join(e, closeErr)
	}
	// Hard-link publication is atomic and refuses an existing final marker, unlike
	// rename overwrite. The completed file and containing directory are fsynced
	// before the caller can issue its first provider Pause.
	if e = os.Link(temp, path); e != nil {
		return e
	}
	if e = os.Remove(temp); e != nil {
		return e
	}
	parent, openErr := os.Open(filepath.Dir(path))
	if openErr != nil {
		return errors.Join(e, openErr)
	}
	defer parent.Close()
	return errors.Join(e, parent.Sync())
}

// ClearWorkerStop is only for offline startup reconciliation under the exclusive
// maintenance lock. Compare the exact retained JSON value and inode; never clear
// an absent, symlinked, partial, replaced or unreviewed marker.
func ClearWorkerStop(database string, expected any) error {
	path, e := WorkerStopMarker(database)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || info.Size() > 1<<20 {
		return errors.New("unsafe startup marker")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if e != nil {
		return e
	}
	want, e := json.Marshal(expected)
	if e != nil {
		return e
	}
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(want, &b) != nil || !reflect.DeepEqual(a, b) {
		return errors.New("startup marker differs from verified generation")
	}
	current, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !os.SameFile(info, current) {
		return errors.New("startup marker was replaced")
	}
	if e = os.Remove(path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
