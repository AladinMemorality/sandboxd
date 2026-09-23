package maintenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// CheckDatabaseUsers catches older daemons which predate the cooperative flock
// protocol. Run natively on the host as root, not in a restricted PID namespace.
// This is a checkpoint, not a substitute for disabling legacy auto-restarts.
// Any other open descriptor (even read-only) conservatively blocks maintenance.
func CheckDatabaseUsers(database string) error {
	targets := []os.FileInfo{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(database + suffix)
		if err == nil {
			targets = append(targets, info)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if len(targets) == 0 {
		return errors.New("database does not exist")
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	for _, process := range processes {
		pid, err := strconv.Atoi(process.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		directory := filepath.Join("/proc", process.Name(), "fd")
		descriptors, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot inspect process %d database handles: %w", pid, err)
		}
		for _, descriptor := range descriptors {
			info, err := os.Stat(filepath.Join(directory, descriptor.Name()))
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
				continue
			}
			if err != nil {
				return fmt.Errorf("cannot inspect process %d descriptor: %w", pid, err)
			}
			for _, target := range targets {
				if os.SameFile(info, target) {
					return fmt.Errorf("process %d still has the database open; stop the control plane and its restart policy before migration", pid)
				}
			}
		}
	}
	return nil
}
