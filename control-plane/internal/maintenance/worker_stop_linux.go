package maintenance

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
