package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestQuiescenceStopsDetachedSessionWriters(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("dedicated-UID child fixture requires root")
	}
	if _, e := exec.LookPath("setsid"); e != nil {
		t.Skip("requires setsid")
	}
	root, e := os.MkdirTemp("", "quiesce-detached-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	os.Chmod(root, 0777)
	file := filepath.Join(root, "writes")
	cmd := exec.Command("setsid", "sh", "-c", "while true; do echo tick >> '"+file+"'; sleep 0.02; done")
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 60001, Gid: 60001, NoSetGroups: true}}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(time.Second)
	for {
		if b, _ := os.ReadFile(file); len(b) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached fixture did not write")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = stopWorkspaceUID(context.Background(), 60001, map[int]bool{}); e != nil {
		t.Fatal(e)
	}
	cmd.Wait()
	before, _ := os.ReadFile(file)
	time.Sleep(80 * time.Millisecond)
	after, _ := os.ReadFile(file)
	if len(before) != len(after) {
		t.Fatal("detached child continued writing after quiescence")
	}
}
