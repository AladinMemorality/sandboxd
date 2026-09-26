package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func TestCubeMotionRequiresCurrentPersistedAppAndGeneration(t *testing.T) {
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	ctx := context.Background()
	row, err := s.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	app := row.AppID.String
	identity := egress.Identity{SandboxID: id, Generation: cubeEgressGeneration(binding)}
	if s.authorizeCubeMotionStudio(ctx, identity, app) {
		t.Fatal("disabled service authorized")
	}
	s.cubeEgress = &cubeEgressManager{config: CubeEgressConfig{MotionStudioAppID: app}}
	if err = s.Store.MarkRunning(ctx, id, "", ""); err != nil {
		t.Fatal(err)
	}
	if !s.authorizeCubeMotionStudio(ctx, identity, app) {
		t.Fatal("exact app binding denied")
	}
	for _, attempt := range []struct {
		id  egress.Identity
		app string
	}{{identity, "other-app"}, {egress.Identity{SandboxID: "missing", Generation: identity.Generation}, app}, {egress.Identity{SandboxID: id, Generation: "previous"}, app}, {egress.Identity{}, app}} {
		if s.authorizeCubeMotionStudio(ctx, attempt.id, attempt.app) {
			t.Fatal("foreign app/generation authorized")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if s.authorizeCubeMotionStudio(cancelled, identity, app) {
		t.Fatal("cancelled capability authorized")
	}
	if err = s.Store.MarkStoppedAt(ctx, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s.authorizeCubeMotionStudio(ctx, identity, app) {
		t.Fatal("stopped binding authorized")
	}
	if _, present := s.cubeNamedServices()["motion"]; present {
		t.Fatal("unconfigured handler registered")
	}
}

func TestCubeMotionConfigOverlayPreservesEncryptedRollbackValues(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	revision := ""
	capability := false
	failApply := false
	applies := 0
	var last runtime.AppConfigRequest
	s, id, _ := cubeTaskFixture(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/config" {
			if failApply {
				w.WriteHeader(503)
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&last); err != nil {
				t.Error(err)
			}
			revision = last.Revision
			applies++
			w.WriteHeader(202)
			return
		}
		status := runtime.Status{AppConfigRevision: revision}
		if capability {
			status.Capabilities = []string{runtime.MotionWorkerCapability}
		}
		json.NewEncoder(w).Encode(status)
	})
	row, err := s.Store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	appID := row.AppID.String
	for k, v := range map[string]string{"STUDIO_WORKER_URL": "http://172.19.0.1:8332", "STUDIO_WORKER_KEY": "fixture-key", "APP_ORIGIN": "https://stable.preview"} {
		body, _ := json.Marshal(map[string]any{"key": k, "value": v, "sensitive": true, "access_policy": "runtime_access"})
		w := cubeRequest(s, "POST", "/v1/apps/"+appID+"/config", string(body), cfgTenant)
		if w.Code != 201 {
			t.Fatal("fixture config rejected", w.Code)
		}
	}
	before, err := s.Store.RuntimeConfigFingerprint(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	s.cubeEgress = &cubeEgressManager{config: CubeEgressConfig{MotionStudioAppID: appID}}
	if err = s.syncCubeAppConfig(ctx, id); err == nil {
		t.Fatal("old template accepted")
	}
	mu.Lock()
	capability = true
	failApply = true
	mu.Unlock()
	if err = s.syncCubeAppConfig(ctx, id); err == nil {
		t.Fatal("failed config marked applied")
	}
	binding, err := s.Store.GetRuntimeBinding(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ConfigAppliedRevision == binding.ConfigRevision {
		t.Fatal("failed overlay acknowledged")
	}
	mu.Lock()
	failApply = false
	mu.Unlock()
	if err = s.syncCubeAppConfig(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = s.syncCubeAppConfig(ctx, id); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if applies != 1 || last.Env["STUDIO_WORKER_URL"] != runtime.MotionWorkerURL || last.Env["STUDIO_WORKER_KEY"] != "fixture-key" || !strings.HasSuffix(revision, ":"+runtime.MotionWorkerCapability) {
		t.Error("overlay or idempotent apply failed")
	}
	mu.Unlock()
	after, err := s.Store.RuntimeConfigFingerprint(ctx, appID)
	if err != nil || before != after {
		t.Fatal("encrypted rollback configuration mutated")
	}
}
