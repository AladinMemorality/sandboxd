package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateWorkspacePreservesOwnerTreeAndCanonicalDigest(t *testing.T) {
	source := t.TempDir()
	for name, body := range map[string]string{".env": "OWNER_SECRET", ".git/config": "tokenless", "data/db.sqlite": "owner data", "node_modules/pkg/index.js": "module"} {
		p := filepath.Join(source, name)
		os.MkdirAll(filepath.Dir(p), 0750)
		os.WriteFile(p, []byte(body), 0640)
	}
	os.MkdirAll(filepath.Join(source, "node_modules/.bin"), 0755)
	if e := os.Symlink("../pkg/index.js", filepath.Join(source, "node_modules/.bin/pkg")); e != nil {
		t.Fatal(e)
	}
	archive, e := ExportPrivateWorkspace(source)
	if e != nil {
		t.Fatal(e)
	}
	before, e := PrivateWorkspaceDigest(archive)
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(t.TempDir(), "app")
	os.Mkdir(root, 0755)
	os.WriteFile(filepath.Join(root, "old"), []byte("old"), 0644)
	if e = InstallPrivateWorkspace(root, archive); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(root, "old")); !os.IsNotExist(e) {
		t.Fatal("replacement merged old tree")
	}
	b, _ := os.ReadFile(filepath.Join(root, ".env"))
	if string(b) != "OWNER_SECRET" {
		t.Fatal("private config lost")
	}
	afterArchive, e := ExportPrivateWorkspace(root)
	if e != nil {
		t.Fatal(e)
	}
	after, e := PrivateWorkspaceDigest(afterArchive)
	if e != nil || after != before {
		t.Fatalf("tree changed: %s %s %v", before, after, e)
	}
	link, e := os.Readlink(filepath.Join(root, "node_modules/.bin/pkg"))
	if e != nil || link != "../pkg/index.js" {
		t.Fatal("symlink lost")
	}
}
func privateTestZip(entries []struct {
	name, body string
	mode       os.FileMode
}) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name}
		h.SetMode(e.mode)
		w, _ := z.CreateHeader(h)
		w.Write([]byte(e.body))
	}
	z.Close()
	return b.Bytes()
}
func TestPrivateWorkspaceRejectsArchiveAttacksBeforeReplacement(t *testing.T) {
	type entry = struct {
		name, body string
		mode       os.FileMode
	}
	cases := [][]entry{{{"../outside", "x", 0644}}, {{"link", "../../outside", os.ModeSymlink | 0777}}, {{"a", "target", os.ModeSymlink | 0777}, {"a/x", "x", 0644}}, {{"a", "x", 0644}, {"a/x", "x", 0644}}, {{"a", "x", 0644}, {"a", "y", 0644}}, {{"pipe", "", os.ModeNamedPipe | 0600}}}
	for _, entries := range cases {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "keep"), []byte("keep"), 0644)
		if e := InstallPrivateWorkspace(root, privateTestZip(entries)); e == nil {
			t.Fatalf("accepted %+v", entries)
		}
		b, _ := os.ReadFile(filepath.Join(root, "keep"))
		if string(b) != "keep" {
			t.Fatal("invalid import changed existing root")
		}
	}
}
func TestPrivateWorkspaceExportRejectsExternalLinks(t *testing.T) {
	for _, link := range []bool{true, false} {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "secret")
		os.WriteFile(outside, []byte("secret"), 0600)
		var e error
		if link {
			e = os.Symlink(outside, filepath.Join(root, "bad"))
		} else {
			e = os.Link(outside, filepath.Join(root, "bad"))
		}
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ExportPrivateWorkspace(root); e == nil {
			t.Fatal("external link exported")
		}
	}
}
func TestPrivateWorkspaceFlattensOnlyProvenOwnerHomeHardlinks(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("fixture needs chown to isolated owner UID1000")
	}
	home := t.TempDir()
	app := filepath.Join(home, "workspace/app")
	cache := filepath.Join(home, ".cache")
	os.MkdirAll(app, 0755)
	os.MkdirAll(cache, 0755)
	p := filepath.Join(cache, "package")
	os.WriteFile(p, []byte("owner cached module"), 0644)
	os.Chown(p, 1000, 1000)
	os.Link(p, filepath.Join(app, "module.js"))
	if _, e := ExportPrivateWorkspace(app); e == nil {
		t.Fatal("unproved links accepted by strict exporter")
	}
	b, e := ExportPrivateWorkspaceOwnerContext(context.Background(), app, home)
	if e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(t.TempDir(), "app")
	os.Mkdir(dest, 0755)
	if e = InstallPrivateWorkspace(dest, b); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "module.js"))
	if string(got) != "owner cached module" {
		t.Fatal("hardlink not flattened")
	}
	outside := filepath.Join(t.TempDir(), "escape")
	os.Link(p, outside)
	if _, e = ExportPrivateWorkspaceOwnerContext(context.Background(), app, home); e == nil {
		t.Fatal("link outside owner home accepted")
	}
}
func TestPrivateWorkspaceLargeExpandedSmallCompressedRoundtrip(t *testing.T) {
	if os.Getenv("CUBE_ARCHIVE_LARGE_TEST") != "1" {
		t.Skip("explicit 300MiB disk/CPU fixture")
	}
	root := t.TempDir()
	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		f, e := os.Create(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = f.Truncate(100 << 20); e != nil {
			t.Fatal(e)
		}
		f.Close()
	}
	b, e := ExportPrivateWorkspace(root)
	if e != nil {
		t.Fatal(e)
	}
	before, e := PrivateWorkspaceDigest(b)
	if e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(t.TempDir(), "app")
	os.Mkdir(dest, 0755)
	if e = InstallPrivateWorkspace(dest, b); e != nil {
		t.Fatal(e)
	}
	afterArchive, e := ExportPrivateWorkspace(dest)
	if e != nil {
		t.Fatal(e)
	}
	after, e := PrivateWorkspaceDigest(afterArchive)
	if e != nil || after != before {
		t.Fatalf("large roundtrip mismatch %v", e)
	}
	t.Logf("expanded_bytes=%d compressed_bytes=%d", 300<<20, len(b))
}
func TestPrivateWorkspaceInterpreterLinksAreNarrowAndNeverDirectories(t *testing.T) {
	for _, target := range []string{"/usr/bin/python3", "/usr/bin/python3.12"} {
		if !privateLink(".venv/bin/python", target) {
			t.Fatal("valid interpreter leaf rejected")
		}
	}
	for _, tt := range [][2]string{{".venv/bin/python", "/usr/bin"}, {".venv/bin/python", "/usr/bin/python3/../../etc/passwd"}, {".venv/bin/python", "/usr/local/bin/python3"}, {".venv/bin/python", "/usr/bin/python3.12/secret"}, {"arbitrary", "/usr/bin/python3"}} {
		if privateLink(tt[0], tt[1]) {
			t.Fatalf("unsafe interpreter link %v", tt)
		}
	}
	type entry = struct {
		name, body string
		mode       os.FileMode
	}
	data := privateTestZip([]entry{{".venv/bin/python", "/usr/bin/python3", os.ModeSymlink | 0777}, {".venv/bin/python/secret", "bad", 0644}})
	if e := ValidatePrivateWorkspaceArchive(data); e == nil {
		t.Fatal("accepted entry below interpreter symlink")
	}
	if PublishedSourcePath(".venv/bin/python") {
		t.Fatal("interpreter exception crossed publication boundary")
	}
}
func TestPrivateWorkspaceRejectsSymlinkedRootAncestors(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.Mkdir(filepath.Join(outside, "app"), 0755)
	os.WriteFile(filepath.Join(outside, "app/secret"), []byte("other owner"), 0600)
	os.Symlink(outside, filepath.Join(root, "workspace"))
	if _, e := ExportPrivateWorkspace(filepath.Join(root, "workspace/app")); e == nil {
		t.Fatal("root parent symlink traversed")
	}
	if _, e := ExportPrivateWorkspaceOwnerContext(context.Background(), filepath.Join(root, "workspace/app"), root); e == nil {
		t.Fatal("owner-aware root parent symlink traversed")
	}
}
func TestUnavailablePrivateOperationsReturnTransportError(t *testing.T) {
	failure := errors.New("unavailable runtime binding")
	c := NewUnavailableClient(failure)
	ctx := context.Background()
	archive := privateTestZip([]struct {
		name, body string
		mode       os.FileMode
	}{{"app.js", "ok", 0644}})
	for name, call := range map[string]func() error{"quiesce": func() error { return c.QuiesceWorkspace(ctx) }, "resume": func() error { return c.ResumeWorkspace(ctx) }, "export": func() error { _, e := c.ExportPrivateWorkspace(ctx); return e }, "private import": func() error { return c.ImportPrivateWorkspace(ctx, archive) }, "source import": func() error { return c.ImportSource(ctx, archive) }, "git import": func() error { return c.ImportGitWorkspace(ctx, archive) }} {
		if e := call(); !errors.Is(e, failure) {
			t.Fatalf("%s: %v", name, e)
		}
	}
}

func TestPrivateWorkspaceSingleLinksDoNotInventoryUnrelatedHome(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "workspace/app")
	if err := os.MkdirAll(app, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "index.js"), []byte("owner source"), 0644); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(home, ".cache")
	for i := 0; i < 66; i++ {
		unrelated = filepath.Join(unrelated, "nested")
	}
	if err := os.MkdirAll(unrelated, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := inventoryOwnerLinks(context.Background(), home); err == nil {
		t.Fatal("fixture must reject unrelated home inventory")
	}
	data, err := ExportPrivateWorkspaceOwnerContext(context.Background(), app, home)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateWorkspaceArchive(data); err != nil {
		t.Fatal(err)
	}
}
