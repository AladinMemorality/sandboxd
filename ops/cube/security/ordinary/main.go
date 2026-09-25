//go:build linux

// Explicit owned-listener acceptance. No raw sockets, BPF or network mutations.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

type guest struct {
	SB     *cube.Sandbox
	Token  string
	Client *rt.Client
	cancel context.CancelFunc
	done   chan struct{}
}
type harness struct {
	ctx                                  context.Context
	cancel                               context.CancelFunc
	cube                                 *cube.Client
	stage, workerStage, marker, template string
	guests                               []*guest
	report                               map[string]any
	http                                 *http.Client
	mu                                   sync.Mutex
	starts                               int
	last                                 time.Time
	forbiddenDials                       atomic.Int32
}
type failure struct{ error }

func must(e error) {
	if e != nil {
		panic(failure{e})
	}
}
func (h *harness) save() {
	b, e := json.MarshalIndent(h.report, "", "  ")
	must(e)
	must(os.WriteFile(filepath.Join(h.stage, "report.json"), b, 0600))
}
func (h *harness) dial(ctx context.Context, network, address string) (net.Conn, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.starts >= 128 {
		return nil, errors.New("host connection budget exhausted")
	}
	if pause := 260*time.Millisecond - time.Since(h.last); pause > 0 {
		select {
		case <-time.After(pause):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	h.starts++
	h.last = time.Now()
	return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, address)
}
func (h *harness) worker(action string, args ...string) map[string]any {
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	base := []string{"-i", "/opt/baarcha-cube/worker-01/operator-key", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "-o", "BatchMode=yes", "-p", "20222", "root@127.0.0.1", "python3", h.workerStage + "/worker.py", action, "--stage", h.workerStage}
	for _, arg := range args {
		if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(arg) {
			panic("invalid fixed worker argument")
		}
	}
	out, e := exec.CommandContext(ctx, "ssh", append(base, args...)...).Output()
	must(e)
	if len(out) > 65536 {
		panic("oversized worker report")
	}
	var v map[string]any
	must(json.Unmarshal(out, &v))
	return v
}
func (h *harness) publishOwned() {
	ids := []string{}
	for _, g := range h.guests {
		if g.SB != nil {
			ids = append(ids, g.SB.SandboxID)
		}
	}
	b, e := json.Marshal(ids)
	must(e)
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-i", "/opt/baarcha-cube/worker-01/operator-key", "-o", "UserKnownHostsFile=/opt/baarcha-cube/worker-01/known_hosts", "-o", "BatchMode=yes", "-p", "20222", "root@127.0.0.1", "cat > "+h.workerStage+"/owned-ids.json")
	cmd.Stdin = bytes.NewReader(b)
	must(cmd.Run())
	// Private recovery receipt; never print bearer/traffic tokens.
	b, e = json.Marshal(h.guests)
	must(e)
	must(os.WriteFile(filepath.Join(h.stage, "owned-guests.private.json"), b, 0600))
}
func (h *harness) remote(g *guest) *rt.Client {
	c, e := rt.NewRemoteClient(rt.RemoteConfig{BaseURL: "http://127.0.0.1:20080", Host: "3031-" + g.SB.SandboxID + ".cube.app", Token: g.Token, TrafficAccessToken: g.SB.TrafficAccessToken})
	must(e)
	return c
}
func (h *harness) detach(g *guest) {
	if g.cancel != nil {
		g.cancel()
		select {
		case <-g.done:
		case <-time.After(5 * time.Second):
			panic("reverse channel failed to close")
		}
		g.cancel = nil
	}
}
func (h *harness) attach(g *guest) {
	h.detach(g)
	for i := 0; i < 20; i++ {
		conn, e := g.Client.OpenEgressChannel(h.ctx)
		if e == nil {
			ctx, cancel := context.WithCancel(h.ctx)
			g.cancel = cancel
			g.done = make(chan struct{})
			go func() {
				defer close(g.done)
				_ = egress.RunHost(ctx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: g.SB.SandboxID, Generation: h.marker}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, Ports: []uint16{18081}}, DialContext: func(context.Context, string, string) (net.Conn, error) {
					h.forbiddenDials.Add(1)
					return nil, errors.New("no external dial in synthetic acceptance")
				}, Services: map[string]http.Handler{"bridge": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					identity, ok := egress.SourceIdentity(r.Context())
					if !ok || identity.SandboxID != g.SB.SandboxID || r.Method != "POST" || r.URL.Path != "/api/bridge" || r.Header.Get("X-Baarcha-Bridge") != h.marker {
						http.Error(w, "denied", 403)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"marker":"`+h.marker+`"}`)
				})}})
			}()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	panic("authenticated reverse channel not available")
}
func (h *harness) create() *guest {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	must(e)
	g := &guest{Token: hex.EncodeToString(b)}
	g.SB, e = h.cube.Create(h.ctx, cube.CreateRequest{TemplateID: h.template, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": g.Token}, Metadata: map[string]string{"operator-fixture": h.marker}, TimeoutSeconds: 1200, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}})
	must(e)
	h.guests = append(h.guests, g)
	h.publishOwned()
	g.Client = h.remote(g)
	h.attach(g)
	return g
}
func (h *harness) request(g *guest, port int, path string) map[string]any {
	ctx, cancel := context.WithTimeout(h.ctx, 65*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:20080"+path, nil)
	must(e)
	req.Host = fmt.Sprintf("%d-%s.cube.app", port, g.SB.SandboxID)
	req.Header.Set("Cube-Traffic-Access-Token", g.SB.TrafficAccessToken)
	res, e := h.http.Do(req)
	must(e)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		panic(fmt.Sprintf("owned app returned HTTP%d", res.StatusCode))
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	must(e)
	if len(b) > 65536 {
		panic("oversized app fixture response")
	}
	var out map[string]any
	must(json.Unmarshal(b, &out))
	return out
}
func (h *harness) install(g *guest, sibling string) {
	script, e := os.ReadFile(filepath.Join(h.stage, "guest.mjs"))
	must(e)
	targets := []map[string]any{{"label": "worker", "address": "10.0.2.15", "port": 18081}, {"label": "gateway", "address": "192.168.0.1", "port": 18082}, {"label": "guest-gateway-alias", "address": "169.254.68.5", "port": 18082}, {"label": "sibling", "address": sibling, "port": 3005}}
	spec, e := json.Marshal(map[string]any{"marker": h.marker, "targets": targets})
	must(e)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, name := range []string{"package.json", "pnpm-lock.yaml", "package-lock.json", "yarn.lock", ".npmrc"} {
		data, err := g.Client.ReadFile(h.ctx, name)
		if err != nil {
			var response *rt.ResponseError
			if errors.As(err, &response) && response.StatusCode == 404 {
				continue
			}
			must(err)
		}
		f, err := z.Create(name)
		must(err)
		_, err = f.Write(data)
		must(err)
	}
	for name, b := range map[string][]byte{"guest.mjs": script, "config/fixture.json": spec, "sandbox.yaml": []byte("version: 1\nweb:\n  command: \"node guest.mjs\"\n  port: 3001\n  health_path: \"/health\"\nbuild:\n  command: \"\"\n")} {
		f, e := z.Create(name)
		must(e)
		_, e = f.Write(b)
		must(e)
	}
	must(z.Close())
	clean, err := rt.SanitizeSourceArchive(buf.Bytes())
	must(err)
	contents, err := zip.NewReader(bytes.NewReader(clean), int64(len(clean)))
	must(err)
	present := map[string]bool{}
	for _, f := range contents.File {
		present[f.Name] = true
	}
	for _, required := range []string{"guest.mjs", "config/fixture.json", "sandbox.yaml"} {
		if !present[required] {
			panic("required synthetic source removed by publication policy")
		}
	}
	before, e := g.Client.Status(h.ctx)
	must(e)
	if err := g.Client.ImportSource(h.ctx, buf.Bytes()); err != nil {
		panic(failure{fmt.Errorf("source import: %w", err)})
	}
	restarted := false
	for i := 0; i < 30; i++ {
		current, err := g.Client.Status(h.ctx)
		if err == nil && !current.Runtimed.BootedAt.Equal(before.Runtimed.BootedAt) {
			restarted = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !restarted {
		panic("import supervisor restart did not complete")
	}
	h.attach(g)
	must(g.Client.ResumeWorkspace(h.ctx))
	for i := 0; i < 30; i++ {
		s, e := g.Client.Status(h.ctx)
		if e == nil && s.Preview.Status == rt.PreviewReady {
			h.request(g, 3001, "/health")
			return
		}
		time.Sleep(time.Second)
	}
	status, _ := g.Client.Status(h.ctx)
	h.report["failed_readiness_status"] = status
	if logs, err := g.Client.ProcessLogs(h.ctx, "web", 20); err == nil {
		h.report["synthetic_web_logs"] = logs
	}
	h.save()
	panic("synthetic app readiness failed")
}
func (h *harness) identity(g *guest) map[string]any {
	id := h.request(g, 3001, "/identity")
	if id["uid"] != float64(1000) || id["gid"] != float64(1000) {
		panic("guest identity is privileged")
	}
	status := id["status"].(map[string]any)
	for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapAmb"} {
		if status[key] != "0000000000000000" {
			panic("guest has active/inheritable capability")
		}
	}
	if status["NoNewPrivs"] != "1" {
		panic("guest no-new-privileges missing")
	}
	if len(id["accessibleForbiddenPaths"].([]any)) != 0 || len(id["presentHostSecretNames"].([]any)) != 0 {
		panic("unexpected host path or credential in guest")
	}
	for _, group := range id["groups"].([]any) {
		if group != float64(1000) {
			panic("unexpected supplementary group")
		}
	}
	return id
}
func (h *harness) phase(name string, a, b *guest) {
	phase := map[string]any{"name": name, "identity_a": h.identity(a), "identity_b": h.identity(b)}
	bindingB := h.worker("binding", "--id", b.SB.SandboxID)
	phase["sibling_binding"] = bindingB
	peer, err := netip.ParseAddr(fmt.Sprint(bindingB["address"]))
	if err != nil || !netip.MustParsePrefix("192.168.0.0/16").Contains(peer) || peer == netip.MustParseAddr("192.168.0.1") || bindingB["id"] != b.SB.SandboxID {
		panic("invalid authoritative owned sibling address")
	}
	controlsBefore := h.worker("probe")
	h.request(b, 3005, "/"+h.marker)
	beforeB := h.request(b, 3001, "/stats")
	before := h.worker("stats")
	// An ordinary pause/reboot may assign the same owned guest a different IP.
	// Bind the finite probe to its current immutable-ID lookup, never a stale IP.
	probe := h.request(a, 3001, "/probe?peer="+url.QueryEscape(peer.String())+"&marker="+h.marker)
	if probe["peer"] != peer.String() {
		panic("probe did not use the authoritative owned sibling address")
	}
	after := h.worker("stats")
	afterB := h.request(b, 3001, "/stats")
	phase["controls_before"] = controlsBefore
	phase["negative_batch"] = probe
	phase["listener_counts_before"] = before
	phase["listener_counts_after"] = after
	phase["sibling_accepts_before"] = beforeB["accepts"]
	phase["sibling_accepts_after"] = afterB["accepts"]
	phases := h.report["phases"].([]any)
	h.report["phases"] = append(phases, phase)
	h.save()
	if fmt.Sprint(before) != fmt.Sprint(after) || beforeB["accepts"] != afterB["accepts"] {
		panic("unexpected synthetic listener acceptance")
	}
	valid, occupied, unreachable := 0, 0, 0
	for _, raw := range probe["results"].([]any) {
		r := raw.(map[string]any)
		if r["connected"] == true {
			panic("protected synthetic connection accepted")
		}
		switch r["error"] {
		case "EADDRINUSE":
			occupied++
		case "TIMEOUT", "ECONNREFUSED":
			valid++
		case "EHOSTUNREACH", "ENETUNREACH":
			unreachable++
		default:
			panic("ambiguous socket error")
		}
	}
	phase["socket_denials"] = valid
	phase["unreachable_inconclusive"] = unreachable
	phase["occupied_source_port_inconclusive"] = occupied
	phase["controls_after"] = h.worker("probe")
	h.request(b, 3005, "/"+h.marker)
	broker := h.request(a, 3001, "/broker")
	phase["broker"] = broker
	phase["public_policy_dial_count"] = h.forbiddenDials.Load()
	good := broker["good"].(map[string]any)
	bad := broker["bad"].(map[string]any)
	denied := broker["denied"].(map[string]any)
	if good["status"] != float64(200) || good["marker"] != true || bad["status"] != float64(403) || denied["status"] != float64(502) || h.forbiddenDials.Load() != 0 {
		panic("reverse fixed-service/public deny control failed")
	}
	// Wrong supervisor capability, with correct private Cube ingress credential.
	wrong := &guest{SB: a.SB, Token: strings.Repeat("0", 64)}
	wc := h.remote(wrong)
	_, err = wc.ReadFile(h.ctx, "guest.mjs")
	var response *rt.ResponseError
	if !errors.As(err, &response) || response.StatusCode != 401 {
		panic("wrong supervisor token did not return401")
	}
	phase["wrong_supervisor_token_denied"] = true
	peerClient := h.remote(&guest{SB: b.SB, Token: a.Token})
	_, peerErr := peerClient.ReadFile(h.ctx, "guest.mjs")
	var peerResponse *rt.ResponseError
	if !errors.As(peerErr, &peerResponse) || peerResponse.StatusCode != 401 {
		panic("peer supervisor token did not return401")
	}
	phase["cross_guest_supervisor_token_denied"] = true
	h.save()
	fmt.Println("PASS", name)
}
func (h *harness) resume(g *guest) {
	h.detach(g)
	sb, e := h.cube.Connect(h.ctx, g.SB.SandboxID, cube.ConnectRequest{TimeoutSeconds: 1200})
	must(e)
	if sb.SandboxID != g.SB.SandboxID {
		panic("resume changed immutable sandbox identity")
	}
	// Connect returns metadata, not the durable per-instance ingress token.
	// Keep the create credential, matching the production runtime binding.
	g.Client = h.remote(g)
	h.attach(g)
	h.publishOwned()
	h.request(g, 3001, "/health")
}
func (h *harness) remove(g *guest) {
	h.detach(g)
	must(h.cube.Delete(h.ctx, g.SB.SandboxID))
	_, e := h.cube.Get(h.ctx, g.SB.SandboxID)
	if e == nil {
		panic("deleted guest still exists")
	}
	var ce *cube.APIError
	if !errors.As(e, &ce) || ce.StatusCode != 404 {
		panic("guest deletion unconfirmed")
	}
	g.SB = nil
	h.publishOwned()
}
func run() (err error) {
	if os.Getenv("CUBE_ORDINARY_ACCEPTANCE") != "reviewed-owned-listeners-v1" {
		return errors.New("explicit reviewed fixture guard required")
	}
	if len(os.Args) != 4 {
		return errors.New("template, stage and worker-stage required")
	}
	template, stage, workerStage := os.Args[1], os.Args[2], os.Args[3]
	if !regexp.MustCompile(`^tpl-[a-f0-9]{24}$`).MatchString(template) || !strings.HasPrefix(stage, "/opt/baarcha-bench/cube-ordinary-") || !regexp.MustCompile(`^/var/lib/cube-socket-fixture-[a-f0-9]{12}$`).MatchString(workerStage) {
		return errors.New("fixed staging paths/template required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	h := &harness{ctx: ctx, cancel: cancel, stage: stage, workerStage: workerStage, template: template, report: map[string]any{"network_isolation_accepted": false, "production_rollout_authorized": false, "phases": []any{}}}
	defer func() {
		if p := recover(); p != nil {
			switch v := p.(type) {
			case failure:
				err = v.error
			default:
				err = fmt.Errorf("%v", v)
			}
		}
	}()
	var scope struct {
		Marker string `json:"marker"`
	}
	v, e := os.ReadFile(filepath.Join(stage, "scope.json"))
	must(e)
	must(json.Unmarshal(v, &scope))
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(scope.Marker) {
		panic("invalid marker")
	}
	h.marker = scope.Marker
	config, e := os.ReadFile("/opt/baarcha-cube/worker-01/staging/cube-install.env")
	must(e)
	var key string
	for _, line := range strings.Split(string(config), "\n") {
		if strings.HasPrefix(line, "CUBE_API_KEY=") {
			key = strings.Trim(strings.TrimPrefix(line, "CUBE_API_KEY="), "\"'")
		}
	}
	if key == "" {
		panic("missing reviewed API credential")
	}
	transport := &http.Transport{DialContext: h.dial, ResponseHeaderTimeout: 65 * time.Second, IdleConnTimeout: 60 * time.Second}
	h.http = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	h.cube, e = cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: key, HTTPClient: h.http})
	must(e)
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 100*time.Second)
		defer stop()
		all := true
		cleanupClient, cleanupErr := cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: key})
		if cleanupErr != nil {
			err = errors.New("cleanup client unavailable")
			return
		}
		for _, g := range h.guests {
			if g.cancel != nil {
				g.cancel()
			}
			if g.SB != nil {
				if e := cleanupClient.Delete(clean, g.SB.SandboxID); e != nil {
					all = false
					continue
				}
				_, e := cleanupClient.Get(clean, g.SB.SandboxID)
				var ce *cube.APIError
				if !errors.As(e, &ce) || ce.StatusCode != 404 {
					all = false
				}
			}
		}
		h.report["cleanup_confirmed"] = all
		h.report["host_dial_starts"] = h.starts
		if !all {
			err = errors.New("owned guest cleanup unconfirmed")
		}
		b, _ := json.MarshalIndent(h.report, "", "  ")
		_ = os.WriteFile(filepath.Join(stage, "report.json"), b, 0600)
		transport.CloseIdleConnections()
	}()
	initial := h.worker("identity")
	if os.Getenv("CUBE_ORDINARY_CONTINUE_RESTART") == "reviewed-paused-owned-guests-v1" {
		previous, e := os.ReadFile(filepath.Join(stage, "report.json"))
		must(e)
		must(json.Unmarshal(previous, &h.report))
		original, ok := h.report["initial_worker"].(map[string]any)
		if !ok || original["boot_id"] == initial["boot_id"] || h.report["waiting_for_coordinated_worker_restart"] != true || h.report["cleanup_confirmed"] != false {
			panic("continuation requires an incomplete owned run and changed worker boot")
		}
		private, e := os.ReadFile(filepath.Join(stage, "owned-guests.private.json"))
		must(e)
		var saved []*guest
		must(json.Unmarshal(private, &saved))
		for _, g := range saved {
			if g.SB == nil {
				continue
			}
			actual, e := h.cube.Get(ctx, g.SB.SandboxID)
			must(e)
			if actual.State != "paused" || actual.TemplateID != template || actual.Metadata["operator-fixture"] != h.marker {
				panic("continuation guest ownership, template or paused state mismatch")
			}
			h.guests = append(h.guests, g)
		}
		if len(h.guests) != 2 {
			panic("continuation requires exactly two owned paused guests")
		}
		bindings, ok := h.report["initial_bindings"].([]any)
		if !ok || len(bindings) != 2 {
			panic("missing original sibling identity")
		}
		peerID := bindings[1].(map[string]any)["id"]
		var primary, peer *guest
		for _, g := range h.guests {
			if g.SB.SandboxID == peerID {
				peer = g
			} else {
				primary = g
			}
		}
		if primary == nil || peer == nil {
			panic("continuation cannot identify the original sibling")
		}
		h.worker("probe")
		for _, g := range h.guests {
			g.Client = h.remote(g)
			h.resume(g)
		}
		h.phase("worker-restart", primary, peer)
		h.report["restart_continued_after_control_plane_ready"] = true
		h.report["waiting_for_coordinated_worker_restart"] = false
		h.report["all_functional_phases_passed"] = true
		h.save()
		return nil
	}
	h.report["initial_worker"] = initial
	h.report["template"] = template
	a, b := h.create(), h.create()
	bindingA := h.worker("binding", "--id", a.SB.SandboxID)
	bindingB := h.worker("binding", "--id", b.SB.SandboxID)
	h.report["initial_bindings"] = []any{bindingA, bindingB}
	h.install(a, bindingB["address"].(string))
	h.install(b, bindingA["address"].(string))
	h.phase("fresh", a, b)
	h.detach(a)
	must(h.cube.Pause(ctx, a.SB.SandboxID))
	h.resume(a)
	h.phase("pause-resume", a, b)
	old := a.SB.SandboxID
	h.remove(a)
	a = h.create()
	h.report["deleted_replaced_id"] = old
	h.install(a, bindingB["address"].(string))
	h.phase("replacement", a, b)
	h.detach(a)
	h.detach(b)
	must(h.cube.Pause(ctx, a.SB.SandboxID))
	must(h.cube.Pause(ctx, b.SB.SandboxID))
	h.report["waiting_for_coordinated_worker_restart"] = true
	h.save()
	fmt.Println("READY_FOR_COORDINATED_WORKER_RESTART")
	// Coordinator performs exactly one separately approved fresh-worker restart.
	changed := false
	for i := 0; i < 120; i++ {
		time.Sleep(time.Second)
		func() {
			defer func() { _ = recover() }()
			next := h.worker("identity")
			if next["boot_id"] != initial["boot_id"] {
				changed = true
			}
		}()
		if changed {
			break
		}
	}
	if !changed {
		panic("coordinated worker restart deadline")
	}
	// SSH/network readiness precedes the worker's database and API readiness.
	// Wait on read-only requests; never retry an uncertain lifecycle mutation.
	apiReady := false
	for i := 0; i < 120; i++ {
		first, firstErr := h.cube.Get(ctx, a.SB.SandboxID)
		second, secondErr := h.cube.Get(ctx, b.SB.SandboxID)
		if firstErr == nil && secondErr == nil && first.State == "paused" && second.State == "paused" {
			apiReady = true
			break
		}
		time.Sleep(time.Second)
	}
	if !apiReady {
		panic("postrestart paused guest API readiness deadline")
	}
	// The coordinator restarts only this run's synthetic sentinel after boot.
	for i := 0; i < 30; i++ {
		ok := false
		func() { defer func() { _ = recover() }(); h.worker("probe"); ok = true }()
		if ok {
			break
		}
		if i == 29 {
			panic("postrestart sentinel missing")
		}
		time.Sleep(time.Second)
	}
	h.resume(a)
	h.resume(b)
	h.phase("worker-restart", a, b)
	h.report["waiting_for_coordinated_worker_restart"] = false
	h.report["all_functional_phases_passed"] = true
	h.save()
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "owned socket fixture failed:", e)
		os.Exit(1)
	}
}
