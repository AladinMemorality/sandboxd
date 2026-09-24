package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadAppManifestRejectsLinksAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "sandbox.yaml")
	if err := os.Symlink(outside, name); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadAppManifest(root); err == nil {
		t.Fatal("symlink escaped manifest boundary")
	}
	os.Remove(name)
	if err := os.Link(outside, name); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadAppManifest(root); err == nil {
		t.Fatal("hardlink accepted")
	}
	os.Remove(name)
	if err := os.Mkdir(name, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadAppManifest(root); err == nil {
		t.Fatal("directory accepted")
	}
	os.Remove(name)
	if _, present, err := ReadAppManifest(root); err != nil || present {
		t.Fatal("missing manifest must preserve default semantics")
	}
}
