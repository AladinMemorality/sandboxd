// Package maintenance fences offline migrations from every daemon writer.
package maintenance

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Acquire uses the canonical database location for both daemon and operator.
// The daemon holds a shared lock for its lifetime; migration uses exclusive.
// Never unlink the lockfile: that would permit two locks on different inodes.
func Acquire(database string, exclusive bool) (*os.File, error) {
	absolute, err := filepath.Abs(database)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	if resolved, e := filepath.EvalSymlinks(absolute); e == nil {
		absolute = resolved
	}
	flags := os.O_RDWR
	if !exclusive {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(absolute+".maintenance.lock", flags, 0600)
	if err != nil {
		return nil, err
	}
	kind := syscall.LOCK_SH
	if exclusive {
		kind = syscall.LOCK_EX
	}
	if err = syscall.Flock(int(f.Fd()), kind|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("database is in use by the daemon or offline maintenance: %w", err)
	}
	const marker = "sandboxd-offline-maintenance-v1\n"
	if exclusive {
		data, e := io.ReadAll(io.LimitReader(f, 128))
		if e != nil || string(data) != marker {
			f.Close()
			return nil, fmt.Errorf("lock-aware sandboxd must run before first offline migration")
		}
	} else {
		if _, err = f.WriteAt([]byte(marker), 0); err == nil {
			err = f.Truncate(int64(len(marker)))
		}
		if err == nil {
			err = f.Sync()
		}
		if err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}
