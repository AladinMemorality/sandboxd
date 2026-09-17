package runtime

import (
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSupervisorProcessHardening(t *testing.T) {
	if os.Getenv("RUNTIMED_HARDENING_TEST_CHILD") == "1" {
		// Pin multiple pre-existing threads so this regression cannot pass just
		// because the scheduler reused the thread that performed the prctl.
		startChildren := make(chan struct{})
		childResults := make(chan error, 4)
		var pinned sync.WaitGroup
		pinned.Add(4)
		for i := 0; i < 4; i++ {
			go func() {
				goruntime.LockOSThread()
				defer goruntime.UnlockOSThread()
				pinned.Done()
				<-startChildren
				child := exec.Command("/bin/sh", "-c", "cat /proc/self/status")
				child.Env = []string{"PATH=/usr/bin:/bin"}
				out, err := child.Output()
				if err == nil && !strings.Contains(string(out), "NoNewPrivs:\t1") {
					err = fmt.Errorf("exec child did not inherit no-new-privileges")
				}
				childResults <- err
			}()
		}
		pinned.Wait()
		if os.Geteuid() == 0 {
			if err := syscall.Setgroups([]int{1000}); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Setgid(1000); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Setuid(1000); err != nil {
				t.Fatal(err)
			}
		}
		// A normal non-setuid exec makes the process dumpable again. Reproduce
		// that state and prove the same-UID read works before applying protection.
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		baseline := exec.Command("/bin/sh", "-c", fmt.Sprintf("cat /proc/%d/environ", os.Getpid()))
		baseline.Env = []string{"PATH=/usr/bin:/bin"}
		raw, err := baseline.Output()
		if err != nil || !strings.Contains(string(raw), "private-test-supervisor-token") {
			t.Fatal("same-UID proc positive control failed")
		}
		if err := ProtectSupervisorProcess(); err != nil {
			if err == syscall.ENOTSUP {
				// Race/cgo test binaries cannot use AllThreadsSyscall. The
				// production guest and this regression also run with CGO=0.
				t.Skip("requires CGO_ENABLED=0, as do Cube guest binaries")
			}
			t.Fatal(err)
		}
		close(startChildren)
		for i := 0; i < 4; i++ {
			if err := <-childResults; err != nil {
				t.Fatal(err)
			}
		}
		dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
		if err != nil || dumpable != 0 {
			t.Fatalf("dumpable=%d err=%v", dumpable, err)
		}
		status, err := os.ReadFile("/proc/self/status")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(status), "NoNewPrivs:\t1") {
			t.Fatal("no-new-privileges not set")
		}
		// The child has the SAME UID and no inherited token. It must not recover
		// the parent's original environment via proc even though exec supplied it.
		child := exec.Command("/bin/sh", "-c", fmt.Sprintf("cat /proc/%d/environ", os.Getpid()))
		child.Env = []string{"PATH=/usr/bin:/bin"}
		output, err := child.CombinedOutput()
		if err == nil || strings.Contains(string(output), "private-test-supervisor-token") {
			t.Fatal("same-UID child could read supervisor environment")
		}
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestSupervisorProcessHardening$", "-test.v")
	child.Env = append(os.Environ(), "RUNTIMED_HARDENING_TEST_CHILD=1", "RUNTIMED_HTTP_TOKEN=private-test-supervisor-token")
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("hardening subprocess: %v\n%s", err, output)
	}
}
