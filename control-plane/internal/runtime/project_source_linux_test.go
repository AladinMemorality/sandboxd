package runtime

import (
	"context"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/projectsource"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectExportSkipsDependencyTreeBeforeReading(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "index.html"), []byte("private source"), 0600)
	os.Mkdir(filepath.Join(root, "node_modules"), 0700)
	// A skipped dependency tree may contain links and files exceeding archive
	// limits; no dependency bytes should be opened, validated or compressed.
	os.Symlink("/does-not-exist", filepath.Join(root, "node_modules", "link"))
	huge, e := os.Create(filepath.Join(root, "node_modules", "large"))
	if e != nil {
		t.Fatal(e)
	}
	huge.Truncate(projectsource.MaxBytes + 1)
	huge.Close()
	r := projectsource.Recipe{Version: 1, RuntimeImage: "example/runtime@sha256:" + strings.Repeat("a", 64), PackageManager: "none", Start: []string{"python3", "-m", "http.server", "3000"}, Port: 3000, HealthPath: "/"}
	source, m, e := ExportProjectSource(context.Background(), root, r)
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Files) != 1 || len(m.Excluded) != 1 || m.Excluded[0].Path != "node_modules" {
		t.Fatalf("inventory: %+v", m)
	}
	if e = projectsource.Verify(source, m); e != nil {
		t.Fatal(e)
	}
	os.Symlink("/etc/passwd", filepath.Join(root, "escape"))
	if _, _, e = ExportProjectSource(context.Background(), root, r); e == nil {
		t.Fatal("source escape accepted")
	}
}
