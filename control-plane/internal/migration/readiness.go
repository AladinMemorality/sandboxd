package migration

import (
	"encoding/json"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

// migrationRuntimeHealthy checks every reported process, not just the absence
// of a web preview. A valid worker-only manifest must declare at least one
// worker; an empty process list cannot prove that its workload is alive.
func migrationRuntimeHealthy(status *runtime.Status) bool {
	if status == nil || status.ActiveTask != nil {
		return false
	}
	switch status.Preview.Status {
	case runtime.PreviewReady:
		// Old retained Docker supervisors can omit Processes. Their web readiness
		// remains authoritative; Cube builds report every declared process.
	case runtime.PreviewNone:
		if len(status.Processes) == 0 {
			return false
		}
	default:
		return false
	}
	names := map[string]bool{}
	for _, p := range status.Processes {
		if p.Name == "" || names[p.Name] || !p.Running || p.Pid <= 0 {
			return false
		}
		names[p.Name] = true
		if p.Kind != "worker" && p.Kind != "web" {
			return false
		}
		if status.Preview.Status == runtime.PreviewNone && p.Kind != "worker" {
			return false
		}
	}
	return true
}

// migrationReadiness requires unchanged process identities/restart counters for
// a small observation window. A crash-looping worker briefly marked Running
// must not cause a provider switch or retirement of the retained source.
type migrationReadiness struct {
	Window   time.Duration
	since    time.Time
	identity string
}

func (r *migrationReadiness) Observe(status *runtime.Status, now time.Time) bool {
	if !migrationRuntimeHealthy(status) {
		r.since = time.Time{}
		r.identity = ""
		return false
	}
	identity, _ := json.Marshal(struct {
		BootedAt  time.Time
		Preview   runtime.PreviewStatus
		PID       int
		Restarts  int
		Processes []runtime.ProcessState
	}{status.Runtimed.BootedAt, status.Preview.Status, status.Preview.Pid, status.Preview.Restarts, status.Processes})
	if r.since.IsZero() || r.identity != string(identity) || now.Before(r.since) {
		r.since = now
		r.identity = string(identity)
		return false
	}
	return now.Sub(r.since) >= r.Window
}
