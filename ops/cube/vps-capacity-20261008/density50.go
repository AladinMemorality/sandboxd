package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	guest "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const root = "/opt/baarcha/operations/vps-density-20261008-02"

type Config struct {
	APIKey    string               `json:"api_key"`
	Admission cube.AdmissionConfig `json:"admission"`
	Template  string               `json:"template"`
}
type VM struct {
	ID      string        `json:"id"`
	Token   string        `json:"token"`
	Traffic string        `json:"traffic"`
	Client  *guest.Client `json:"-"`
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func save(n string, v any) {
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	must(os.WriteFile(filepath.Join(root, n), b, 0600))
}
func event(phase string, v any) {
	save("progress.json", map[string]any{"at": time.Now().UTC(), "phase": phase, "detail": v})
	fmt.Println(phase, v)
}
func token() string {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	must(e)
	return hex.EncodeToString(b)
}
func gate() {
	if _, e := os.Stat(root + "/STOP"); e == nil {
		panic("host monitor stopped escalation")
	}
}

var hc = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func request(ctx context.Context, v VM, method, path string) (int, []byte, time.Duration, error) {
	at := time.Now()
	r, e := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:20080"+path, nil)
	if e != nil {
		return 0, nil, 0, e
	}
	r.Host = "3000-" + v.ID + ".cube.app"
	r.Header.Set("cube-traffic-access-token", v.Traffic)
	resp, e := hc.Do(r)
	if e != nil {
		return 0, nil, time.Since(at), e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return resp.StatusCode, b, time.Since(at), e
}
func round(ctx context.Context, owned []VM) []map[string]any {
	out := make([]map[string]any, len(owned))
	var wg sync.WaitGroup
	for i, v := range owned {
		wg.Add(1)
		go func(i int, v VM) {
			defer wg.Done()
			code, b, d, e := request(ctx, v, "GET", "/")
			s, se := v.Client.Status(ctx)
			ok := e == nil && se == nil && s.Preview.Status == guest.PreviewReady && code == 200 && strings.Contains(strings.ToLower(string(b)), "<html")
			previewStatus := "unavailable"
			if s != nil {
				previewStatus = string(s.Preview.Status)
			}
			out[i] = map[string]any{"preview_status": previewStatus, "id": v.ID, "ok": ok, "http": code, "seconds": d.Seconds(), "html_bytes": len(b)}
		}(i, v)
	}
	wg.Wait()
	return out
}
func run() (err error) {
	var cfg Config
	b, e := os.ReadFile(root + "/config.PRIVATE.json")
	must(e)
	must(json.Unmarshal(b, &cfg))
	if cfg.Admission.NodeID != "10.0.2.15" || cfg.Admission.MaxActive != 50 {
		panic("VPS-only 50-fixture contract required")
	}
	must(cfg.Admission.RequireStorageGuard())
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	st, e := store.Open(ctx, root+"/admission.db?_journal=WAL&_busy_timeout=5000&_fk=1", root+"/source/control-plane/migrations")
	must(e)
	defer st.Close()
	partition, e := st.AdmissionPartition("vps")
	must(e)
	client, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: cfg.APIKey})
	must(e)
	must(client.ConfigurePlacement("http://127.0.0.1:20889", "cubebox"))
	must(client.ConfigureAdmission(ctx, partition, cfg.Admission))
	owned := []VM{}
	readyCount := 0
	report := map[string]any{"target_requested": 50, "max_test_fixtures": 50, "profile": "1vCPU / 512MiB React/Vite template, pages then concurrent template builds", "worker": "vps", "rounds": []any{}, "build_rounds": []any{}}
	channels := []context.CancelFunc{}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
			report["stopped_reason"] = err.Error()
		}
		event("cleanup", len(owned))
		for _, f := range channels {
			f()
		}
		clean, stop := context.WithTimeout(context.Background(), 4*time.Minute)
		defer stop()
		failed := []string{}
		for i := len(owned) - 1; i >= 0; i-- {
			v := owned[i]
			r, e := client.Get(clean, v.ID)
			if e != nil || r.Metadata["fixture"] != "vps-density-20261008-02" {
				failed = append(failed, v.ID)
				continue
			}
			if e = client.Delete(clean, v.ID); e != nil {
				failed = append(failed, v.ID)
			}
		}
		var charged int
		qe := st.DB().QueryRow("SELECT COALESCE(SUM(charged),0) FROM cube_admission").Scan(&charged)
		report["cleanup_failures"] = failed
		report["charged_remaining"] = charged
		report["cleanup_verified"] = len(failed) == 0 && qe == nil && charged == 0
		report["created_fixtures"] = len(owned)
		report["ready_fixtures"] = readyCount
		save("result.json", report)
		event("finished", report["cleanup_verified"])
	}()
	app, e := os.ReadFile(root + "/app.cjs")
	must(e)
	for i := 0; i < cfg.Admission.MaxActive; i++ {
		gate()
		v := VM{Token: token()}
		ident := fmt.Sprintf("vps-density-20261008-02-%02d", i)
		save(fmt.Sprintf("intent-%02d.json", i), map[string]any{"id": ident, "at": time.Now().UTC()})
		at := time.Now()
		r, e := client.Create(ctx, cube.CreateRequest{TemplateID: cfg.Template, TimeoutSeconds: 1200, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0", "::/0"}}, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": v.Token}, Metadata: map[string]string{"sandboxd_id": ident, "sandboxd_app_id": ident, "fixture": "vps-density-20261008-02"}})
		must(e)
		v.ID = r.SandboxID
		v.Traffic = r.TrafficAccessToken
		owned = append(owned, v)
		save("owned.PRIVATE.json", owned)
		event("created", len(owned))
		g, e := guest.NewRemoteClient(guest.RemoteConfig{BaseURL: "http://127.0.0.1:20080", Host: "3031-" + v.ID + ".cube.app", Token: v.Token, TrafficAccessToken: v.Traffic})
		must(e)
		v.Client = g
		owned[len(owned)-1] = v
		deadline := time.Now().Add(60 * time.Second)
		for {
			_, e = g.Status(ctx)
			if e == nil {
				break
			}
			if time.Now().After(deadline) {
				panic("supervisor readiness timed out")
			}
			time.Sleep(time.Second)
		}
		cc, close := context.WithCancel(ctx)
		channels = append(channels, close)
		conn, e := g.OpenEgressChannel(cc)
		must(e)
		go egress.RunHost(cc, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: ident, Generation: v.ID}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}}})
		must(g.ResumeWorkspace(ctx))
		_, e = g.PutFile(ctx, "capacity-bench.cjs", strings.NewReader(string(app)))
		must(e)
		_, e = g.PutFile(ctx, "sandbox.yaml", strings.NewReader("version: 1\nweb:\n  command: node capacity-bench.cjs\n  port: 3000\n  health_path: /\nbuild:\n  command: \"\"\n"))
		must(e)

		old, e := g.Status(ctx)
		must(e)
		must(g.QuiesceWorkspace(ctx))
		must(g.ApplyAppConfig(ctx, guest.AppConfigRequest{Revision: ident, Env: map[string]string{}}))
		deadline = time.Now().Add(45 * time.Second)
		for {
			now, e := g.Status(ctx)
			if e == nil && !now.Runtimed.BootedAt.Equal(old.Runtimed.BootedAt) {
				break
			}
			if time.Now().After(deadline) {
				panic("fixture supervisor did not reload manifest")
			}
			time.Sleep(300 * time.Millisecond)
		}
		cc2, close2 := context.WithCancel(ctx)
		channels = append(channels, close2)
		conn2, e := g.OpenEgressChannel(cc2)
		must(e)
		go egress.RunHost(cc2, conn2, egress.HostOptions{Identity: egress.Identity{SandboxID: ident, Generation: v.ID}, Policy: egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}}})
		must(g.ResumeWorkspace(ctx))

		deadline = time.Now().Add(60 * time.Second)
		for {
			gate()
			code, body, _, e := request(ctx, v, "GET", "/__capacity/status")
			if e == nil && code == 200 && strings.Contains(string(body), "\"idle\"") {
				status, se := g.Status(ctx)
				if se == nil && status.Preview.Status == guest.PreviewReady {
					break
				}
			}
			if time.Now().After(deadline) {
				logs, _ := g.ProcessLogs(ctx, "web", 30)
				save("readiness-failure.PRIVATE.json", logs)
				panic("Vite app readiness timed out")
			}
			time.Sleep(time.Second)
		}
		readyCount++
		event("ready", map[string]any{"count": len(owned), "boot_seconds": time.Since(at).Seconds()})
		responses := round(ctx, owned)
		for _, r := range responses {
			if r["ok"] != true {
				save("failed-round.json", responses)
				panic("page or supervisor check failed")
			}
		}
		report["rounds"] = append(report["rounds"].([]any), responses)
		save("result.json", report)
		if len(owned)==10 || len(owned)==20 || len(owned)==35 || len(owned)==50 {
   event("cohort",len(owned))
   for j:=0;j<3;j++ { gate();responses:=round(ctx,owned);for _,r:=range responses {if r["ok"]!=true {panic("cohort preview failed")}};report["rounds"]=append(report["rounds"].([]any),responses);save("result.json",report);time.Sleep(3*time.Second) }
  }
  time.Sleep(2 * time.Second)
 }
	for iteration := 0; iteration < 6; iteration++ {
		gate()
		responses := round(ctx, owned)
		report["rounds"] = append(report["rounds"].([]any), responses)
		save("result.json", report)
		for _, r := range responses {
			if r["ok"] != true {
				panic("steady preview failed")
			}
		}
		event("steady", iteration+1)
		time.Sleep(5 * time.Second)
	}
	for _, n := range []int{1, 2} {
		gate()
		event("build-start", n)
		start := time.Now()
		var wg sync.WaitGroup
		codes := make([]int, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				code, _, _, _ := request(ctx, owned[i], "POST", "/__capacity/build")
				codes[i] = code
			}(i)
		}
		wg.Wait()
		for _, code := range codes {
			if code != 202 {
				panic("template build did not start")
			}
		}
		results := make([]map[string]any, n)
		for {
			gate()
			done := true
			for i := 0; i < n; i++ {
				code, b, _, e := request(ctx, owned[i], "GET", "/__capacity/status")
				must(e)
				if code != 200 {
					panic("build status unavailable")
				}
				must(json.Unmarshal(b, &results[i]))
				if results[i]["state"] == "running" {
					done = false
				} else if results[i]["state"] != "passed" {
					save("build-failure.json", results)
					panic("template build failed")
				}
			}
			if done {
				break
			}
			if time.Since(start) > 100*time.Second {
				panic("build cohort timeout")
			}
			time.Sleep(time.Second)
		}
		report["build_rounds"] = append(report["build_rounds"].([]any), map[string]any{"concurrent": n, "seconds": time.Since(start).Seconds(), "guests": results})
		save("result.json", report)
		event("build-passed", n)
	}
	report["completed"] = true
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Println("test stopped:", e)
		os.Exit(1)
	}
}
