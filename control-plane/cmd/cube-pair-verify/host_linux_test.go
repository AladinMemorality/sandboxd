//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPrivateInputRejectsSymlinkHardlinkAndPublicMode(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("native root private-path contract")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "private.json")
	if e := os.WriteFile(p, []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := privateFile(p, 2); e != nil {
		t.Fatal(e)
	}
	if _, e := privateFile(p, 1); e == nil {
		t.Fatal("oversize")
	}
	link := filepath.Join(dir, "link")
	if e := os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	if _, e := privateFile(link, 8); e == nil {
		t.Fatal("symlink")
	}
	if e := os.Link(p, filepath.Join(dir, "hard")); e != nil {
		t.Fatal(e)
	}
	if _, e := privateFile(p, 8); e == nil {
		t.Fatal("hardlink")
	}
	os.Remove(filepath.Join(dir, "hard"))
	os.Chmod(p, 0644)
	if _, e := privateFile(p, 8); e == nil {
		t.Fatal("public")
	}
}
func TestQEMUDiskPinRequiresExactFileField(t *testing.T) {
	p := "/private/restore/data.qcow2"
	for _, s := range []string{"qemu\x00-drive\x00file=" + p + ",format=qcow2\x00", "qemu\x00-drive\x00format=qcow2,file=" + p + "\x00"} {
		if !qemuDiskArgument([]byte(s), p) {
			t.Fatal("valid disk")
		}
	}
	for _, s := range []string{p, "file=" + p + ".old", "otherfile=" + p, "file=/production/data.qcow2"} {
		if qemuDiskArgument([]byte(s), p) {
			t.Fatal("substring pin accepted")
		}
	}
}
func TestProcessGenerationMismatchRefused(t *testing.T) {
	p := processPin{PID: os.Getpid(), Start: "wrong", Exe: "/usr/bin/qemu-system-x86_64", CommandSHA256: strings.Repeat("0", 64)}
	if _, e := verifyProcess(p, p.Exe); e == nil {
		t.Fatal("not QEMU")
	}
}
func TestClosedDatabaseRefusesSidecars(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root contract")
	}
	p := filepath.Join(t.TempDir(), "db")
	os.WriteFile(p, []byte("closed"), 0600)
	c := config{Database: reference{p, digest([]byte("closed"))}}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		os.WriteFile(p+suffix, nil, 0600)
		if closedDB(c) == nil {
			t.Fatal("accepted sidecar", suffix)
		}
		os.Remove(p + suffix)
	}
}

func TestInheritedLockKeepsSameOFDAndRejectsContender(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("native root contract")
	}
	p := filepath.Join(t.TempDir(), "backup.lock")
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if checkLock(p, int(f.Fd())) == nil {
		t.Fatal("unlocked parent accepted")
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		t.Fatal("shared fixture lock")
	}
	if checkLock(p, int(f.Fd())) == nil {
		t.Fatal("shared parent upgraded")
	}
	probe, e := os.OpenFile(p, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	if syscall.Flock(int(probe.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		t.Fatal("shared parent was upgraded by rejected check")
	}
	probe.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		t.Fatal("exclusive fixture lock")
	}
	if checkLock(p, int(f.Fd())) != nil {
		t.Fatal("held exclusive lock refused")
	}
	duplicate, e := syscall.Dup(int(f.Fd()))
	if e != nil {
		t.Fatal(e)
	}
	defer syscall.Close(duplicate)
	if checkLock(p, duplicate) != nil {
		t.Fatal("inherited same OFD refused")
	}
	other, e := os.OpenFile(p, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	if checkLock(p, int(other.Fd())) == nil {
		t.Fatal("competing OFD acquired")
	}
	f.Close()
	if syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		t.Fatal("lock lost while inherited duplicate lives")
	}
}
