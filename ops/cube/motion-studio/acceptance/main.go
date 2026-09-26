// Build this operator-only source inside control-plane/cmd/motion-acceptance.
// The maintenance coordinator owns traffic/writer fences and the four outer
// locks. This process independently requires the native exclusive DB guard.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
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
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/migration"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const database = "/var/lib/sandboxd/state/sandboxd.db"
const template = "tpl-c0c9813b42db46898f7ddd9f"
const proxy = "http://127.0.0.1:20080"
const domain = "cube.app"

type config struct {
	Directory   string               `json:"directory"`
	Migrations  string               `json:"migrations"`
	ProxySource string               `json:"proxy_source"`
	Probe       string               `json:"probe"`
	Video       string               `json:"video"`
	APIKey      string               `json:"api_key"`
	WorkerKey   string               `json:"worker_key"`
	Admission   cube.AdmissionConfig `json:"admission"`
}
type guest struct {
	App, ID, Token string
	Remote         *cube.Sandbox
	Client         *rt.Client
	Journal        *store.RuntimeMigration
}
type runner struct {
	c                  config
	canonical, journal *store.Store
	cipher             *secrets.Cipher
	cube               *cube.Client
	broker             *migration.MigrationBroker
	marker             string
	guests             []*guest
}

func require(err error) {
	if err != nil {
		panic(err)
	}
}
func random() string {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	require(e)
	return hex.EncodeToString(b)
}
func receipt(directory, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	parent, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
func (r *runner) save(name string, value any) { require(receipt(r.c.Directory, name, value)) }
func freshStore(ctx context.Context, file, migrations string) (*store.Store, error) {
	return store.Open(ctx, "file:"+file+"?_journal=WAL&_busy_timeout=5000&_fk=1", migrations)
}
func (r *runner) encrypted(g *guest) ([]byte, []byte, error) {
	traffic := ""
	if g.Remote != nil {
		traffic = g.Remote.TrafficAccessToken
	}
	raw, err := json.Marshal(map[string]string{"supervisor_token": g.Token, "traffic_access_token": traffic})
	if err != nil {
		return nil, nil, err
	}
	return r.cipher.Seal(raw)
}
func (r *runner) create(ctx context.Context, g *guest, label string) {
	r.guests = append(r.guests, g)
	require(r.journal.CreateApp(ctx, &store.App{ID: g.App, OwnerToken: r.marker, Name: "Owned Motion acceptance " + label}))
	require(r.journal.Create(ctx, &store.Sandbox{ID: g.ID, AppID: sql.NullString{String: g.App, Valid: true}, RuntimeProvider: "docker", Status: "stopped", Visibility: "private", Image: "owned-fixture-no-docker-container", Ports: []int{3000}}))
	require(r.journal.BeginRuntimeMigration(ctx, g.ID, "react-vite", template, domain))
	require(r.journal.AdvanceRuntimeMigration(ctx, g.ID, "planned", "quiesced", ""))
	require(r.journal.AdvanceRuntimeMigration(ctx, g.ID, "quiesced", "archived", ""))
	ciphertext, nonce, e := r.encrypted(g)
	require(e)
	require(r.journal.PrepareMigrationTargetCredential(ctx, g.ID, ciphertext, nonce))
	r.save(label+"-create-intent.json", map[string]any{"app_id": g.App, "sandbox_id": g.ID, "template_id": template, "marker": r.marker})
	network, e := cube.OperatorEgressPolicy("")
	require(e)
	g.Remote, e = r.cube.Create(ctx, cube.CreateRequest{TemplateID: template, TimeoutSeconds: 1800, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": g.Token}, Metadata: map[string]string{"sandboxd_id": g.ID, "sandboxd_app_id": g.App, "owned_motion_acceptance": r.marker}, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: network})
	require(e)
	if g.Remote.Domain != domain || g.Remote.TemplateID != template {
		panic("unexpected created target domain or template")
	}
	r.save(label+"-created.json", map[string]string{"runtime_id": g.Remote.SandboxID, "app_id": g.App, "sandbox_id": g.ID, "template_id": template})
	ciphertext, nonce, e = r.encrypted(g)
	require(e)
	require(r.journal.SaveMigrationTarget(ctx, g.ID, &store.RuntimeBinding{RuntimeID: g.Remote.SandboxID, TokenCiphertext: ciphertext, TokenNonce: nonce}))
	g.Journal, e = r.journal.GetRuntimeMigration(ctx, g.ID)
	require(e)
	g.Client, e = rt.NewRemoteClient(rt.RemoteConfig{BaseURL: proxy, Host: "3031-" + g.Remote.SandboxID + "." + domain, Token: g.Token, TrafficAccessToken: g.Remote.TrafficAccessToken})
	require(e)
	var status *rt.Status
	for ctx.Err() == nil {
		status, e = g.Client.Status(ctx)
		if e == nil {
			break
		}
		select {
		case <-ctx.Done():
			require(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	require(e)
	found := false
	for _, capability := range status.Capabilities {
		if capability == rt.MotionWorkerCapability {
			found = true
		}
	}
	if !found {
		panic("actual guest lacks Motion capability")
	}
	r.save(label+"-capability.json", map[string]any{"motion_worker_v1": true})
	attach, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	require(r.broker.Attach(attach, g.Journal, g.Client))
	require(g.Client.QuiesceWorkspace(ctx))
	before, e := g.Client.Status(ctx)
	require(e)
	source, e := os.ReadFile(r.c.ProxySource)
	require(e)
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	for name, body := range map[string][]byte{"proxy.mjs": source, "sandbox.yaml": []byte("version: 1\nweb:\n  command: \"node proxy.mjs\"\n  port: 3000\n  health_path: \"/api/health\"\nbuild:\n  command: \"\"\n")} {
		h := &zip.FileHeader{Name: name}
		h.SetMode(0644)
		f, e := z.CreateHeader(h)
		require(e)
		_, e = f.Write(body)
		require(e)
	}
	require(z.Close())
	require(g.Client.ImportPrivateWorkspace(ctx, archive.Bytes()))
	// Both import and config return before reexec. Wait for authenticated
	// acknowledgement of each generation instead of racing the old supervisor.
	waitStatus(ctx, g.Client.Status, func(s *rt.Status) bool {
		return !s.Runtimed.BootedAt.Equal(before.Runtimed.BootedAt)
	})
	r.save(label+"-imported.json", map[string]bool{"new_supervisor_generation": true})
	require(g.Client.ApplyAppConfig(ctx, rt.AppConfigRequest{Revision: r.marker, Env: map[string]string{"STUDIO_WORKER_URL": rt.MotionWorkerURL, "STUDIO_WORKER_KEY": r.c.WorkerKey, "APP_ORIGIN": "https://cube-motion-acceptance.invalid"}}))
	waitStatus(ctx, g.Client.Status, func(s *rt.Status) bool { return s.AppConfigRevision == r.marker })
	r.save(label+"-configured.json", map[string]bool{"revision_acknowledged": true})
	require(g.Client.ResumeWorkspace(ctx))
	for ctx.Err() == nil {
		status, e = g.Client.Status(ctx)
		if e == nil && status.Preview.Status == rt.PreviewReady {
			break
		}
		select {
		case <-ctx.Done():
			require(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	require(ctx.Err())
	r.save(label+"-ready.json", map[string]any{"runtime_id": g.Remote.SandboxID, "actual_capability": true, "app_ready": true})
}

func waitStatus(ctx context.Context, get func(context.Context) (*rt.Status, error), matches func(*rt.Status) bool) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		status, err := get(ctx)
		if err == nil && status != nil && matches(status) {
			return
		}
		select {
		case <-ctx.Done():
			require(ctx.Err())
		case <-time.After(150 * time.Millisecond):
		}
	}
	require(ctx.Err())
}
func (r *runner) status(ctx context.Context, g *guest) int {
	request, e := http.NewRequestWithContext(ctx, "GET", proxy+"/api/status", nil)
	require(e)
	request.Host = "3000-" + g.Remote.SandboxID + "." + domain
	request.Header.Set("cube-traffic-access-token", g.Remote.TrafficAccessToken)
	client := &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, e := client.Do(request)
	require(e)
	defer response.Body.Close()
	_, e = io.Copy(io.Discard, io.LimitReader(response.Body, 16<<20))
	require(e)
	return response.StatusCode
}
func (r *runner) cleanupGuest(g *guest, index int) error {
	// A failed create may have no acknowledged ID. Preserve its durable pending
	// admission, do not guess, repeat the create, or release its reservation.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if g.Remote == nil {
		admission, e := r.canonical.AdmissionLookupKey(ctx, "app:"+g.App)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if admission.State == "deleted" && admission.Charged == 0 {
			return nil
		}
		return errors.New("unacknowledged create requires explicit reconciliation")
	}
	r.broker.Detach(g.ID)
	actual, e := r.cube.Get(ctx, g.Remote.SandboxID)
	if e != nil {
		return e
	}
	if actual.TemplateID != template || actual.Metadata["sandboxd_id"] != g.ID || actual.Metadata["sandboxd_app_id"] != g.App || actual.Metadata["owned_motion_acceptance"] != r.marker {
		return errors.New("owned fixture identity changed; deletion refused")
	}
	if e = receipt(r.c.Directory, fmt.Sprintf("guest-%d-delete-intent.json", index), map[string]string{"runtime_id": actual.SandboxID}); e != nil {
		return e
	}
	if e = r.cube.Delete(ctx, actual.SandboxID); e != nil {
		return e
	}
	_, e = r.cube.Get(ctx, actual.SandboxID)
	var apiError *cube.APIError
	if !errors.As(e, &apiError) || apiError.StatusCode != 404 {
		return errors.New("owned fixture deletion not independently confirmed")
	}
	admission, e := r.canonical.AdmissionLookup(ctx, actual.SandboxID)
	if e != nil {
		return e
	}
	if admission.Charged != 0 || admission.State != "deleted" {
		return errors.New("owned fixture admission not released")
	}
	return receipt(r.c.Directory, fmt.Sprintf("guest-%d-deleted.json", index), map[string]any{"runtime_id": actual.SandboxID, "provider_404": true, "admission_released": true})
}
func (r *runner) execute(ctx context.Context) (err error) {
	defer func() {
		if value := recover(); value != nil {
			message := fmt.Sprint(value)
			if len(message) > 1024 {
				message = message[:1024]
			}
			_ = receipt(r.c.Directory, "failure.PRIVATE.json", map[string]string{"type": fmt.Sprintf("%T", value), "message": message})
			err = errors.New("owned guest acceptance failed; inspect private phase receipts")
		}
		cleanup := true
		for i := len(r.guests) - 1; i >= 0; i-- {
			if e := r.cleanupGuest(r.guests[i], i); e != nil {
				cleanup = false
				err = errors.Join(err, e)
			}
		}
		if e := receipt(r.c.Directory, "guest-cleanup.json", map[string]any{"complete": cleanup, "guests": len(r.guests)}); e != nil {
			err = errors.Join(err, e)
		}
	}()
	owner := &guest{App: ulid.Make().String(), ID: ulid.Make().String(), Token: random()}
	sibling := &guest{App: ulid.Make().String(), ID: ulid.Make().String(), Token: random()}
	var e error
	r.broker, e = migration.NewMigrationBrokerWithOptions(ctx, egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}, migration.MigrationBrokerOptions{MotionStudioAppID: owner.App, Journal: r.journal})
	require(e)
	defer r.broker.Close()
	r.create(ctx, owner, "owner")
	r.create(ctx, sibling, "sibling")
	if status := r.status(ctx, sibling); status != 502 {
		panic("sibling did not receive missing-capability denial")
	}
	r.save("sibling-denied.json", map[string]int{"status": 502})
	input, e := json.Marshal(map[string]string{"origin": proxy, "host": "3000-" + owner.Remote.SandboxID + "." + domain, "trafficToken": owner.Remote.TrafficAccessToken, "marker": r.marker, "directory": r.c.Directory, "video": r.c.Video})
	require(e)
	command := exec.CommandContext(ctx, "/usr/bin/node", r.c.Probe)
	command.Stdin = bytes.NewReader(input)
	// Do not inherit controller, Cube API, or worker secrets into the HTTP probe.
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	require(command.Run())
	// Alter only the private fixture journal; the live channel's old generation
	// must lose authorization before any reattach with the new phase.
	require(r.journal.AdvanceRuntimeMigration(ctx, owner.ID, "staged", "imported", ""))
	if status := r.status(ctx, owner); status != 403 {
		panic("stale generation remained authorized")
	}
	r.save("stale-generation-denied.json", map[string]int{"status": 403})
	owner.Journal, e = r.journal.GetRuntimeMigration(ctx, owner.ID)
	require(e)
	attach, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	require(r.broker.Attach(attach, owner.Journal, owner.Client))
	if r.status(ctx, owner) != 200 {
		panic("current generation failed to regain authorized access")
	}
	r.broker.Detach(owner.ID)
	if status := r.status(ctx, owner); status < 400 {
		panic("detached guest retained worker access")
	}
	r.save("channel-revoked.json", map[string]bool{"denied": true})
	return nil
}
func run() error {
	if os.Geteuid() != 0 {
		return errors.New("native root operator required")
	}
	if _, e := os.Stat("/.dockerenv"); e == nil {
		return errors.New("must run natively with database users visible")
	}
	if len(os.Args) != 2 {
		return errors.New("usage: motion-acceptance PRIVATE_CONFIG")
	}
	data, e := os.ReadFile(os.Args[1])
	if e != nil {
		return e
	}
	var c config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&c); e != nil {
		return errors.New("invalid private configuration")
	}
	if !strings.HasPrefix(c.Directory, "/opt/baarcha-bench/cube-motion-acceptance-") || filepath.Clean(c.Directory) != c.Directory || c.APIKey == "" || c.WorkerKey == "" {
		return errors.New("invalid acceptance scope")
	}
	if c.Admission.MaxActive != 4 || c.Admission.CPUCount != 2 || c.Admission.MemoryMB != 2048 || c.Admission.WritableDiskMB != 10240 || len(c.Admission.Templates) == 0 {
		return errors.New("reviewed capacity profile required")
	}
	if e = c.Admission.RequireStorageGuard(); e != nil {
		return e
	}
	if e = os.Mkdir(c.Directory, 0700); e != nil {
		return e
	} // A consumed directory is never replayed.
	lock, e := maintenance.Acquire(database, true)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = maintenance.CheckDatabaseUsers(database); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	canonical, e := freshStore(ctx, database, c.Migrations)
	if e != nil {
		return e
	}
	defer canonical.Close()
	journal, e := freshStore(ctx, filepath.Join(c.Directory, "fixture.db"), c.Migrations)
	if e != nil {
		return e
	}
	defer journal.Close()
	cipher, e := secrets.Load("", filepath.Join(c.Directory, "fixture.key"))
	if e != nil {
		return e
	}
	client, e := cube.New(cube.Config{APIURL: "http://127.0.0.1:20300", APIKey: c.APIKey})
	if e != nil {
		return e
	}
	c.Admission.Templates[template] = cube.AdmissionResources{CPUCount: 2, MemoryMB: 2048}
	if e = client.ConfigureAdmission(ctx, canonical, c.Admission); e != nil {
		return e
	}
	r := &runner{c: c, canonical: canonical, journal: journal, cipher: cipher, cube: client, marker: "cube-motion-owned-" + random()[:32]}
	r.save("started.json", map[string]string{"marker": r.marker, "template_id": template, "started_at": time.Now().UTC().Format(time.RFC3339)})
	e = r.execute(ctx)
	if e != nil {
		return e
	}
	return receipt(c.Directory, "guest-acceptance-complete.json", map[string]any{"success": true, "template_id": template, "paid_jobs_submitted": 0})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
