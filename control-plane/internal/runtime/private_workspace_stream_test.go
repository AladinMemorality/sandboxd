package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateWorkspaceFileCanonicalCompatibilityAndCancellation(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "app")
	os.Mkdir(root, 0755)
	os.WriteFile(filepath.Join(root, ".env"), []byte("owner data"), 0600)
	os.Chmod(filepath.Join(root, ".env"), 0660)
	var b bytes.Buffer
	if err := ExportPrivateWorkspaceFile(context.Background(), root, home, &b); err != nil {
		t.Fatal(err)
	}
	old, err := PrivateWorkspaceDigest(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got, err := PrivateWorkspaceFileDigest(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil || got != old {
		t.Fatal("journal digest changed", err)
	}
	dest := filepath.Join(t.TempDir(), "app")
	os.Mkdir(dest, 0755)
	os.WriteFile(filepath.Join(dest, "keep"), []byte("unchanged"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = InstallPrivateWorkspaceFile(ctx, dest, bytes.NewReader(b.Bytes()), int64(b.Len())); err == nil {
		t.Fatal("cancelled import accepted")
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "keep")); string(got) != "unchanged" {
		t.Fatal("cancelled import replaced source")
	}
	if err = ExportPrivateWorkspaceFile(ctx, root, home, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = InstallPrivateWorkspaceFile(context.Background(), dest, bytes.NewReader(b.Bytes()), int64(b.Len())); err != nil {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err = ExportPrivateWorkspaceFile(context.Background(), dest, filepath.Dir(dest), &after); err != nil {
		t.Fatal(err)
	}
	got, err = PrivateWorkspaceFileDigest(bytes.NewReader(after.Bytes()), int64(after.Len()))
	if err != nil || got != old {
		t.Fatal("roundtrip changed owner tree", err)
	}
}

func TestPrivateArchiveIndexZIP64AndMalformedMetadata(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for i := 0; i < 65536; i++ {
		if _, err := z.Create(fmt.Sprintf("d/%d/", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateArchiveIndex(bytes.NewReader(b.Bytes()), int64(b.Len()), 65536); err != nil {
		t.Fatal("valid ZIP64", err)
	}
	if err := ValidatePrivateArchiveIndex(bytes.NewReader(b.Bytes()), int64(b.Len()), 65535); err == nil {
		t.Fatal("entry bound bypass")
	}
	var small bytes.Buffer
	z = zip.NewWriter(&small)
	w, _ := z.Create("file")
	w.Write([]byte("x"))
	z.Close()
	good := small.Bytes()
	for _, mutate := range []func([]byte){
		func(v []byte) { binary.LittleEndian.PutUint16(v[len(v)-12:], 0) },
		func(v []byte) { binary.LittleEndian.PutUint32(v[len(v)-10:], 0xffffffff) },
		func(v []byte) { binary.LittleEndian.PutUint32(v[len(v)-6:], 0) },
		func(v []byte) { binary.LittleEndian.PutUint16(v[len(v)-18:], 1) },
	} {
		v := append([]byte(nil), good...)
		mutate(v)
		if err := ValidatePrivateArchiveIndex(bytes.NewReader(v), int64(len(v)), 200000); err == nil {
			t.Fatal("forged metadata accepted")
		}
	}
	if err := ValidatePrivateArchiveIndex(bytes.NewReader(good[:len(good)-1]), int64(len(good)-1), 200000); err == nil {
		t.Fatal("truncated archive accepted")
	}
}

func TestPrivateWorkspaceFileLargeRoundtrip(t *testing.T) {
	if os.Getenv("CUBE_ARCHIVE_LARGE_TEST") != "1" {
		t.Skip("explicit 270MiB incompressible archive fixture")
	}
	home := t.TempDir()
	root := filepath.Join(home, "app")
	os.Mkdir(root, 0755)
	f, err := os.Create(filepath.Join(root, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.CopyN(f, rand.Reader, 270<<20); err != nil {
		t.Fatal(err)
	}
	f.WriteAt([]byte("end marker"), (270<<20)-10)
	f.Close()
	archive, err := os.CreateTemp(t.TempDir(), "archive-")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if err = ExportPrivateWorkspaceFile(context.Background(), root, home, archive); err != nil {
		t.Fatal(err)
	}
	st, _ := archive.Stat()
	if st.Size() <= MaxPrivateWorkspaceBytes {
		t.Fatal("fixture must exceed old compressed archive limit")
	}
	before, err := PrivateWorkspaceFileDigest(archive, st.Size())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = privateWorkspaceReader(archive, st.Size(), workspaceV1Limits); err == nil {
		t.Fatal("fixture must exceed old per-file limit")
	}
	dest := filepath.Join(t.TempDir(), "app")
	os.Mkdir(dest, 0755)
	if err = InstallPrivateWorkspaceFile(context.Background(), dest, archive, st.Size()); err != nil {
		t.Fatal(err)
	}
	restored, err := os.Open(filepath.Join(dest, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	buf := make([]byte, 10)
	if _, err = restored.ReadAt(buf, (270<<20)-10); err != nil || string(buf) != "end marker" {
		t.Fatal("large file tail lost", err)
	}
	second, err := os.CreateTemp(t.TempDir(), "second-")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = ExportPrivateWorkspaceFile(context.Background(), dest, filepath.Dir(dest), second); err != nil {
		t.Fatal(err)
	}
	st, _ = second.Stat()
	after, err := PrivateWorkspaceFileDigest(second, st.Size())
	if err != nil || before != after {
		t.Fatal("large checksum mismatch", err)
	}
}
