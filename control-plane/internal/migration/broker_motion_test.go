package migration

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestMigrationMotionUsesActualJournalPhaseAndFrozenApp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	const appID = "01M3CKN983PFRGMD711PCEPDFD"
	const id = "owned-motion-runtime"
	file := filepath.Join(t.TempDir(), "journal.db")
	st, err := store.Open(ctx, "file:"+file+"?_fk=1", "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateApp(ctx, &store.App{ID: appID, OwnerToken: "owner", Name: "owned fixture"}); err != nil {
		t.Fatal(err)
	}
	if err = st.Create(ctx, &store.Sandbox{ID: id, AppID: sql.NullString{String: appID, Valid: true}, Status: "stopped", Visibility: "private", Image: "retained-docker", Ports: []int{3000}}); err != nil {
		t.Fatal(err)
	}
	if err = st.BeginRuntimeMigration(ctx, id, "react-vite", "reviewed-template", "cube.test"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", "file:"+file+"?_fk=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Only this disposable fixture DB is advanced to a acknowledged owned target.
	if _, err = db.ExecContext(ctx, "UPDATE runtime_migration SET phase='staged',runtime_id=?,token_ciphertext=?,token_nonce=? WHERE sandbox_id=?", "fixture-target", []byte("encrypted-target"), []byte("nonce"), id); err != nil {
		t.Fatal(err)
	}
	journal, err := st.GetRuntimeMigration(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	policy := egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}
	broker, err := NewMigrationBrokerWithOptions(ctx, policy, MigrationBrokerOptions{MotionStudioAppID: appID, Journal: st})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	entered := make(chan struct{}, 1)
	ended := make(chan struct{}, 1)
	broker.motion = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := egress.SourceIdentity(r.Context())
		if !ok || !broker.authorizeMotion(r.Context(), identity, appID) {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.URL.Path == "/api/status" {
			entered <- struct{}{}
			<-r.Context().Done()
			ended <- struct{}{}
			return
		}
		io.WriteString(w, "owned-projects")
	})
	newGuest := func() (*egress.Guest, *runtime.Client, *httptest.Server) {
		t.Helper()
		token := strings.Repeat("a", 64)
		g, e := egress.NewGuest(egress.GuestOptions{Authenticate: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+token }})
		if e != nil {
			t.Fatal(e)
		}
		control := httptest.NewServer(g.ChannelHandler())
		server := httptest.NewServer(g.ServiceHandler("motion"))
		t.Cleanup(control.Close)
		t.Cleanup(server.Close)
		t.Cleanup(func() { g.Close() })
		client, e := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: control.URL, Token: token})
		if e != nil {
			t.Fatal(e)
		}
		return g, client, server
	}
	guest, client, server := newGuest()
	if err = broker.Attach(ctx, journal, client); err != nil {
		t.Fatal(err)
	}
	if err = guest.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	request := func(server *httptest.Server, path string) (*http.Response, error) {
		r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
		r.Header.Set("Authorization", "Bearer worker-token")
		return server.Client().Do(r)
	}
	response, err := request(server, "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "owned-projects" {
		t.Fatal("journal owner unavailable", response.StatusCode, err)
	}
	env := map[string]string{"STUDIO_WORKER_URL": "http://172.19.0.1:8332", "STUDIO_WORKER_KEY": "worker-token", "APP_ORIGIN": "https://stable.preview"}
	if _, err = broker.motionEnvironment(ctx, journal, &runtime.Status{}, env); err == nil {
		t.Fatal("old target capability accepted")
	}
	target, err := broker.motionEnvironment(ctx, journal, &runtime.Status{Capabilities: []string{runtime.MotionWorkerCapability}}, env)
	if err != nil || target["STUDIO_WORKER_URL"] != runtime.MotionWorkerURL || env["STUDIO_WORKER_URL"] != "http://172.19.0.1:8332" {
		t.Fatal("target overlay/rollback contract", err)
	}
	other, otherClient, otherServer := newGuest()
	foreign := *journal
	foreign.SandboxID = "sibling"
	foreign.Source.AppID = sql.NullString{String: "01M3CKN983PFRGMD711PCEPDFA", Valid: true}
	if err = broker.Attach(ctx, &foreign, otherClient); err != nil {
		t.Fatal(err)
	}
	if err = other.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	response, err = request(otherServer, "/api/projects")
	if err == nil {
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("sibling inherited named worker")
		}
	}
	pending := make(chan struct{})
	go func() {
		defer close(pending)
		resp, _ := request(server, "/api/status")
		if resp != nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("active request not reached")
	}
	oldIdentity := egress.Identity{SandboxID: id, Generation: migrationChannelGeneration(journal)}
	if _, err = db.ExecContext(ctx, "UPDATE runtime_migration SET phase='imported' WHERE sandbox_id=?", id); err != nil {
		t.Fatal(err)
	}
	if broker.authorizeMotion(ctx, oldIdentity, appID) {
		t.Fatal("phase drift retained authorization")
	}
	journal, err = st.GetRuntimeMigration(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = broker.Attach(ctx, journal, client); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("old generation not cancelled")
	}
	select {
	case <-pending:
	case <-ctx.Done():
		t.Fatal("old request leaked")
	}
	broker.Detach(id)
	if broker.authorizeMotion(ctx, egress.Identity{SandboxID: id, Generation: migrationChannelGeneration(journal)}, appID) {
		t.Fatal("detached capability retained")
	}
	if _, err = db.ExecContext(ctx, "UPDATE runtime_migration SET phase='complete' WHERE sandbox_id=?", id); err != nil {
		t.Fatal(err)
	}
	journal, err = st.GetRuntimeMigration(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// Recovery/rollback may still attach a generic deny-all broker; it receives
	// no named worker capability and must not require a new service admission.
	if err = broker.Attach(ctx, journal, client); err != nil {
		t.Fatal("rollback attach blocked", err)
	}
	if broker.authorizeMotion(ctx, egress.Identity{SandboxID: id, Generation: migrationChannelGeneration(journal)}, appID) {
		t.Fatal("completed journal retained worker")
	}
	row, err := st.Get(ctx, id)
	if err != nil || row.RuntimeProvider != "docker" {
		t.Fatal("broker performed provider switch")
	}
}
func TestMigrationMotionConfigurationFailsClosed(t *testing.T) {
	policy := egress.Policy{ProtectedPrefixes: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}}
	if b, e := NewMigrationBrokerWithOptions(context.Background(), policy, MigrationBrokerOptions{MotionStudioAppID: "01M3CKN983PFRGMD711PCEPDFD"}); e == nil {
		b.Close()
		t.Fatal("missing authoritative journal accepted")
	}
}
