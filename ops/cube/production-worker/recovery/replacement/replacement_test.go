package main

import (
	"archive/zip"
	"bytes"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPIDRequiresExactOldDataIdentityAndAcknowledgedTime(t *testing.T) {
	ack := time.Unix(1700000200, 0)
	good := []byte("42\n/home/sandbox/.baarcha-postgres/data\n1700000100\n5432\n/tmp/old-socket\n\n123 4\nready\n")
	if e := validateOldPID(good, ack); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(string(good), "/home/sandbox/.baarcha-postgres/data", "/other/data", 1), strings.Replace(string(good), "1700000100", "1700000300", 1), strings.Replace(string(good), "ready", "starting", 1), "0\n", string(good) + "\x00"} {
		if validateOldPID([]byte(bad), ack) == nil {
			t.Fatal("unsafe old PID accepted")
		}
	}
}
func TestDerivedArchivePreservesOriginalAndOnlyRemovesPID(t *testing.T) {
	d := t.TempDir()
	name := filepath.Join(d, "original.zip")
	f, e := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	dir := zip.FileHeader{Name: ".baarcha-postgres/"}
	dir.SetMode(os.ModeDir | 0700)
	if _, e = w.CreateHeader(&dir); e != nil {
		t.Fatal(e)
	}
	values := map[string]string{pidPath: "42\n/home/sandbox/.baarcha-postgres/data\n1700000100\n5432\n/tmp/old\n\n123 4\nready\n", ".baarcha-postgres/data/pg_wal/000000010000000000000001": "latest WAL bytes", ".baarcha-postgres/data/PG_VERSION": "18\n"}
	for n, v := range values {
		h := zip.FileHeader{Name: n}
		h.SetMode(0600)
		out, e := w.CreateHeader(&h)
		if e != nil {
			t.Fatal(e)
		}
		out.Write([]byte(v))
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	st, _ := f.Stat()
	before := hashFile(f)
	m := rt.HomeManifest{Version: 2, Entries: []rt.HomeManifestEntry{{Path: "workspace/app", Disposition: "separate"}, {Path: ".runtimed", Disposition: "separate"}, {Path: ".baarcha-postgres", Disposition: "preserve"}}}
	dest := filepath.Join(d, "derived.zip")
	removed, e := deriveHome(f, st.Size(), dest, m, time.Unix(1700000200, 0))
	if e != nil {
		t.Fatal(e)
	}
	if removed != hashBytes([]byte(values[pidPath])) || hashFile(f) != before {
		t.Fatal("original or PID evidence changed")
	}
	b, e := os.ReadFile(dest)
	if e != nil {
		t.Fatal(e)
	}
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = zipMember(z, pidPath, 4096); e == nil {
		t.Fatal("stale pid remains")
	}
	for n, v := range values {
		if n == pidPath {
			continue
		}
		got, e := zipMember(z, n, 4096)
		if e != nil || string(got) != v {
			t.Fatal("acknowledged data altered")
		}
	}
}
func TestInventoryRequiresOnlyOriginalOwnedGuest(t *testing.T) {
	good := "NODES_SCANNED 1/1\nSANDBOX_COUNT 1\nowned unknown rest\n"
	if !exactOldInventory(good, "owned") {
		t.Fatal("owned inventory rejected")
	}
	for _, bad := range []string{strings.Replace(good, "1/1", "1/2", 1), strings.Replace(good, "COUNT 1", "COUNT 2", 1), strings.Replace(good, "owned unknown", "other unknown", 1), good + "owned unknown rest\n"} {
		if exactOldInventory(bad, "owned") {
			t.Fatal("unowned/incomplete inventory accepted")
		}
	}
}

func TestDerivedHomeAcceptsPythonDeflatedDirectoryWithoutChangingFiles(t *testing.T) {
	source, e := os.Open("testdata/python-deflated-home.zip")
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	info, e := source.Stat()
	if e != nil {
		t.Fatal(e)
	}
	before := hashFile(source)
	m := rt.HomeManifest{Version: 2, Entries: []rt.HomeManifestEntry{{Path: "workspace/app", Disposition: "separate"}, {Path: ".runtimed", Disposition: "separate"}, {Path: ".baarcha-postgres", Disposition: "preserve"}}}
	destination := filepath.Join(t.TempDir(), "derived.zip")
	if _, e = deriveHome(source, info.Size(), destination, m, time.Unix(1700000200, 0)); e != nil {
		t.Fatal(e)
	}
	if hashFile(source) != before {
		t.Fatal("source evidence changed")
	}
	original, e := zip.NewReader(source, info.Size())
	if e != nil {
		t.Fatal(e)
	}
	derived, e := zip.OpenReader(destination)
	if e != nil {
		t.Fatal(e)
	}
	defer derived.Close()
	if len(derived.File) != len(original.File)-1 {
		t.Fatal("unexpected removed members")
	}
	for _, f := range original.File {
		if f.Name == pidPath {
			continue
		}
		if f.FileInfo().IsDir() {
			if f.Method != zip.Deflate || f.CompressedSize64 == 0 {
				t.Fatal("fixture no longer exercises Python empty deflate stream")
			}
			found := false
			for _, d := range derived.File {
				if d.Name == f.Name && d.Mode() == f.Mode() && d.UncompressedSize64 == 0 {
					found = true
				}
			}
			if !found {
				t.Fatal("directory identity or mode changed")
			}
			continue
		}
		old, e := zipMember(original, f.Name, 4096)
		if e != nil {
			t.Fatal(e)
		}
		got, e := zipMember(&derived.Reader, f.Name, 4096)
		if e != nil || !bytes.Equal(old, got) {
			t.Fatal("recovered file changed")
		}
	}
}
