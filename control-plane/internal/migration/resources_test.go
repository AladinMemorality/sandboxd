package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
)

func resourceFixtureContainer(id string) *docker.ContainerJSON {
	c := &docker.ContainerJSON{ID: id}
	c.HostConfig.Memory = 2 << 30
	c.HostConfig.NanoCPUs = 2_000_000_000
	return c
}
func TestSourceResourcesRejectUnknownAndPreserveFractionalCPU(t *testing.T) {
	c := resourceFixtureContainer("source")
	limits, e := sourceResources(c)
	if e != nil || limits != (ResourceLimits{2000, 2 << 30}) {
		t.Fatal(limits, e)
	}
	c.HostConfig.NanoCPUs = 0
	c.HostConfig.CPUQuota = 150001
	c.HostConfig.CPUPeriod = 100000
	limits, e = sourceResources(c)
	if e != nil || limits.CPUMilli != 1501 {
		t.Fatal("fractional CPU rounded down", limits, e)
	}
	c.HostConfig.CPUQuota = -1
	if _, e = sourceResources(c); e == nil {
		t.Fatal("unlimited CPU silently bounded")
	}
	c = resourceFixtureContainer("source")
	c.HostConfig.Memory = 0
	if _, e = sourceResources(c); e == nil {
		t.Fatal("unlimited memory silently bounded")
	}
}
func TestMigrationResourceContractRejectsDowngradeDriftAndUnverifiedGuest(t *testing.T) {
	engine, f, id, _ := fixture(t)
	ctx := context.Background()
	m, e := engine.Store.GetRuntimeMigration(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	m.Source.ContainerID = sql.NullString{String: "source", Valid: true}
	c := resourceFixtureContainer("source")
	f.TemplateResources = map[string]ResourceLimits{m.Binding.TemplateID: {1000, 1 << 30}}
	if e = f.preserveSourceResources(m, c); e == nil {
		t.Fatal("half-sized template accepted")
	}
	f.TemplateResources[m.Binding.TemplateID] = ResourceLimits{2000, 2 << 30}
	if e = f.preserveSourceResources(m, c); e != nil {
		t.Fatal(e)
	}
	if e = f.preserveSourceResources(m, c); e != nil {
		t.Fatal("idempotent resource plan failed", e)
	}
	c.HostConfig.Memory = 3 << 30
	if e = f.preserveSourceResources(m, c); e == nil {
		t.Fatal("source resource change ignored")
	}
	c.HostConfig.Memory = 2 << 30
	f.TemplateResources[m.Binding.TemplateID] = ResourceLimits{3000, 3 << 30}
	if _, e = f.readResourceContract(m); e == nil {
		t.Fatal("resource contract changed during resume")
	}
	f.TemplateResources[m.Binding.TemplateID] = ResourceLimits{2000, 2 << 30}
	m.Binding.RuntimeID = "target"
	cpu, memory := 1, 1024
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/sandboxes/target" {
			t.Errorf("unexpected provider mutation %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"sandboxID": "target", "templateID": m.Binding.TemplateID, "cpuCount": cpu, "memoryMB": memory})
	}))
	defer api.Close()
	f.Cube, e = cube.New(cube.Config{APIURL: api.URL, APIKey: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.verifyTargetResources(ctx, m); e == nil {
		t.Fatal("actual undersized guest accepted")
	}
	cpu, memory = 2, 2048
	if e = f.verifyTargetResources(ctx, m); e != nil {
		t.Fatal(e)
	}
	cpu, memory = 0, 0
	if e = f.verifyTargetResources(ctx, m); e == nil {
		t.Fatal("provider omitted resource metadata")
	}
	path, e := f.resourceContractPath(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = f.verifyTargetResources(ctx, m); e == nil {
		t.Fatal("resume without durable resource plan accepted")
	}
}
func TestMigrationRejectsResourceDowngradeBeforeDockerStop(t *testing.T) {
	engine, f, id, _ := fixture(t)
	ctx := context.Background()
	m, e := engine.Store.GetRuntimeMigration(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	m.Source.ContainerID = sql.NullString{String: "source", Valid: true}
	root := t.TempDir()
	home := filepath.Join(root, id)
	if e = os.MkdirAll(filepath.Join(home, "workspace", "app"), 0755); e != nil {
		t.Fatal(e)
	}
	f.WorkspaceRoot = root
	f.TemplateResources = map[string]ResourceLimits{m.Binding.TemplateID: {1000, 1 << 30}}
	c := resourceFixtureContainer("source")
	c.State.Running = true
	c.Config.Labels = map[string]string{"sandboxd.managed": "true"}
	c.Mounts = append(c.Mounts, struct {
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	}{home, "/home/sandbox"})
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "inspect.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(root, "docker")
	if e = os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in inspect) cat \"$(dirname \"$0\")/inspect.json\" ;; stop) touch \"$(dirname \"$0\")/stop-called\" ;; *) exit 91 ;; esac\n"), 0700); e != nil {
		t.Fatal(e)
	}
	f.Docker = &docker.Client{Bin: script}
	if e = f.OfflineBackend.StopSource(ctx, m); e == nil || !strings.Contains(e.Error(), "reduce") {
		t.Fatalf("expected capacity rejection: %v", e)
	}
	if _, e = os.Stat(filepath.Join(root, "stop-called")); !os.IsNotExist(e) {
		t.Fatal("source was stopped before capacity validation")
	}
}
func TestTemplateResourcesReaderRejectsUnknownFieldsAndInvalidUnits(t *testing.T) {
	for _, raw := range []string{`{"tpl":{"cpu_milli":0,"memory_bytes":2147483648}}`, `{"tpl":{"cpu_milli":2000,"memory_mb":2048}}`, `{"tpl":{"cpu_milli":2000,"memory_bytes":2147483648}} {}`} {
		path := filepath.Join(t.TempDir(), "resources.json")
		if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := ReadTemplateResources(path); e == nil {
			t.Fatal("invalid resource map accepted")
		}
	}
}

func TestHistoricalShortContainerIDPinsFullIdentity(t *testing.T) {
	engine, f, id, _ := fixture(t)
	m, err := engine.Store.GetRuntimeMigration(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	full := strings.Repeat("a", 64)
	m.Source.ContainerID = sql.NullString{String: full[:12], Valid: true}
	f.TemplateResources = map[string]ResourceLimits{m.Binding.TemplateID: {2000, 2 << 30}}
	inspected := resourceFixtureContainer(full)
	if err = f.preserveSourceResources(m, inspected); err != nil {
		t.Fatal(err)
	}
	contract, err := f.readResourceContract(m)
	if err != nil || contract.SourceContainerID != full {
		t.Fatal("full immutable identity not pinned", err)
	}
	f.TemplateResources = nil
	if err = f.verifyPinnedSourceIdentity(m, inspected); err != nil {
		t.Fatal("rollback depends on current template map", err)
	}
	f.TemplateResources = map[string]ResourceLimits{m.Binding.TemplateID: {2000, 2 << 30}}
	inspected.ID = full[:12] + strings.Repeat("b", 52)
	if err = f.verifyPinnedSourceIdentity(m, inspected); err == nil {
		t.Fatal("rollback accepted changed full source identity")
	}
	if err = f.preserveSourceResources(m, inspected); err == nil {
		t.Fatal("different resolved container accepted after pinning")
	}
	for _, recorded := range []string{"named-container", full[:11], strings.ToUpper(full[:12]), strings.Repeat("b", 12)} {
		if sourceContainerIdentityMatches(recorded, full) {
			t.Errorf("unsafe identity accepted: %q", recorded)
		}
	}
}
