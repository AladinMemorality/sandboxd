package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceBriefUsesCurrentInstalledFacts(t *testing.T) {
	root := t.TempDir()
	put := func(name, value string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	put("package.json", `{"dependencies":{"react":"^18","tailwindcss":"^4","next":"^16"},"scripts":{"dev":"SECRET=private vite","build":"tsc"}}`)
	put("node_modules/react/package.json", `{}`)
	put("node_modules/tailwindcss/package.json", `{}`)
	put("src/App.tsx", "private tenant content")
	put("src/components/ui/button.tsx", "export { Button, buttonVariants }")
	brief := workspaceBrief(root, t.TempDir())
	for _, want := range []string{"src/App.tsx", `"react":"^18"`, `"tailwindcss":"^4"`, `"Button"`, `"build","dev"`} {
		if !strings.Contains(brief, want) {
			t.Errorf("missing %s in %s", want, brief)
		}
	}
	for _, unwanted := range []string{"private", `"next"`, "react_dom_testing"} {
		if strings.Contains(brief, unwanted) {
			t.Errorf("leaked/unverified fact: %s", unwanted)
		}
	}
	put("src/components/ui/button.tsx", "export { NewButton }")
	if b := workspaceBrief(root, "missing"); strings.Contains(b, `"Button"`) || !strings.Contains(b, `"NewButton"`) {
		t.Fatal("inventory reused stale component API")
	}
}

func TestWorkspaceBriefSkipsExternalLinksAndOversizeFiles(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte(`{"dependencies":{"react":"secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "BRIEF.md"), []byte(strings.Repeat("x", 33000)), 0644); err != nil {
		t.Fatal(err)
	}
	brief := workspaceBrief(root, t.TempDir())
	if strings.Contains(brief, "secret") || strings.Contains(brief, "BRIEF.md") || strings.Contains(brief, "package.json") {
		t.Fatal(brief)
	}
}
