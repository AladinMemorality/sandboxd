package runtime

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func testSourceZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, content := range files {
		f, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		io.WriteString(f, content)
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestSourceArchiveExcludesPrivateState(t *testing.T) {
	files := map[string]string{"src/App.tsx": "published source", "package.json": "{}", "tsconfig.json": "{}", "bench-state.json": "SECRET_STATE", "credentials.json": "SECRET_CREDS", "secrets.json": "SECRET_SECRETS", "src/credentials.json": "SECRET_SRC_CREDS", "public/logo.svg": "<svg/>", ".env": "SECRET_ENV", ".env.production": "SECRET_PROD", ".git/config": "SECRET_GIT", ".runtimed/tasks/result.json": "SECRET_TASK", ".npmrc": "SECRET_NPM", ".ssh/id_rsa": "SECRET_SSH", "data/users.json": "SECRET_DATA", "storage/files.json": "SECRET_STORAGE", "app.db": "SECRET_DB", "app.sqlite3": "SECRET_DB", "private/client.json": "SECRET_PRIVATE"}
	data, err := SanitizeSourceArchive(testSourceZip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	z, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if len(z.File) != 4 {
		t.Fatalf("published count %d", len(z.File))
	}
	for _, file := range z.File {
		r, _ := file.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		if strings.Contains(string(b), "SECRET") {
			t.Fatalf("secret exported: %s", file.Name)
		}
	}
}
func TestSourceArchiveRejectsTraversalAndSpecialEntries(t *testing.T) {
	for _, name := range []string{"../file.ts", "/etc/file.ts", "a/../file.ts", "a\\file.ts"} {
		if _, err := SanitizeSourceArchive(testSourceZip(t, map[string]string{name: "escape"})); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "link.ts"}
	h.SetMode(os.ModeSymlink | 0777)
	f, _ := z.CreateHeader(h)
	io.WriteString(f, "/etc/passwd")
	z.Close()
	if _, err := SanitizeSourceArchive(b.Bytes()); err == nil {
		t.Fatal("accepted symlink archive")
	}
	b.Reset()
	z = zip.NewWriter(&b)
	for i := 0; i < 2; i++ {
		f, _ := z.Create("duplicate.ts")
		io.WriteString(f, "x")
	}
	z.Close()
	if _, err := SanitizeSourceArchive(b.Bytes()); err == nil {
		t.Fatal("accepted duplicate archive entries")
	}
}

func TestSourceArchivePreservesPlatformPublicMediaOnly(t *testing.T) {
	files := map[string]string{"public/media/manifest.json": `{"files":[]}`}
	want := map[string]bool{"public/media/manifest.json": true}
	for _, ext := range []string{"mp4", "webm", "mov", "mp3", "m4a", "wav", "ogg", "weba", "flac", "pdf"} {
		for _, tree := range []string{"public", "assets", "static"} {
			name := tree + "/media/sample." + ext
			files[name] = "published media"
			want[name] = true
		}
		for _, prefix := range []string{"", "src/", "private/", "public/private/", "public/.hidden/", "public/credentials.", "public/secrets."} {
			files[prefix+"sample."+ext] = "PRIVATE_MEDIA"
		}
	}
	data, err := SanitizeSourceArchive(testSourceZip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != len(want) {
		t.Fatalf("published media count %d, want %d", len(z.File), len(want))
	}
	for _, f := range z.File {
		if !want[f.Name] {
			t.Fatalf("private media exported: %s", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		r.Close()
		if err != nil || strings.Contains(string(content), "PRIVATE_MEDIA") {
			t.Fatalf("invalid exported media %s", f.Name)
		}
	}
}

func TestPublishedAuthenticationSourceSurvivesRemix(t *testing.T) {
	for _, name := range []string{"src/auth.ts", "backend/auth.py", "server/session.js", "src/sessions.go"} {
		if !PublishedSourcePath(name) {
			t.Errorf("source module excluded: %s", name)
		}
	}
	for _, name := range []string{"src/auth.json", "public/session.json", "src/credentials.ts", "src/secrets.js", "private/auth.ts", ".env"} {
		if PublishedSourcePath(name) {
			t.Errorf("private state included: %s", name)
		}
	}
}

func TestSourceArchivePreservesDependencyPatchesOnly(t *testing.T) {
	patch := "diff --git a/dist/node/index.js b/dist/node/index.js\n"
	files := map[string]string{"patches/vite@5.4.21.patch": patch}
	for _, name := range []string{"owner.patch", "private/change.patch", "src/change.patch", "patches/private/change.patch", "patches/secrets.patch", "patches/credentials.patch", "patches/.hidden.patch", "patches/data.sql"} {
		files[name] = "PRIVATE_FIXTURE"
	}
	data, err := SanitizeSourceArchive(testSourceZip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) != 1 || z.File[0].Name != "patches/vite@5.4.21.patch" {
		t.Fatal("dependency patch missing or private patch published")
	}
	r, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	content, err := io.ReadAll(r)
	if err != nil || string(content) != patch {
		t.Fatal("dependency patch bytes changed")
	}
}
