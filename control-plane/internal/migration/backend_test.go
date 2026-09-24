package migration

import (
	"os"
	"path/filepath"
	"testing"

	"context"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func TestMigrationRejectsLinkedAncestryAndWrongOwnerMount(t *testing.T) {
	root := t.TempDir()
	id := "owned-sandbox"
	home := filepath.Join(root, id)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "app"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "workspace")); err != nil {
		t.Fatal(err)
	}
	backend := &OfflineBackend{WorkspaceRoot: root}
	m := &store.RuntimeMigration{SandboxID: id, Source: store.Sandbox{WorkspaceMnt: home}}
	if _, err := backend.sourceRoot(m); err == nil {
		t.Fatal("tenant ancestor link read host files")
	}
	inspected := &docker.ContainerJSON{}
	inspected.Config.Labels = map[string]string{"sandboxd.managed": "true"}
	inspected.Mounts = append(inspected.Mounts, struct {
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	}{Source: filepath.Join(root, "another-owner"), Destination: "/home/sandbox"})
	if err := backend.validateSourceContainer(m, inspected); err == nil {
		t.Fatal("another owner's source container accepted")
	}
	inspected.Mounts[0].Source = home
	if err := backend.validateSourceContainer(m, inspected); err != nil {
		t.Fatal(err)
	}
}

func TestGuestHistoryCannotAddUnrequestedTask(t *testing.T) {
	root := t.TempDir()
	ids := []string{ulid.Make().String(), ulid.Make().String()}
	for _, id := range ids {
		directory := filepath.Join(root, id)
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "events.jsonl"), []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "result.json"), []byte(fmt.Sprintf(`{"id":%q,"status":"succeeded"}`, id)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := runtime.ExportPrivateTaskHistory(context.Background(), root, ids)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateHistoryScope(archive, ids[:1]); err == nil {
		t.Fatal("unrequested task from guest accepted")
	}
	if err = validateHistoryScope(archive, ids); err != nil {
		t.Fatal(err)
	}
}
