package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// Exercise real Linux signal/pipe behavior: losing supervisor stdout must not
// terminate a stop coordinator before it can keep its maintenance lock alive.
func TestStopCoordinatorSurvivesBrokenPipeAndOrdinarySignals(t *testing.T) {
	if os.Getenv("SANDBOXD_STOP_SIGNAL_FIXTURE") == "1" {
		holdSignals()
		for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP} {
			_ = syscall.Kill(os.Getpid(), sig)
		}
		_, _ = fmt.Fprintln(os.Stdout, "proof-to-closed-supervisor-pipe")
		ack := os.NewFile(3, "ack")
		_, _ = ack.Write([]byte("still-held"))
		ack.Close()
		return
	}
	read, write, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	read.Close()
	defer write.Close()
	ackRead, ackWrite, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer ackRead.Close()
	defer ackWrite.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStopCoordinatorSurvivesBrokenPipeAndOrdinarySignals$")
	cmd.Env = append(os.Environ(), "SANDBOXD_STOP_SIGNAL_FIXTURE=1")
	cmd.Stdout = write
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{ackWrite}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	ackWrite.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("signal fixture timed out")
	}
	raw, e := io.ReadAll(ackRead)
	if e != nil || string(raw) != "still-held" {
		t.Fatalf("coordinator exited on signal/pipe failure: %v", e)
	}
}
