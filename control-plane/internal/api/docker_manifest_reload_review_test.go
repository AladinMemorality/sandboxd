package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
)

// Independent review: a prior acknowledgement cannot stand in for an actual
// running container, even if the durable row/socket still report old success.
func TestDockerManifestReloadReviewStaleAcknowledgementNeedsLiveContainer(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%v", missing), func(t *testing.T) {
			f := newDockerReloadFixture(t)
			if code := f.call(`{"reload_manifest":true}`, cfgTenant); code != 200 {
				t.Fatalf("initial activation HTTP %d", code)
			}
			before := f.read(t)
			stale := before
			stale.Container.State.Running = false
			stale.Container.State.Status = "exited"
			if missing {
				stale.Container.ID = ""
			}
			f.write(t, stale)
			if code := f.call(`{"reload_manifest":true}`, cfgTenant); code != 200 {
				t.Fatalf("recovery activation HTTP %d", code)
			}
			after := f.read(t)
			if after.Recreates != before.Recreates+1 || after.Starts != before.Starts+1 || !after.Container.State.Running {
				t.Fatalf("stale acknowledgement skipped actual recovery: before=%+v after=%+v", before, after)
			}
			if after.Container.ID == before.Container.ID {
				t.Fatal("recovery did not create a live container")
			}
		})
	}
}

func TestDockerManifestReloadReviewForeignOwnerNeverInspectsDocker(t *testing.T) {
	f := newDockerReloadFixture(t)
	marker := filepath.Join(f.root, "forbidden-docker-call")
	bin := filepath.Join(f.root, "must-not-run")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf invoked > '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.s.Docker = &docker.Client{Bin: bin}
	if code := f.call(`{"reload_manifest":true}`, cfgTenant+"-foreign"); code != 404 {
		t.Fatalf("foreign owner HTTP %d", code)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("foreign owner reached Docker command: %v", err)
	}
	state := f.read(t)
	if state.Stops != 0 || state.Removes != 0 || state.Recreates != 0 || !state.Container.State.Running {
		t.Fatal("foreign owner changed the live sandbox")
	}
}
