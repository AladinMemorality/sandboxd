package migration

import (
	"context"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"io"
	"os"
	"path/filepath"
)

func (b *OfflineBackend) saveWorkspaceArchive(ctx context.Context, m *store.RuntimeMigration, name string, export func(io.Writer) error) (string, error) {
	path, e := b.artifact(m, name)
	if e != nil {
		return "", e
	}
	file, e := os.CreateTemp(filepath.Dir(path), ".workspace-")
	if e != nil {
		return "", e
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if e = export(file); e != nil {
		return "", e
	}
	if e = ctx.Err(); e != nil {
		return "", e
	}
	info, e := file.Stat()
	if e != nil {
		return "", e
	}
	digest, e := runtime.PrivateWorkspaceFileDigestContext(ctx, file, info.Size())
	if e != nil {
		return "", e
	}
	if e = file.Sync(); e != nil {
		return "", e
	}
	if old, err := os.Open(path); err == nil {
		defer old.Close()
		info, err := old.Stat()
		if err != nil {
			return "", err
		}
		previous, err := runtime.PrivateWorkspaceFileDigestContext(ctx, old, info.Size())
		if err != nil || previous != digest {
			return "", errors.New("retained workspace archive differs; refusing to overwrite recovery data")
		}
		return digest, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if e = os.Link(file.Name(), path); e != nil {
		return "", e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return "", e
	}
	defer dir.Close()
	if e = dir.Sync(); e != nil {
		return "", e
	}
	return digest, nil
}
func (b *OfflineBackend) readWorkspaceArchive(ctx context.Context, m *store.RuntimeMigration, name, digest string) (*os.File, int64, error) {
	path, e := b.artifact(m, name)
	if e != nil {
		return nil, 0, e
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, 0, e
	}
	info, e := file.Stat()
	if e != nil {
		file.Close()
		return nil, 0, e
	}
	actual, e := runtime.PrivateWorkspaceFileDigestContext(ctx, file, info.Size())
	if e != nil || actual != digest {
		file.Close()
		return nil, 0, errors.New("workspace recovery archive checksum mismatch")
	}
	return file, info.Size(), nil
}
