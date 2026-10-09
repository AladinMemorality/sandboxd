// cube-relocate is a root-only coordinator primitive. It is not an HTTP API.
// Run under the shared operator/deployment locks. Bulk export/import is performed
// separately on each worker through S3; only verified receipts reach this tool.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	guest "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type request struct {
	Action, ID, AppID, SandboxID, ExpectedRuntime, TargetWorker, TargetTemplate, Directory, Migrations string
	PackageDownloads, SameProfileReplacement                                                           bool
}
type target struct {
	Relocation      store.CubeRelocation
	SupervisorToken string `json:"supervisor_token"`
	TrafficToken    string `json:"traffic_access_token"`
	Admission       cube.AdmissionRecord
	Runtime         *cube.Sandbox
	Env             map[string]string
}
type proof struct {
	RelocationID, SandboxID, RuntimeID, WorkerID                                      string
	WorkspaceSHA256, HomeSHA256, HistorySHA256                                        string
	WorkspaceVerified, HomeVerified, HistoryVerified, ConfigApplied, ApplicationReady bool
}

func validDiscardTarget(j store.CubeRelocation, t target, actual *cube.Sandbox, binding *store.RuntimeBinding) bool {
	return j.TargetWorker == "vps" && t.Relocation.ID == j.ID && t.Runtime != nil && actual != nil && binding != nil && binding.RuntimeID == j.SourceRuntimeID && t.Runtime.SandboxID != j.SourceRuntimeID && actual.SandboxID == t.Runtime.SandboxID && actual.TemplateID == j.DestinationTemplate() && t.Admission.Key == j.TargetKey && actual.Metadata["sandboxd_id"] == j.SandboxID && actual.Metadata["sandboxd_app_id"] == j.AppID && actual.Metadata["sandboxd_relocation_id"] == j.ID && actual.Metadata["sandboxd_admission_operation"] == t.Admission.Token
}

func validConnectedTargetLease(j store.CubeRelocation, runtime string, a cube.AdmissionRecord) bool {
	return a.Key == j.TargetKey && a.RuntimeID == runtime && runtime != j.SourceRuntimeID && a.TemplateID == j.DestinationTemplate() && a.WorkerID == j.TargetWorker && a.Operation == "connect" && a.State == "active" && a.Charged == 1 && a.Token != ""
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func token() string {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	must(e)
	return hex.EncodeToString(b)
}
func privateRead(path string, v any) {
	i, e := os.Lstat(path)
	must(e)
	if !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() > 256<<10 || i.Sys().(*syscall.Stat_t).Uid != 0 {
		panic("private input required")
	}
	raw, e := os.ReadFile(path)
	must(e)
	must(json.Unmarshal(raw, v))
}
func privateWrite(path string, v any) {
	raw, e := json.Marshal(v)
	must(e)
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	must(e)
	_, e = f.Write(raw)
	must(e)
	must(f.Sync())
	must(f.Close())
}
func main() {
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "relocation operation failed; inspect private journal; no automatic retry or source release")
			os.Exit(1)
		}
	}()
	if os.Geteuid() != 0 {
		panic("root required")
	}
	syscall.Umask(0077)
	var in request
	if len(os.Args) == 2 {
		privateRead(os.Args[1], &in)
	} else {
		raw, e := io.ReadAll(io.LimitReader(os.Stdin, 32769))
		must(e)
		if len(raw) > 32768 {
			panic("request too large")
		}
		must(json.Unmarshal(raw, &in))
	}
	if !filepath.IsAbs(in.Directory) || !filepath.IsAbs(in.Migrations) {
		panic("absolute paths required")
	}
	i, e := os.Lstat(in.Directory)
	must(e)
	if !i.IsDir() || i.Mode().Perm()&0077 != 0 || i.Sys().(*syscall.Stat_t).Uid != 0 {
		panic("private directory required")
	}
	window := 3 * time.Minute
	if in.Action == "channel" {
		window = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), window)
	defer cancel()
	inspected, e := exec.CommandContext(ctx, "docker", "inspect", "src-sandboxd-1").Output()
	must(e)
	var containers []struct {
		Config struct {
			Env        []string
			Entrypoint []string
		}
	}
	must(json.Unmarshal(inspected, &containers))
	if len(containers) != 1 || len(containers[0].Config.Entrypoint) != 1 || containers[0].Config.Entrypoint[0] != "/usr/local/bin/cube-controller" {
		panic("Cube controller required")
	}
	env := map[string]string{}
	for _, v := range containers[0].Config.Env {
		k, val, ok := strings.Cut(v, "=")
		if ok {
			env[k] = val
		}
	}
	db, e := store.Open(ctx, "file:/var/lib/sandboxd/state/sandboxd.db?_journal=WAL&_busy_timeout=5000&_fk=1", in.Migrations)
	must(e)
	defer db.Close()
	if in.Action == "fence" {
		var templates []string
		if in.TargetTemplate != "" {
			templates = []string{in.TargetTemplate}
		}
		var j store.CubeRelocation
		var e error
		if in.SameProfileReplacement {
			j, e = db.BeginCubeSameProfileReplacement(ctx, in.ID, in.SandboxID, in.ExpectedRuntime, in.TargetWorker, in.TargetTemplate)
		} else {
			j, e = db.BeginCubeRelocation(ctx, in.ID, in.SandboxID, in.ExpectedRuntime, in.TargetWorker, templates...)
		}
		must(e)
		privateWrite(filepath.Join(in.Directory, "fenced.json"), j)
		fmt.Println(`{"fenced":true}`)
		return
	}
	if in.Action == "abort" {
		must(db.AbortCubeRelocation(ctx, in.ID))
		fmt.Println(`{"aborted":true}`)
		return
	}
	var j store.CubeRelocation
	if in.Action == "fixture" {
		app, e := db.GetApp(ctx, in.AppID)
		must(e)
		if app.ExternalUserID.String != "operator:relocation-acceptance" || app.RuntimePreset.String != "react-vite" {
			panic("owned fixture app required")
		}
		if _, e = db.CurrentSandboxForApp(ctx, in.AppID); !errors.Is(e, store.ErrNotFound) {
			panic("fixture app already has runtime")
		}
		var templates map[string]string
		must(json.Unmarshal([]byte(env["SANDBOXD_CUBE_TEMPLATES"]), &templates))
		j = store.CubeRelocation{ID: in.ID, SandboxID: in.SandboxID, AppID: in.AppID, TargetWorker: in.TargetWorker, TargetKey: "app:" + in.AppID, TemplateID: templates["react-vite"], Domain: env["SANDBOXD_CUBE_DOMAIN"]}
	} else {
		var phase string
		j, phase, e = db.GetCubeRelocation(ctx, in.ID)
		must(e)
		if phase != "fenced" {
			panic("fenced relocation required")
		}
	}
	var fleet struct{ Workers []cube.FleetWorkerConfig }
	must(json.Unmarshal([]byte(env["SANDBOXD_CUBE_FLEET"]), &fleet))
	var selected *cube.FleetWorkerConfig
	for i := range fleet.Workers {
		if fleet.Workers[i].ID == j.TargetWorker {
			selected = &fleet.Workers[i]
		}
	}
	if selected == nil || selected.Draining {
		panic("target worker unavailable")
	}
	partition, e := db.AdmissionPartition(j.TargetWorker)
	must(e)
	provider, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: env["SANDBOXD_CUBE_API_KEY"]})
	must(e)
	guard, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: env["SANDBOXD_CUBE_API_KEY"]})
	must(e)
	must(guard.ConfigurePlacement("http://10.254.240.1:18089", "cubebox"))
	must(guard.ConfigureAdmission(ctx, partition, selected.Admission))
	if env["SANDBOXD_SECRETS_KEY"] == "" {
		_, e = os.Stat("/var/lib/sandboxd/secrets.key")
		must(e)
	}
	if in.Action == "discard-target" || in.Action == "connect-target" || in.Action == "observe-connected-target" {
		var t target
		privateRead(filepath.Join(in.Directory, "target.PRIVATE.json"), &t)
		if j.TargetWorker != "vps" || t.Relocation.ID != j.ID || t.Runtime == nil || t.Runtime.SandboxID == j.SourceRuntimeID || t.Admission.Token == "" {
			panic("uncommitted VPS target required")
		}
		actual, err := provider.Get(ctx, t.Runtime.SandboxID)
		must(err)
		binding, err := db.GetRuntimeBinding(ctx, j.SandboxID)
		must(err)
		if !validDiscardTarget(j, t, actual, binding) {
			panic("refusing to discard a source or changed target")
		}
		if in.Action == "connect-target" || in.Action == "observe-connected-target" {
			if actual.State != "paused" && actual.State != "running" {
				panic("target state requires reconciliation")
			}
			intent := map[string]string{"runtime_id": actual.SandboxID, "source_retained": j.SourceRuntimeID}
			if in.Action == "connect-target" {
				privateWrite(filepath.Join(in.Directory, "connect-target-intent.json"), intent)
				_, err = guard.Connect(ctx, actual.SandboxID, cube.ConnectRequest{TimeoutSeconds: 3600})
				must(err)
			} else {
				var recorded map[string]string
				privateRead(filepath.Join(in.Directory, "connect-target-intent.json"), &recorded)
				if recorded["runtime_id"] != intent["runtime_id"] || recorded["source_retained"] != intent["source_retained"] {
					panic("connected target intent differs")
				}
			}
			// Connect responses may omit metadata. Re-read authoritative identity;
			// reconciliation only observes an already active exact connect lease.
			connected, err := provider.Get(ctx, actual.SandboxID)
			must(err)
			if connected.State != "running" || !validDiscardTarget(j, t, connected, binding) {
				panic("target connection was not authoritatively verified")
			}
			lease, err := partition.AdmissionLookup(ctx, connected.SandboxID)
			must(err)
			if !validConnectedTargetLease(j, connected.SandboxID, lease) {
				panic("connected target admission differs")
			}
			privateWrite(filepath.Join(in.Directory, "connected-target.PRIVATE.json"), connected)
			fmt.Println(`{"connected_target":true,"source_retained":true}`)
			return
		}
		privateWrite(filepath.Join(in.Directory, "discard-intent.json"), map[string]string{"runtime_id": actual.SandboxID, "source_retained": j.SourceRuntimeID})
		must(guard.Delete(ctx, actual.SandboxID))
		must(db.AbortCubeRelocation(ctx, j.ID))
		fmt.Println(`{"discarded_target":true,"source_retained":true,"relocation_aborted":true}`)
		return
	}
	cipher, e := secrets.Load(env["SANDBOXD_SECRETS_KEY"], "/var/lib/sandboxd/secrets.key")
	must(e)
	if in.Action == "channel" {
		var t target
		privateRead(filepath.Join(in.Directory, "target.PRIVATE.json"), &t)
		if t.Relocation.ID != j.ID || t.Runtime == nil || t.SupervisorToken == "" || t.TrafficToken == "" {
			panic("target channel scope differs")
		}
		policy := egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}
		outbound := "deny-all"
		if in.PackageDownloads {
			if j.TargetWorker != "vps" {
				panic("package recovery is limited to the VPS")
			}
			policy, e = egress.OperatorPolicy(env["SANDBOXD_CUBE_EGRESS_PROTECTED_CIDRS"], env["SANDBOXD_CUBE_EGRESS_PROTECTED_DOMAINS"])
			must(e)
			policy.AllowedHosts = []string{"registry.npmjs.org"}
			policy.Ports = []uint16{443}
			must(policy.Validate())
			outbound = "npm-registry-only"
		}
		origin := "http://10.254.240.2:28080"
		if j.TargetWorker == "vps" {
			origin = "http://127.0.0.1:20080"
		}
		client, e := guest.NewRemoteClient(guest.RemoteConfig{BaseURL: origin, Host: "3031-" + t.Runtime.SandboxID + "." + j.Domain, Token: t.SupervisorToken, TrafficAccessToken: t.TrafficToken})
		must(e)
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
		for ctx.Err() == nil {
			conn, e := client.OpenEgressChannel(ctx)
			if e == nil {
				fmt.Printf("{\"channel_ready\":true,\"outbound\":%q}\n", outbound)
				_ = egress.RunHost(ctx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: j.SandboxID, Generation: t.Runtime.SandboxID}, Policy: policy})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		return
	}
	if in.Action == "create" || in.Action == "fixture" || in.Action == "retry-rejected-create" {
		var t target
		if in.Action == "retry-rejected-create" {
			privateRead(filepath.Join(in.Directory, "create-intent.PRIVATE.json"), &t)
			var rejection rejectedCreate
			privateRead(filepath.Join(in.Directory, "provider-rejection.json"), &rejection)
			reservation, err := partition.AdmissionLookupKey(ctx, j.TargetKey)
			must(err)
			if !validRejectedCreate(j, t, reservation, rejection) {
				panic("unambiguous rejected create required")
			}
			for _, name := range []string{"create-response.PRIVATE.json", "target.PRIVATE.json", "retry-intent.PRIVATE.json"} {
				if _, err := os.Lstat(filepath.Join(in.Directory, name)); !os.IsNotExist(err) {
					panic("create outcome already recorded or retry attempted")
				}
			}
			binding, err := db.GetRuntimeBinding(ctx, j.SandboxID)
			must(err)
			if binding.RuntimeID != j.SourceRuntimeID {
				panic("source binding changed")
			}
			must(noRelocationTarget(ctx, env["SANDBOXD_CUBE_API_KEY"], j, t.Admission.Token))
			privateWrite(filepath.Join(in.Directory, "retry-intent.PRIVATE.json"), t)
		} else {
			if _, e = os.Lstat(filepath.Join(in.Directory, "create-intent.PRIVATE.json")); !os.IsNotExist(e) {
				panic("create intent already exists; reconcile before retry")
			}
			t = target{Relocation: j, SupervisorToken: token(), Env: map[string]string{}}
			entries, err := appenv.For(ctx, db, cipher, j.AppID)
			must(err)
			for _, entry := range entries {
				k, v, ok := strings.Cut(entry, "=")
				if ok {
					t.Env[k] = v
				}
			}
			t.Admission, e = partition.AdmissionBegin(ctx, j.TargetKey, "", j.DestinationTemplate(), "create", token())
			must(e)
			privateWrite(filepath.Join(in.Directory, "create-intent.PRIVATE.json"), t)
		}
		network, e := cube.OperatorEgressPolicy("")
		must(e)
		t.Runtime, e = provider.Create(ctx, cube.CreateRequest{TemplateID: j.DestinationTemplate(), TimeoutSeconds: 3600, DistributionScope: []string{selected.Admission.NodeID}, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": t.SupervisorToken}, Metadata: map[string]string{"sandboxd_id": j.SandboxID, "sandboxd_app_id": j.AppID, "sandboxd_admission_operation": t.Admission.Token, "sandboxd_relocation_id": j.ID}, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: network})
		if e != nil {
			privateWrite(filepath.Join(in.Directory, "create-error.PRIVATE.json"), map[string]string{"error": e.Error()})
		}
		must(e)
		privateWrite(filepath.Join(in.Directory, "create-response.PRIVATE.json"), t)
		actual, e := provider.Get(ctx, t.Runtime.SandboxID)
		must(e)
		if actual.State != "running" || actual.CPUCount != selected.Admission.Templates[j.DestinationTemplate()].CPUCount || actual.MemoryMB != selected.Admission.Templates[j.DestinationTemplate()].MemoryMB || actual.TemplateID != j.DestinationTemplate() || actual.Metadata["sandboxd_id"] != j.SandboxID || actual.Metadata["sandboxd_app_id"] != j.AppID || actual.Metadata["sandboxd_relocation_id"] != j.ID || actual.Metadata["sandboxd_admission_operation"] != t.Admission.Token || t.Runtime.TrafficAccessToken == "" {
			panic("target identity mismatch")
		}
		req, e := http.NewRequestWithContext(ctx, "GET", "http://10.254.240.1:18089/cube/sandbox/info?sandbox_id="+t.Runtime.SandboxID+"&instance_type=cubebox", nil)
		must(e)
		response, e := http.DefaultClient.Do(req)
		must(e)
		defer response.Body.Close()
		var placement struct {
			Ret struct {
				Code int `json:"ret_code"`
			} `json:"ret"`
			Data []struct {
				ID   string `json:"sandbox_id"`
				Node string `json:"host_id"`
			} `json:"data"`
		}
		must(json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&placement))
		if response.StatusCode != 200 || placement.Ret.Code != 200 || len(placement.Data) != 1 || placement.Data[0].ID != t.Runtime.SandboxID || placement.Data[0].Node != selected.Admission.NodeID {
			panic("wrong target placement")
		}
		must(partition.AdmissionFinish(ctx, t.Admission, t.Runtime.SandboxID, "active"))
		t.TrafficToken = t.Runtime.TrafficAccessToken
		privateWrite(filepath.Join(in.Directory, "target.PRIVATE.json"), t)
		if in.Action == "fixture" {
			credentials, e := json.Marshal(map[string]string{"supervisor_token": t.SupervisorToken, "traffic_access_token": t.TrafficToken})
			must(e)
			sealed, nonce, e := cipher.Seal(credentials)
			must(e)
			app, e := db.GetApp(ctx, j.AppID)
			must(e)
			must(db.Create(ctx, &store.Sandbox{ID: j.SandboxID, Status: "stopped", Image: "cube-template:" + j.DestinationTemplate(), RuntimeProvider: "cube", RuntimeBinding: &store.RuntimeBinding{SandboxID: j.SandboxID, Provider: "cube", RuntimeID: t.Runtime.SandboxID, TemplateID: j.DestinationTemplate(), Domain: j.Domain, TokenCiphertext: sealed, TokenNonce: nonce}, AppID: sql.NullString{String: j.AppID, Valid: true}, ExternalUserID: app.ExternalUserID, ExternalProjectID: app.ExternalProjectID, Visibility: "private", IdlePolicy: "sleep", Ports: []int{3000}, WebPort: sql.NullInt64{Int64: 3000, Valid: true}}))
		}
		fmt.Println(`{"created":true,"placement_verified":true}`)
		return
	}
	if in.Action == "commit" {
		var t target
		privateRead(filepath.Join(in.Directory, "target.PRIVATE.json"), &t)
		// Quiescence and the source fence are separate operations. A task may
		// be submitted between export and fencing; its history must not be
		// omitted even if the fenced DB snapshot itself remains unchanged.
		var source struct {
			SandboxID string   `json:"sandbox_id"`
			RuntimeID string   `json:"runtime_id"`
			TaskIDs   []string `json:"task_ids"`
		}
		privateRead(filepath.Join(in.Directory, "export-result.PRIVATE.json"), &source)
		if source.SandboxID != j.SandboxID || source.RuntimeID != j.SourceRuntimeID || len(source.TaskIDs) != j.TaskCount {
			panic("source task snapshot differs from fence")
		}
		seen := map[string]bool{}
		for _, id := range source.TaskIDs {
			task, err := db.GetTask(ctx, id)
			must(err)
			if seen[id] || task.SandboxID != j.SandboxID {
				panic("source task identity differs")
			}
			seen[id] = true
		}
		var p proof
		privateRead(filepath.Join(in.Directory, "verified.json"), &p)
		if t.Relocation.ID != j.ID || t.Runtime == nil || p.RelocationID != j.ID || p.SandboxID != j.SandboxID || p.RuntimeID != t.Runtime.SandboxID || p.WorkerID != j.TargetWorker || !p.WorkspaceVerified || !p.HomeVerified || !p.HistoryVerified || !p.ConfigApplied || !p.ApplicationReady {
			panic("complete verified target required")
		}
		for _, hash := range []string{p.WorkspaceSHA256, p.HomeSHA256, p.HistorySHA256} {
			b, e := hex.DecodeString(hash)
			must(e)
			if len(b) != 32 {
				panic("invalid artifact digest")
			}
		}
		actual, e := provider.Get(ctx, t.Runtime.SandboxID)
		must(e)
		if actual.State != "running" {
			panic("target no longer running")
		}
		targetToken := t.Admission.Token
		if _, err := os.Lstat(filepath.Join(in.Directory, "connected-target.PRIVATE.json")); err == nil {
			var connected cube.Sandbox
			privateRead(filepath.Join(in.Directory, "connected-target.PRIVATE.json"), &connected)
			binding, err := db.GetRuntimeBinding(ctx, j.SandboxID)
			must(err)
			if connected.State != "running" || !validDiscardTarget(j, t, &connected, binding) || !validDiscardTarget(j, t, actual, binding) {
				panic("connected target identity differs")
			}
			lease, err := partition.AdmissionLookup(ctx, actual.SandboxID)
			must(err)
			if !validConnectedTargetLease(j, actual.SandboxID, lease) {
				panic("connected target admission differs")
			}
			// Connect replaces the original create lease. Commit still CASes
			// the exact freshly observed target token; no source lease changes.
			targetToken = lease.Token
		} else if !os.IsNotExist(err) {
			must(err)
		}
		credentials, e := json.Marshal(map[string]string{"supervisor_token": t.SupervisorToken, "traffic_access_token": t.TrafficToken})
		must(e)
		sealed, nonce, e := cipher.Seal(credentials)
		must(e)
		proofRaw, e := json.Marshal(p)
		must(e)
		sha := sha256.Sum256(proofRaw)
		must(db.CommitCubeRelocation(ctx, j.ID, targetToken, hex.EncodeToString(sha[:]), store.RuntimeBinding{SandboxID: j.SandboxID, Provider: "cube", RuntimeID: t.Runtime.SandboxID, TemplateID: j.DestinationTemplate(), Domain: j.Domain, ConfigRevision: j.ConfigRevision, ConfigAppliedRevision: j.ConfigRevision, TokenCiphertext: sealed, TokenNonce: nonce}))
		fmt.Println(`{"committed":true}`)
		return
	}
	panic(errors.New("unknown operation"))
}
