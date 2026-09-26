package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerAuthCannotBeDisabledOnStartupOrReload(t *testing.T) {
	values := map[string]string{"SANDBOXD_API_TOKENS": "platform=fixture", "SANDBOXD_PREVIEW_TOKEN_SECRETS": "v1=" + strings.Repeat("x", 32)}
	get := func(key string) string { return values[key] }
	if _, err := strictAuth(get); err != nil {
		t.Fatal(err)
	}
	values["SANDBOXD_API_AUTH_DISABLED"] = "true"
	if _, err := strictAuth(get); err == nil {
		t.Fatal("auth disabled")
	}
	delete(values, "SANDBOXD_API_AUTH_DISABLED")
	delete(values, "SANDBOXD_API_TOKENS")
	if _, err := strictAuth(get); err == nil {
		t.Fatal("missing service tokens accepted")
	}
}

func TestControllerNeverInitializesMissingOrSymlinkedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if err := requireFile(path); err == nil {
		t.Fatal("missing state accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("state created")
	}
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requireFile(path); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.db")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := requireFile(link); err == nil {
		t.Fatal("symlinked state accepted")
	}
}

func TestRetainedEncryptionKeyIsNeverGeneratedOrOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.key")
	if _, err := retainedCipher("", path); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing key was generated")
	}
	for _, bad := range []string{"", "corrupt"} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := retainedCipher("", path); err == nil {
			t.Fatal("invalid key accepted")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != bad {
			t.Fatal("existing key was replaced")
		}
	}
}
