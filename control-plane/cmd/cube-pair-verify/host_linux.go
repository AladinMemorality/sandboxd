//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var operationLocks = []string{"/opt/baarcha/deploy-release.lock", "/opt/sandboxd/deploy-state/deploy.lock", "/run/lock/cube-operator-acceptance.lock", "/opt/baarcha-bench/cube-workload-operator.lock"}

func realPath(p string) error {
	got, e := filepath.EvalSymlinks(p)
	if e != nil || got != p {
		return failed
	}
	return nil
}
func privateFile(p string, max int64) ([]byte, error) {
	if realPath(p) != nil {
		return nil, failed
	}
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, failed
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil {
		return nil, failed
	}
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok || !s.Mode().IsRegular() || s.Mode().Perm() != 0600 || st.Uid != 0 || st.Nlink != 1 || s.Size() > max {
		return nil, failed
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, failed
	}
	return b, nil
}
func readRef(r reference) ([]byte, error) {
	b, e := privateFile(r.Path, 32<<20)
	if e != nil || digest(b) != r.SHA256 {
		return nil, failed
	}
	return b, nil
}
func verifyProcess(p processPin, exe string) ([]byte, error) {
	if p.PID <= 1 || p.Start == "" || p.Exe != exe || !hashPattern.MatchString(p.CommandSHA256) {
		return nil, failed
	}
	base := "/proc/" + strconv.Itoa(p.PID)
	info, e := os.Stat(base)
	if e != nil || info.Sys().(*syscall.Stat_t).Uid != 0 {
		return nil, failed
	}
	actual, e := os.Readlink(base + "/exe")
	if e != nil || actual != p.Exe {
		return nil, failed
	}
	stat, e := os.ReadFile(base + "/stat")
	if e != nil {
		return nil, failed
	}
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return nil, failed
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 || fields[19] != p.Start {
		return nil, failed
	}
	args, e := os.ReadFile(base + "/cmdline")
	if e != nil || digest(args) != p.CommandSHA256 {
		return nil, failed
	}
	return args, nil
}
func keepLocks(c config) error {
	if len(c.LockFDs) != len(operationLocks) {
		return failed
	}
	seen := map[int]bool{}
	for _, p := range operationLocks {
		fd, ok := c.LockFDs[p]
		if !ok || fd <= 2 || seen[fd] {
			return failed
		}
		seen[fd] = true
		if checkLock(p, fd) != nil {
			return failed
		}
	}
	// The exclusive lifetime lock prevents a background supervisor from starting
	// the original40GiB worker while the clone consumes that host capacity.
	if c.WorkerLockFD <= 2 || seen[c.WorkerLockFD] || checkLock("/opt/baarcha-cube/worker-01/backup.lock", c.WorkerLockFD) != nil {
		return failed
	}
	// Never Unlock: the inherited OFDs remain locked in the parent coordinator.
	return nil
}
func noOriginalQEMU() error {
	dirs, e := os.ReadDir("/proc")
	if e != nil {
		return failed
	}
	for _, d := range dirs {
		if _, e = strconv.Atoi(d.Name()); e != nil {
			continue
		}
		p := "/proc/" + d.Name()
		exe, e := os.Readlink(p + "/exe")
		if errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ESRCH) {
			continue
		}
		if e != nil {
			return failed
		}
		if !strings.Contains(filepath.Base(exe), "qemu-system") {
			continue
		}
		b, e := os.ReadFile(p + "/cmdline")
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return failed
		}
		for _, original := range []string{"/opt/baarcha-cube/worker-01/root.qcow2", "/mnt/nvme/baarcha-cube/worker-01/data.qcow2"} {
			if bytes.Contains(b, []byte(original)) {
				return failed
			}
		}
	}
	return nil
}
func guard(ctx context.Context, c config) error {
	b, e := exec.CommandContext(ctx, "systemctl", "show", "baarcha-cube-worker-01.service", "--property=ActiveState", "--value").Output()
	if e != nil || strings.TrimSpace(string(b)) != "inactive" {
		return failed
	}
	args, e := verifyProcess(c.Clone, "/usr/bin/qemu-system-x86_64")
	if e != nil {
		return e
	}
	for _, p := range []string{c.CloneRoot, c.CloneData} {
		if realPath(p) != nil || !qemuDiskArgument(args, p) || distinctCloneDisk(p) != nil {
			return failed
		}
	}
	if !bytes.Contains(args, []byte("hostfwd=tcp:127.0.0.1:21222-:22")) {
		return failed
	}
	args, e = verifyProcess(c.Proxy, "/usr/bin/ssh")
	if e != nil {
		return e
	}
	a := strings.Split(string(args), "\x00")
	pairs := map[string]bool{}
	for i := 0; i+1 < len(a); i++ {
		pairs[a[i]+" "+a[i+1]] = true
	}
	if !pairs["-L 127.0.0.1:21080:127.0.0.1:80"] || !pairs["-p 21222"] || !bytes.Contains(args, []byte("root@127.0.0.1\x00")) {
		return failed
	}
	return nil
}
func closedDB(c config) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, e := os.Lstat(c.Database.Path + suffix); !errors.Is(e, os.ErrNotExist) {
			return failed
		}
	}
	_, e := readRef(c.Database)
	if e != nil {
		return e
	}
	return noFileUsers(c.Database.Path)
}
func run(ctx context.Context, p string) error {
	if os.Geteuid() != 0 {
		return failed
	}
	b, e := privateFile(p, 1<<20)
	if e != nil {
		return e
	}
	var c config
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || validate(c) != nil {
		return failed
	}
	if realPath(c.Generation) != nil {
		return failed
	}
	s, e := os.Stat(c.Generation)
	if e != nil || !s.IsDir() || s.Mode().Perm() != 0700 || s.Sys().(*syscall.Stat_t).Uid != 0 {
		return failed
	}
	if keepLocks(c) != nil || noOriginalQEMU() != nil || guard(ctx, c) != nil || closedDB(c) != nil {
		return failed
	}
	expected, e := sourceExpected(c, readRef)
	if e != nil {
		return e
	}
	key, e := readRef(c.Key)
	if e != nil {
		return e
	}
	defer clear(key)
	cred, e := readDB(ctx, c, expected, key)
	if e != nil {
		return e
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	if e = verifyGuest(ctx, c, expected, networkGetter(c, cred, func() error { return guard(ctx, c) }, client)); e != nil {
		return e
	}
	if guard(ctx, c) != nil || noOriginalQEMU() != nil || closedDB(c) != nil {
		return failed
	}
	for _, r := range appendRefs(c) {
		if _, e = readRef(r); e != nil {
			return e
		}
	}
	current, e := privateFile(p, 1<<20)
	if e != nil || !bytes.Equal(current, b) {
		return failed
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"version": 1, "application_home_sql_history_verified": true, "read_only": true,
		"config_sha256": digest(b), "database_sha256": c.Database.SHA256, "operator_journal_sha256": c.OperatorJournal.SHA256,
		"app_id": appID, "sandbox_id": sandboxID, "runtime_id": c.RuntimeID, "clone_pid": c.Clone.PID, "clone_start_ticks": c.Clone.Start, "clone_cmdline_sha256": c.Clone.CommandSHA256,
		"failed_tasks_preserved": 4, "checkpoint_ids_match": true, "checkpoint_object_revert_verified": false,
		"ai_coding_passed": false, "platform_database_acl_verified": false, "offhost_cipher_integrity_verified": false,
	})
}

func qemuDiskArgument(args []byte, p string) bool {
	for _, arg := range strings.Split(string(args), "\x00") {
		for _, field := range strings.Split(arg, ",") {
			if field == "file="+p {
				return true
			}
		}
	}
	return false
}
func distinctCloneDisk(p string) error {
	s, e := os.Stat(p)
	if e != nil || !s.Mode().IsRegular() {
		return failed
	}
	st := s.Sys().(*syscall.Stat_t)
	if st.Uid != 0 || st.Nlink != 1 {
		return failed
	}
	for _, original := range []string{"/opt/baarcha-cube/worker-01/root.qcow2", "/mnt/nvme/baarcha-cube/worker-01/data.qcow2"} {
		o, e := os.Stat(original)
		if e != nil {
			return failed
		}
		if os.SameFile(s, o) {
			return failed
		}
	}
	return nil
}
func noFileUsers(p string) error {
	target, e := os.Stat(p)
	if e != nil {
		return failed
	}
	procs, e := os.ReadDir("/proc")
	if e != nil {
		return failed
	}
	for _, proc := range procs {
		pid, e := strconv.Atoi(proc.Name())
		if e != nil || pid == os.Getpid() {
			continue
		}
		fds, e := os.ReadDir("/proc/" + proc.Name() + "/fd")
		if errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ESRCH) {
			continue
		}
		if e != nil {
			return failed
		}
		for _, fd := range fds {
			s, e := os.Stat("/proc/" + proc.Name() + "/fd/" + fd.Name())
			if errors.Is(e, os.ErrNotExist) || errors.Is(e, syscall.ESRCH) {
				continue
			}
			if e != nil {
				return failed
			}
			if os.SameFile(s, target) {
				return failed
			}
		}
	}
	return nil
}

func checkLock(p string, fd int) error {
	if realPath(p) != nil {
		return failed
	}
	s, e := os.Stat(p)
	if e != nil {
		return failed
	}
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Mode&0777 != 0600 || st.Nlink != 1 {
		return failed
	}
	want := s.Sys().(*syscall.Stat_t)
	if st.Dev != want.Dev || st.Ino != want.Ino {
		return failed
	}
	// A separate SH contender proves another OFD already excludes readers.
	// Only then test EX on the inherited OFD: this cannot invent/up-convert the
	// parent's required continuously held lock.
	probe, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return failed
	}
	defer probe.Close()
	probeInfo, e := probe.Stat()
	if e != nil || !os.SameFile(s, probeInfo) {
		return failed
	}
	e = syscall.Flock(int(probe.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if e == nil {
		return failed
	}
	if !errors.Is(e, syscall.EWOULDBLOCK) && !errors.Is(e, syscall.EAGAIN) {
		return failed
	}
	if syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return failed
	}
	return nil
}
