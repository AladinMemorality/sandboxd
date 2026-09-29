package publication

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

// Bundle is the CI/import contract: package.json and a successful dist build.
// Nothing is extracted, executed or written until Capture verifies all files.
func Bundle(data []byte) (Source, error) {
	if len(data) > maxBytes {
		return nil, ErrUnsupported
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	source := &bundle{files: map[string]*zip.File{}}
	var total uint64
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() || !validPath(f.Name) || (f.Name != "package.json" && !strings.HasPrefix(f.Name, "dist/")) || f.UncompressedSize64 > runtime.MaxFileContentBytes {
			return nil, ErrUnsupported
		}
		if _, exists := source.files[f.Name]; exists {
			return nil, ErrUnsupported
		}
		source.files[f.Name] = f
		total += f.UncompressedSize64
		if total > maxBytes || len(source.files) > maxFiles {
			return nil, ErrUnsupported
		}
	}
	return source, nil
}

type bundle struct{ files map[string]*zip.File }

func (b *bundle) ListFiles(context.Context, string, bool) (*runtime.FileList, error) {
	list := &runtime.FileList{}
	for name, f := range b.files {
		if strings.HasPrefix(name, "dist/") {
			list.Entries = append(list.Entries, runtime.FileEntry{Path: name, Type: "file", Size: int64(f.UncompressedSize64)})
		}
	}
	return list, nil
}
func (b *bundle) ReadFile(_ context.Context, name string) ([]byte, error) {
	f := b.files[name]
	if f == nil {
		return nil, os.ErrNotExist
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, runtime.MaxFileContentBytes+1))
}
