package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func homeFixture(t *testing.T) (string, HomeManifest) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{".runtimed", "workspace/app", "workspace/assets", ".local/bin", ".claude"} {
		if e := os.MkdirAll(filepath.Join(root, dir), 0755); e != nil {
			t.Fatal(e)
		}
	}
	for name, data := range map[string]string{".bashrc": "custom shell\n", "workspace/app/index.js": "separate app", "workspace/assets/data.txt": "owner sibling", ".local/bin/tool": "owner tool", ".claude/state.json": "private provider history", ".runtimed/token": "fresh control"} {
		if e := os.WriteFile(filepath.Join(root, name), []byte(data), 0644); e != nil {
			t.Fatal(e)
		}
	}
	return root, HomeManifest{Version: 1, Entries: []HomeManifestEntry{{Path: ".runtimed", Disposition: "separate"}, {Path: "workspace/app", Disposition: "separate"}, {Path: "workspace/assets", Disposition: "preserve"}, {Path: ".local/bin", Disposition: "preserve"}, {Path: ".bashrc", Disposition: "preserve"}, {Path: ".claude", Disposition: "retained", Reason: "provider session retained on original source"}}}
}
func homeZip(t *testing.T, root string, m HomeManifest) *os.File {
	t.Helper()
	f, e := os.CreateTemp(t.TempDir(), "home-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	if e = ExportPrivateHome(context.Background(), root, m, f); e != nil {
		t.Fatal(e)
	}
	if e = f.Sync(); e != nil {
		t.Fatal(e)
	}
	return f
}
func TestPrivateHomeRoundtripPreservesOwnerDataExcludesIdentitiesAndRetries(t *testing.T) {
	root, m := homeFixture(t)
	if e := os.Chmod(filepath.Join(root, ".local/bin/tool"), 0660); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	report, e := ValidateHomeManifest(ctx, root, m)
	if e != nil || !report.Eligible || report.RetainedEntries != 2 {
		t.Fatalf("report %+v %v", report, e)
	}
	if e = os.Symlink("../assets/data.txt", filepath.Join(root, "workspace/assets/self")); e != nil {
		t.Fatal(e)
	}
	f := homeZip(t, root, m)
	st, _ := f.Stat()
	digest, e := PrivateHomeDigest(m, f, st.Size())
	if e != nil {
		t.Fatal(e)
	}
	dest, _ := homeFixture(t)
	if e = os.WriteFile(filepath.Join(dest, ".runtimed/token"), []byte("destination identity"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.RemoveAll(filepath.Join(dest, ".claude")); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dest, "workspace/assets/stale"), []byte("removed by owner"), 0600); e != nil {
		t.Fatal(e)
	}
	// A previous process crash left only private runtime staging. It must not
	// leak into an export or prevent retry of the journaled artifact.
	if e = os.MkdirAll(filepath.Join(dest, ".runtimed/.home-import-crashed"), 0700); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = InstallPrivateHome(ctx, dest, m, f, st.Size()); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = os.Stat(filepath.Join(dest, "workspace/assets/stale")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("stale selected data survived", e)
	}
	if info, e := os.Stat(filepath.Join(dest, ".local/bin/tool")); e != nil || info.Mode().Perm() != 0660 {
		t.Fatal("0660 mode was narrowed by umask", e)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, ".runtimed/token")); string(got) != "destination identity" {
		t.Fatal("runtime identity overwritten")
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "workspace/app/index.js")); string(got) != "separate app" {
		t.Fatal("app channel overwritten")
	}
	exported := homeZip(t, dest, m)
	size, _ := exported.Stat()
	got, e := PrivateHomeDigest(m, exported, size.Size())
	if e != nil || got != digest {
		t.Fatal("owner content/mode/link digest differs", e)
	}
	z, e := zip.NewReader(f, st.Size())
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range z.File {
		if strings.HasPrefix(entry.Name, ".runtimed") || strings.HasPrefix(entry.Name, ".claude") || strings.HasPrefix(entry.Name, "workspace/app") {
			t.Fatal("identity entered archive")
		}
	}
}
func TestHomeManifestRejectsNonadjacentOverlapUnclassifiedAndCredentialTrees(t *testing.T) {
	root, m := homeFixture(t)
	bad := HomeManifest{Version: 1, Entries: []HomeManifestEntry{{Path: ".runtimed", Disposition: "separate"}, {Path: "foo", Disposition: "preserve"}, {Path: "foo-bar", Disposition: "preserve"}, {Path: "foo/x", Disposition: "preserve"}}}
	if _, e := CanonicalHomeManifest(bad); e == nil {
		t.Fatal("nonadjacent ancestor accepted")
	}
	if e := os.WriteFile(filepath.Join(root, "unknown"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ValidateHomeManifest(context.Background(), root, m); e == nil {
		t.Fatal("unclassified owner data ignored")
	}
	os.Remove(filepath.Join(root, "unknown"))
	if e := os.WriteFile(filepath.Join(root, ".local/bin/auth.json"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ValidateHomeManifest(context.Background(), root, m); e == nil {
		t.Fatal("nested credentials included")
	}
	for _, name := range []string{".runtimed", ".claude", ".local/share/opencode", ".ssh", "workspace/app", "workspace"} {
		bad = HomeManifest{Version: 1, Entries: []HomeManifestEntry{{Path: ".runtimed", Disposition: "separate"}, {Path: name, Disposition: "preserve"}}}
		if _, e := CanonicalHomeManifest(bad); e == nil {
			t.Fatalf("unsafe scope %s accepted", name)
		}
	}
}
func TestPrivateHomeStockBytesVerifiedAndRestored(t *testing.T) {
	root, m := homeFixture(t)
	m.Entries = append(m.Entries, HomeManifestEntry{Path: "workspace/.gitkeep", Disposition: "stock"})
	if e := os.WriteFile(filepath.Join(root, "workspace/.gitkeep"), nil, 0644); e != nil {
		t.Fatal(e)
	}
	archive := homeZip(t, root, m)
	st, _ := archive.Stat()
	dest, _ := homeFixture(t)
	if e := InstallPrivateHome(context.Background(), dest, m, archive, st.Size()); e != nil {
		t.Fatal(e)
	}
	if data, e := os.ReadFile(filepath.Join(dest, "workspace/.gitkeep")); e != nil || len(data) != 0 {
		t.Fatal("verified stock missing at target", e)
	}
	if e := os.WriteFile(filepath.Join(root, "workspace/.gitkeep"), []byte("modified owner data"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := ValidateHomeManifest(context.Background(), root, m); e == nil {
		t.Fatal("changed stock discarded")
	}
}
func TestPrivateHomeRejectsArchiveScopeTraversalAndExternalLinks(t *testing.T) {
	_, m := homeFixture(t)
	for _, name := range []string{"../escape", ".runtimed/token", ".claude/state.json", "workspace/app/index.js"} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create(name)
		w.Write([]byte("bad"))
		z.Close()
		if _, e := PrivateHomeDigest(m, bytes.NewReader(b.Bytes()), int64(b.Len())); e == nil {
			t.Fatalf("bad archive accepted %s", name)
		}
	}
	root, m := homeFixture(t)
	if e := os.Symlink("/etc/shadow", filepath.Join(root, ".local/bin/secret")); e != nil {
		t.Fatal(e)
	}
	if _, e := ValidateHomeManifest(context.Background(), root, m); e == nil {
		t.Fatal("external symlink accepted")
	}
}
func TestPrivateHomeFlattensOnlyOwnerContainedHardlinks(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("fixture needs chown")
	}
	root, m := homeFixture(t)
	file := filepath.Join(root, ".local/bin/tool")
	if e := os.Chown(file, 1000, 1000); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(file, filepath.Join(root, "workspace/app/shared")); e != nil {
		t.Fatal(e)
	}
	homeZip(t, root, m)
	if e := os.Link(file, filepath.Join(t.TempDir(), "outside")); e != nil {
		t.Fatal(e)
	}
	if e := ExportPrivateHome(context.Background(), root, m, io.Discard); e == nil {
		t.Fatal("owner-external hardlink exported")
	}
}
func TestPrivateHomeRejectsDestinationAncestorSymlink(t *testing.T) {
	root, m := homeFixture(t)
	archive := homeZip(t, root, m)
	st, _ := archive.Stat()
	dest, _ := homeFixture(t)
	if e := os.RemoveAll(filepath.Join(dest, ".local")); e != nil {
		t.Fatal(e)
	}
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(dest, ".local")); e != nil {
		t.Fatal(e)
	}
	if e := InstallPrivateHome(context.Background(), dest, m, archive, st.Size()); e == nil {
		t.Fatal("destination escaped through ancestor link")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside owner home")
	}
}
func TestPrivateHomeStreamingFileAboveLegacyLimit(t *testing.T) {
	if os.Getenv("CUBE_HOME_LARGE_TEST") != "1" {
		t.Skip("explicit large sparse file fixture")
	}
	root, m := homeFixture(t)
	f, e := os.Create(filepath.Join(root, "workspace/assets/large.bin"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(160 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	archive := homeZip(t, root, m)
	st, _ := archive.Stat()
	dest, _ := homeFixture(t)
	if e = InstallPrivateHome(context.Background(), dest, m, archive, st.Size()); e != nil {
		t.Fatal(e)
	}
	restored, e := os.Stat(filepath.Join(dest, "workspace/assets/large.bin"))
	if e != nil || restored.Size() != 160<<20 {
		t.Fatal("large file truncated", e)
	}
}
