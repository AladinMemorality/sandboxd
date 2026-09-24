package migration

import (
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"testing"
	"time"
)

func TestMigrationRequiresAllDeclaredWorkersHealthy(t *testing.T) {
	worker := runtime.ProcessState{Name: "jobs", Kind: "worker", Running: true, Pid: 41}
	for _, tc := range []struct {
		name   string
		status *runtime.Status
		want   bool
	}{
		{"missing status", nil, false},
		{"worker has no web and no process", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewNone}}, false},
		{"worker running", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewNone}, Processes: []runtime.ProcessState{worker}}, true},
		{"worker stopped", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewNone}, Processes: []runtime.ProcessState{{Name: "jobs", Kind: "worker", Running: false}}}, false},
		{"worker missing PID", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewNone}, Processes: []runtime.ProcessState{{Name: "jobs", Kind: "worker", Running: true}}}, false},
		{"one of two workers failed", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewNone}, Processes: []runtime.ProcessState{worker, {Name: "mail", Kind: "worker"}}}, false},
		{"web ready with failed worker", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewReady}, Processes: []runtime.ProcessState{{Name: "web", Kind: "web", Running: true, Pid: 40}, {Name: "jobs", Kind: "worker"}}}, false},
		{"web ready and worker running", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewReady}, Processes: []runtime.ProcessState{{Name: "web", Kind: "web", Running: true, Pid: 40}, worker}}, true},
		{"legacy Docker web", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewReady}}, true},
		{"web starting", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewStarting}, Processes: []runtime.ProcessState{worker}}, false},
		{"active task", &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewNone}, Processes: []runtime.ProcessState{worker}, ActiveTask: &runtime.ActiveTask{}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := migrationRuntimeHealthy(tc.status); got != tc.want {
				t.Fatalf("healthy=%v want%v", got, tc.want)
			}
		})
	}
}
func TestMigrationReadinessRejectsCrashLoopUntilStable(t *testing.T) {
	now := time.Unix(1000, 0)
	r := migrationReadiness{Window: 2 * time.Second}
	s := &runtime.Status{Preview: runtime.PreviewState{Status: runtime.PreviewNone}, Processes: []runtime.ProcessState{{Name: "jobs", Kind: "worker", Running: true, Pid: 40}}}
	for i := 0; i < 5; i++ {
		s.Processes[0].Pid++
		s.Processes[0].Restarts++
		if r.Observe(s, now.Add(time.Duration(i)*time.Second)) {
			t.Fatal("crash loop accepted")
		}
	}
	if r.Observe(s, now.Add(5*time.Second)) {
		t.Fatal("readiness accepted before stable window")
	}
	if !r.Observe(s, now.Add(6*time.Second)) {
		t.Fatal("recovered stable worker rejected")
	}
	s.Processes[0].Running = false
	if r.Observe(s, now.Add(7*time.Second)) {
		t.Fatal("stopped worker accepted")
	}
	s.Processes[0].Running = true
	if r.Observe(s, now.Add(8*time.Second)) {
		t.Fatal("unhealthy observation did not reset stability")
	}
	if !r.Observe(s, now.Add(10*time.Second)) {
		t.Fatal("healthy recovery never became ready")
	}
}
