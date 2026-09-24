package main

import (
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestRevisionIsTheSuccessfullyParsedBootSource(t *testing.T) {
	root := t.TempDir()
	defaults := Defaults{WebCommand: "node server.mjs", WebPort: 3000}
	absent, err := LoadManifest(root, defaults)
	if err != nil || absent.SourceDigest != runtime.ManifestSourceDigest(nil, false) {
		t.Fatal("absent default manifest identity missing")
	}
	source := []byte("version: 1\nweb:\n  command: node server.mjs\n  port: 3000\n")
	if err := os.WriteFile(filepath.Join(root, ManifestFile), source, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(root, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestFile), []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if loaded.SourceDigest != runtime.ManifestSourceDigest(source, true) {
		t.Fatal("digest did not bind parsed boot bytes")
	}
	invalid, err := LoadManifest(root, defaults)
	if err == nil || invalid.SourceDigest != "" {
		t.Fatal("invalid fallback must never acknowledge requested source")
	}
}
