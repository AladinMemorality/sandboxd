package projectsource

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func recipe() Recipe {
	return Recipe{Version: 1, RuntimeImage: "example/runtime@sha256:" + strings.Repeat("a", 64), PackageManager: "npm", Lockfile: "package-lock.json", Install: []string{"npm", "ci", "--no-audit", "--no-fund"}, Start: []string{"npm", "start"}, Port: 3000, HealthPath: "/", DataPaths: []string{"data"}, SecretPaths: []string{".env"}, OutputPaths: []string{".next"}}
}
func zipFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for p, v := range files {
		f, e := w.Create(p)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(v))
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func baseFiles() map[string]string {
	return map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "package-lock.json": `{"lockfileVersion":3}`, "server.js": "server source", ".config/settings.json": "private source", "node_modules/lib/index.js": "rebuild me", "data/app.sqlite": "keep elsewhere", ".env": "SECRET=value", ".next/cache/blob": "rebuild me"}
}
func TestSourceRoundtripPreservesPrivateCodeAndExcludesRebuildableState(t *testing.T) {
	source, m, e := Build(zipFiles(t, baseFiles()), recipe())
	if e != nil {
		t.Fatal(e)
	}
	if e = Verify(source, m); e != nil {
		t.Fatal(e)
	}
	if len(m.Files) != 4 || len(m.Excluded) != 4 {
		t.Fatalf("files=%+v exclusions=%+v", m.Files, m.Excluded)
	}
	z, _ := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	for _, f := range z.File {
		if f.Name == "node_modules/lib/index.js" || f.Name == ".env" || f.Name == "data/app.sqlite" {
			t.Fatal("excluded bytes entered source")
		}
	}
}
func TestSourceDeterministicAndLocalPackageCacheInvalidation(t *testing.T) {
	files := baseFiles()
	files["packages/local/index.js"] = "v1"
	a, ma, e := Build(zipFiles(t, files), recipe())
	if e != nil {
		t.Fatal(e)
	}
	b, mb, e := Build(zipFiles(t, files), recipe())
	if e != nil || !bytes.Equal(a, b) || ma.DependencyKey != mb.DependencyKey {
		t.Fatalf("non-deterministic: %v", e)
	}
	files["packages/local/index.js"] = "v2"
	_, mc, e := Build(zipFiles(t, files), recipe())
	if e != nil || ma.DependencyKey == mc.DependencyKey {
		t.Fatalf("local change reused cache: %v", e)
	}
}
func TestSourceRejectsUnclassifiedDataTraversalLinksAndMissingLock(t *testing.T) {
	for _, name := range []string{"../escape", "/escape", "a/../../escape", "private.sqlite", ".npmrc", "nested/.env"} {
		t.Run(name, func(t *testing.T) {
			files := baseFiles()
			files[name] = "bad"
			if _, _, e := Build(zipFiles(t, files), recipe()); e == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
	files := baseFiles()
	delete(files, "package-lock.json")
	if _, _, e := Build(zipFiles(t, files), recipe()); e == nil {
		t.Fatal("unlocked install accepted")
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "linked.js"}
	h.SetMode(os.ModeSymlink | 0777)
	f, _ := w.CreateHeader(h)
	f.Write([]byte("/etc/passwd"))
	w.Close()
	if _, _, e := Build(b.Bytes(), recipe()); e == nil {
		t.Fatal("link accepted")
	}
}
func TestSourceRejectsChecksumInventoryAndRecipeTampering(t *testing.T) {
	data, m, e := Build(zipFiles(t, baseFiles()), recipe())
	if e != nil {
		t.Fatal(e)
	}
	modified := append([]byte{}, data...)
	modified[0] ^= 1
	if e = Verify(modified, m); e == nil {
		t.Fatal("corruption accepted")
	}
	m.Files[0].SHA256 = strings.Repeat("f", 64)
	if e = Verify(data, m); e == nil {
		t.Fatal("inventory corruption accepted")
	}
	r := recipe()
	r.Install = []string{"npm", "install"}
	if e = r.Validate(); e == nil {
		t.Fatal("unlocked command accepted")
	}
	r = recipe()
	r.DataPaths = []string{"package-lock.json"}
	if e = r.Validate(); e == nil {
		t.Fatal("excluded lockfile accepted")
	}
	r = recipe()
	r.RuntimeImage = "example/runtime:latest"
	if e = r.Validate(); e == nil {
		t.Fatal("mutable image accepted")
	}
	// Confirm the artifact has a stable JSON contract for the uploader.
	if _, e = json.Marshal(m); e != nil {
		t.Fatal(e)
	}
}
