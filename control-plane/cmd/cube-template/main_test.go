package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
)

func TestPrepareRegistryAndPreserveFiles(t *testing.T) {
	for _, p := range preset.List() {
		t.Run(p.ID, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, p.Template)
			if err := os.MkdirAll(source, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "run"), []byte("starter"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("run", filepath.Join(source, "run-link")); err != nil {
				t.Fatal(err)
			}
			destination := t.TempDir()
			if err := prepare(p.ID, root, destination); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(destination, "sandbox.yaml"))
			if err != nil || string(got) != p.Manifest {
				t.Fatalf("manifest: %s %v", got, err)
			}
			link, err := os.Readlink(filepath.Join(destination, "run-link"))
			if err != nil || link != "run" {
				t.Fatalf("link: %s %v", link, err)
			}
			info, err := os.Stat(filepath.Join(destination, "run"))
			if err != nil || info.Mode()&0111 == 0 {
				t.Fatal("executable mode lost", err)
			}
			if err := prepare(p.ID, root, destination); err == nil {
				t.Fatal("overwrote populated destination")
			}
		})
	}
}

func TestPrepareRejectsUnknownAndLinkedDestination(t *testing.T) {
	root := t.TempDir()
	destination := t.TempDir()
	if err := prepare("not-a-preset", root, destination); err == nil {
		t.Fatal("unknown preset accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(destination, link); err != nil {
		t.Fatal(err)
	}
	if err := prepare("react-vite", root, link); err == nil {
		t.Fatal("linked destination accepted")
	}
	entries, _ := os.ReadDir(destination)
	if len(entries) != 0 {
		t.Fatal("modified rejected destination")
	}
}
