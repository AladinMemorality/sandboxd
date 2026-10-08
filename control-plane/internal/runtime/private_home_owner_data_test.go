package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func ownerDataFixture(t *testing.T) (string, HomeManifest, string) {
	t.Helper()
	root, m := homeFixture(t)
	name := ".local/share/example-app/auth.json"
	data := []byte(`{"hash":"app-password-hash","salt":"app-salt","secret":"app-session-secret"}`)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
		t.Fatal(err)
	}
	m.Version = 2
	m.Entries = append(m.Entries, HomeManifestEntry{Path: ".local/share", Disposition: "preserve"})
	m.OwnerDataFiles = []HomeOwnerDataFile{{Path: name, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Bytes: int64(len(data))}}
	return root, m, name
}

func TestReviewedOwnerApplicationAuthRoundtrip(t *testing.T) {
	root, m, name := ownerDataFixture(t)
	plain := m
	plain.OwnerDataFiles = nil
	if _, err := ValidateHomeManifest(context.Background(), root, plain); err == nil {
		t.Fatal("auth file accepted without review")
	}
	f := homeZip(t, root, m)
	info, _ := f.Stat()
	dest, _, _ := ownerDataFixture(t)
	if err := os.WriteFile(filepath.Join(dest, name), []byte("replaced destination"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallPrivateHome(context.Background(), dest, m, f, info.Size()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, name))
	want, _ := os.ReadFile(filepath.Join(root, name))
	if !bytes.Equal(got, want) {
		t.Fatal("app credentials changed")
	}
	if st, _ := os.Stat(filepath.Join(dest, name)); st.Mode().Perm() != 0600 {
		t.Fatal("private mode changed")
	}
	if _, err := PrivateHomeDigest(plain, f, info.Size()); err == nil {
		t.Fatal("import accepted without review")
	}
	if err := os.WriteFile(filepath.Join(root, name), bytes.Repeat([]byte("x"), len(want)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateHomeManifest(context.Background(), root, m); err == nil {
		t.Fatal("changed contents accepted")
	}
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateHomeManifest(context.Background(), root, m); err == nil {
		t.Fatal("missing reviewed file accepted")
	}
	if err := os.Symlink("../../bin/tool", filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateHomeManifest(context.Background(), root, m); err == nil {
		t.Fatal("reviewed file replaced with symlink")
	}
}

func TestOwnerDataReviewDoesNotPermitProviderIdentity(t *testing.T) {
	_, base, _ := ownerDataFixture(t)
	for _, name := range []string{".local/share/opencode/auth.json", ".runtimed/auth.json", ".config/gcloud/credentials.json", ".local/share/../opencode/auth.json", ".local/share/auth.json", ".local/share/app/plain.txt"} {
		m := base
		m.OwnerDataFiles = append([]HomeOwnerDataFile(nil), base.OwnerDataFiles...)
		m.OwnerDataFiles[0].Path = name
		if _, err := CanonicalHomeManifest(m); err == nil {
			t.Fatalf("accepted invalid scope %s", name)
		}
	}
	m := base
	m.Version = 1
	if _, err := CanonicalHomeManifest(m); err == nil {
		t.Fatal("v1 review accepted")
	}
	m = base
	m.OwnerDataFiles = append(m.OwnerDataFiles, m.OwnerDataFiles[0])
	if _, err := CanonicalHomeManifest(m); err == nil {
		t.Fatal("duplicate review accepted")
	}
}

func TestOwnerDataArchiveRejectsTamperingAndOmission(t *testing.T) {
	root, m, name := ownerDataFixture(t)
	f := homeZip(t, root, m)
	st, _ := f.Stat()
	src, err := zip.NewReader(f, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	for _, omit := range []bool{false, true} {
		var out bytes.Buffer
		writer := zip.NewWriter(&out)
		for _, entry := range src.File {
			if entry.Name == name && omit {
				continue
			}
			r, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			if entry.Name == name {
				body = bytes.Repeat([]byte("x"), len(body))
			}
			h := entry.FileHeader
			w, err := writer.CreateHeader(&h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(body); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := PrivateHomeDigest(m, bytes.NewReader(out.Bytes()), int64(out.Len())); err == nil {
			t.Fatal("modified archive accepted", omit)
		}
	}
}
