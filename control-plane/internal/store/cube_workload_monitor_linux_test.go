package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const workloadCgroup = "/sys/fs/cgroup/system.slice/baarcha-cube-worker-01.service"

type workloadPressureSample struct {
	Host       workloadHostSample `json:"hosts"`
	Cgroup     map[string]string  `json:"cgroup"`
	PlatformMS float64            `json:"platform_ms"`
	PlatformOK bool               `json:"platform_ok"`
	Phase      string             `json:"phase"`
	ElapsedMS  int64              `json:"elapsed_ms"`
}
type workloadAbortState struct {
	fullSince time.Time
	badHealth int
}

func workloadCounter(raw, key string) (uint64, error) {
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == key {
			return strconv.ParseUint(f[1], 10, 64)
		}
	}
	return 0, fmt.Errorf("missing cgroup counter %s", key)
}
func (s *workloadAbortState) check(before, now workloadPressureSample, at time.Time) error {
	if err := workloadMemorySafe(before.Host, now.Host, false); err != nil {
		return err
	}
	current, err := strconv.ParseUint(strings.TrimSpace(now.Cgroup["memory.current"]), 10, 64)
	if err != nil || current >= 40<<30 {
		return errors.New("QEMU memory at abort watermark or unreadable")
	}
	swap, err := strconv.ParseUint(strings.TrimSpace(now.Cgroup["memory.swap.current"]), 10, 64)
	if err != nil || swap != 0 {
		return errors.New("QEMU swap use or unreadable")
	}
	for _, key := range []string{"oom", "oom_kill", "max"} {
		old, e1 := workloadCounter(before.Cgroup["memory.events"], key)
		fresh, e2 := workloadCounter(now.Cgroup["memory.events"], key)
		if e1 != nil || e2 != nil || old != fresh {
			return errors.New("QEMU memory events changed or unreadable")
		}
	}
	if now.Host.Outer.MemoryFullAvg10 > 1 || now.Host.Worker.MemoryFullAvg10 > 1 {
		if s.fullSince.IsZero() {
			s.fullSince = at
		}
		if at.Sub(s.fullSince) >= 10*time.Second {
			return errors.New("memory full pressure sustained above one percent")
		}
	} else {
		s.fullSince = time.Time{}
	}
	if !now.PlatformOK || now.PlatformMS > 1000 {
		s.badHealth++
	} else {
		s.badHealth = 0
	}
	if s.badHealth >= 3 {
		return errors.New("platform health failed or exceeded one second three times")
	}
	return nil
}

type workloadMonitor struct {
	mu           sync.Mutex
	currentPhase string
	abort        string
	samples      int
	started      time.Time
	cancel       context.CancelFunc
	done         chan struct{}
	phaseFile    *os.File
}

func (m *workloadMonitor) phase(p string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentPhase = p
	_ = json.NewEncoder(m.phaseFile).Encode(map[string]any{"phase": p, "at": time.Now().UTC(), "elapsed_ms": time.Since(m.started).Milliseconds()})
}
func (m *workloadMonitor) stop() { m.cancel(); <-m.done; m.phase("cleanup"); m.phaseFile.Close() }
func (m *workloadMonitor) result() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"samples": m.samples, "abort": m.abort, "telemetry": "telemetry.ndjson", "phases": "phases.ndjson", "interval_seconds": 1, "sample_latency_is_recorded": true}
}
func workloadPressure(ctx context.Context) (workloadPressureSample, error) {
	s := workloadPressureSample{Cgroup: map[string]string{}}
	var err error
	s.Host, err = workloadMetrics(ctx)
	if err != nil {
		return s, err
	}
	for _, name := range []string{"memory.current", "memory.peak", "memory.swap.current", "memory.events", "memory.stat", "memory.pressure", "cpu.stat", "cpu.pressure", "cpu.max"} {
		b, e := os.ReadFile(filepath.Join(workloadCgroup, name))
		if e != nil {
			return s, e
		}
		s.Cgroup[name] = string(b)
	}
	probe, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	req, _ := http.NewRequestWithContext(probe, "GET", "http://127.0.0.1:3100/", nil)
	at := time.Now()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, e := client.Do(req)
	s.PlatformMS = float64(time.Since(at).Microseconds()) / 1000
	if e == nil {
		s.PlatformOK = resp.StatusCode == 200
		resp.Body.Close()
	}
	return s, nil
}
func startWorkloadMonitor(ctx context.Context, abort context.CancelFunc, dir string, before workloadHostSample) (*workloadMonitor, error) {
	initial, err := workloadPressure(ctx)
	if err != nil {
		return nil, err
	}
	if err = workloadMemorySafe(before, initial.Host, true); err != nil {
		return nil, err
	}
	if !initial.PlatformOK || initial.PlatformMS > 1000 {
		return nil, errors.New("platform not healthy before benchmark")
	}
	state := workloadAbortState{}
	if err = state.check(initial, initial, time.Now()); err != nil {
		return nil, err
	}
	out, err := os.OpenFile(filepath.Join(dir, "telemetry.ndjson"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	phases, err := os.OpenFile(filepath.Join(dir, "phases.ndjson"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		out.Close()
		return nil, err
	}
	monitorCtx, cancel := context.WithCancel(ctx)
	m := &workloadMonitor{currentPhase: "preflight", started: time.Now(), cancel: cancel, done: make(chan struct{}), phaseFile: phases}
	go func() {
		defer close(m.done)
		defer out.Close()
		for {
			if monitorCtx.Err() != nil {
				return
			}
			if _, e := os.Lstat(filepath.Join(dir, "abort-request.json")); e == nil || !os.IsNotExist(e) {
				m.mu.Lock()
				m.abort = "operator hold monitor requested abort or abort path unreadable"
				m.mu.Unlock()
				abort()
				return
			}
			started := time.Now()
			sample, e := workloadPressure(monitorCtx)
			if monitorCtx.Err() != nil {
				return
			}
			m.mu.Lock()
			sample.Phase = m.currentPhase
			sample.ElapsedMS = time.Since(m.started).Milliseconds()
			m.samples++
			m.mu.Unlock()
			row := map[string]any{"sample": sample, "sample_duration_ms": time.Since(started).Milliseconds()}
			if e == nil {
				e = state.check(initial, sample, time.Now())
			}
			if e != nil {
				row["abort"] = e.Error()
			}
			if writeErr := json.NewEncoder(out).Encode(row); writeErr != nil {
				e = writeErr
			}
			if e != nil {
				m.mu.Lock()
				m.abort = e.Error()
				m.mu.Unlock()
				abort()
				return
			}
			select {
			case <-monitorCtx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	return m, nil
}

func TestWorkloadAbortMonitor(t *testing.T) {
	m := workloadMemory{AvailableKiB: 16 << 20}
	before := workloadPressureSample{Host: workloadHostSample{Outer: m, Worker: m}, Cgroup: map[string]string{"memory.current": "1000", "memory.swap.current": "0", "memory.events": "oom 0\noom_kill 0\nmax 0\n"}, PlatformOK: true, PlatformMS: 5}
	for _, kind := range []string{"outer", "worker", "qemu", "swap", "oom", "missing"} {
		t.Run(kind, func(t *testing.T) {
			now := before
			now.Cgroup = map[string]string{}
			for k, v := range before.Cgroup {
				now.Cgroup[k] = v
			}
			switch kind {
			case "outer":
				now.Host.Outer.AvailableKiB = 7 << 20
			case "worker":
				now.Host.Worker.AvailableKiB = 5 << 20
			case "qemu":
				now.Cgroup["memory.current"] = strconv.FormatUint(40<<30, 10)
			case "swap":
				now.Cgroup["memory.swap.current"] = "1"
			case "oom":
				now.Cgroup["memory.events"] = "oom 1\noom_kill 0\nmax 0\n"
			case "missing":
				delete(now.Cgroup, "memory.events")
			}
			if (&workloadAbortState{}).check(before, now, time.Now()) == nil {
				t.Fatal("unsafe sample accepted")
			}
		})
	}
	state := workloadAbortState{}
	now := before
	now.Host.Worker.MemoryFullAvg10 = 2
	at := time.Now()
	if state.check(before, now, at) != nil || state.check(before, now, at.Add(9*time.Second)) != nil || state.check(before, now, at.Add(10*time.Second)) == nil {
		t.Fatal("pressure duration")
	}
	state = workloadAbortState{}
	now = before
	now.PlatformMS = 1001
	if state.check(before, now, at) != nil || state.check(before, now, at) != nil || state.check(before, now, at) == nil {
		t.Fatal("health consecutive threshold")
	}
}
