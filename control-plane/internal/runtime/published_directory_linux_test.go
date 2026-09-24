package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPublishedDirectoryPreservesSourceWithoutHomeOrSecrets(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{
		"package.json": "{}", "src/auth.ts": "export const enabled = true", "public/media/demo.pdf": "public asset",
		".env": "private", "data/customer.json": "private", ".claude/auth.json": "private", "node_modules/pkg/index.js": "dependency",
	} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := ExportPublishedDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != 3 {
		t.Fatalf("unexpected published files: %d", len(z.File))
	}
	for _, file := range z.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil || bytes.Contains(b, []byte("private")) {
			t.Fatalf("private content exported: %s", file.Name)
		}
	}
}

func TestPublishedDirectoryRejectsUnsafeSources(t *testing.T) {
	for _, kind := range []string{"symlink", "ancestor", "hardlink", "fifo", "large", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			external := filepath.Join(t.TempDir(), "private.js")
			if err := os.WriteFile(external, []byte("secret"), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "source.js")
			ctx := context.Background()
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(external, target)
			case "ancestor":
				link := filepath.Join(t.TempDir(), "link")
				err = os.Symlink(root, link)
				root = link
			case "hardlink":
				err = os.Link(external, target)
			case "fifo":
				err = unix.Mkfifo(target, 0600)
			case "large":
				var f *os.File
				f, err = os.Create(target)
				if err == nil {
					err = f.Truncate(MaxFileWriteBytes + 1)
					f.Close()
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				err = os.WriteFile(target, []byte("source"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ExportPublishedDirectory(ctx, root); err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}
