package main

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"strings"
	"time"
)

// A Cube guest has one owner UID. Process-group signals alone miss daemonized
// descendants (setsid/double-fork), so migration stops every process of that UID
// except runtimed and its trusted bootstrap ancestors. pidfds avoid PID-reuse
// races; a fork storm or unreadable proc state fails closed with writes fenced.
func stopWorkspaceWriters(ctx context.Context) error {
	uid := os.Geteuid()
	if uid != 1000 || os.Getenv("RUNTIMED_CUBE_GUEST") != "1" {
		return errors.New("private migration requires isolated guest UID 1000")
	}
	protected := map[int]bool{}
	pid := os.Getpid()
	for pid > 0 {
		if protected[pid] {
			return errors.New("invalid supervisor ancestry")
		}
		protected[pid] = true
		_, ppid, _, e := workspaceProcessStatus(pid)
		if e != nil {
			return e
		}
		pid = ppid
	}
	return stopWorkspaceUID(ctx, uid, protected)
}
func workspaceProcessStatus(pid int) (uid, ppid int, zombie bool, err error) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if e != nil {
		return 0, 0, false, e
	}
	uid = -1
	ppid = -1
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "Uid:":
			uid, err = strconv.Atoi(f[1])
		case "PPid:":
			ppid, err = strconv.Atoi(f[1])
		case "State:":
			zombie = f[1] == "Z" || f[1] == "X"
		}
		if err != nil {
			return
		}
	}
	if uid < 0 || ppid < 0 {
		err = errors.New("incomplete guest process status")
	}
	return
}
func stopWorkspaceUID(ctx context.Context, uid int, protected map[int]bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		entries, e := os.ReadDir("/proc")
		if e != nil {
			return e
		}
		remaining := 0
		for _, entry := range entries {
			pid, e := strconv.Atoi(entry.Name())
			if e != nil || protected[pid] {
				continue
			}
			owner, _, zombie, e := workspaceProcessStatus(pid)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if owner != uid || zombie {
				continue
			}
			remaining++
			fd, e := unix.PidfdOpen(pid, 0)
			if e == unix.ESRCH {
				continue
			}
			if e != nil {
				return errors.New("pidfd unavailable; cannot prove workspace quiescence")
			}
			e = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
			unix.Close(fd)
			if e != nil && e != unix.ESRCH {
				return e
			}
		}
		if remaining == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("workspace writers could not be quiesced")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
