package migration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// ReadHomeManifests accepts explicit per-sandbox scope; it never guesses that a
// provider directory is safe to transport or substitutes another owner's plan.
func ReadHomeManifests(path string) (map[string]runtime.HomeManifest, error) {
	values := map[string]runtime.HomeManifest{}
	if path == "" {
		return values, nil
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, errors.New("home manifests must be a bounded regular JSON file")
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&values); e != nil {
		return nil, e
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || values == nil {
		return nil, errors.New("invalid home manifest map")
	}
	for _, value := range values {
		if _, e = runtime.CanonicalHomeManifest(value); e != nil {
			return nil, e
		}
	}
	return values, nil
}
func migrationHome(m *store.RuntimeMigration) (runtime.HomeManifest, error) {
	var manifest runtime.HomeManifest
	if e := json.Unmarshal([]byte(m.HomeManifestJSON), &manifest); e != nil {
		return manifest, e
	}
	canonical, e := runtime.CanonicalHomeManifest(manifest)
	if e != nil {
		return manifest, e
	}
	if string(canonical) != m.HomeManifestJSON {
		return manifest, errors.New("journal home manifest is not canonical")
	}
	return manifest, nil
}

// saveHomeArchive validates a disk-backed stream, fsyncs it, then publishes it
// without replacement. An ambiguous retry must reproduce the same contents.
func (b *OfflineBackend) saveHomeArchive(ctx context.Context, m *store.RuntimeMigration, name string, export func(io.Writer) error) (string, error) {
	manifest, e := migrationHome(m)
	if e != nil {
		return "", e
	}
	path, e := b.artifact(m, name)
	if e != nil {
		return "", e
	}
	file, e := os.CreateTemp(filepath.Dir(path), ".home-")
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
	digest, e := runtime.PrivateHomeDigest(manifest, file, info.Size())
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
		previous, err := runtime.PrivateHomeDigest(manifest, old, info.Size())
		if err != nil || previous != digest {
			return "", errors.New("retained home archive differs; refusing to overwrite recovery data")
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
func (b *OfflineBackend) readHomeArchive(m *store.RuntimeMigration, name, digest string) (*os.File, int64, error) {
	manifest, e := migrationHome(m)
	if e != nil {
		return nil, 0, e
	}
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
	actual, e := runtime.PrivateHomeDigest(manifest, file, info.Size())
	if e != nil || actual != digest {
		file.Close()
		return nil, 0, errors.New("home recovery archive checksum mismatch")
	}
	return file, info.Size(), nil
}
func (b *OfflineBackend) archiveSourceHome(ctx context.Context, m *store.RuntimeMigration) error {
	if m.HomeManifestJSON == "" {
		return nil
	}
	manifest, e := migrationHome(m)
	if e != nil {
		return e
	}
	digest, e := b.saveHomeArchive(ctx, m, "source-home", func(w io.Writer) error {
		return runtime.ExportPrivateHome(ctx, filepath.Join(b.WorkspaceRoot, m.SandboxID), manifest, w)
	})
	if e != nil {
		return e
	}
	return b.Store.RecordMigrationHome(ctx, m.SandboxID, m.Phase, digest, false)
}
func (b *OfflineBackend) importTargetHome(ctx context.Context, m *store.RuntimeMigration, client *runtime.Client) error {
	if m.HomeManifestJSON == "" {
		return nil
	}
	manifest, e := migrationHome(m)
	if e != nil {
		return e
	}
	file, size, e := b.readHomeArchive(m, "source-home", m.HomeSHA256)
	if e != nil {
		return e
	}
	defer file.Close()
	return client.ImportPrivateHome(ctx, manifest, file, size)
}
func (b *OfflineBackend) verifyTargetHome(ctx context.Context, m *store.RuntimeMigration, client *runtime.Client) error {
	if m.HomeManifestJSON == "" {
		return nil
	}
	manifest, e := migrationHome(m)
	if e != nil {
		return e
	}
	digest, e := b.saveHomeArchive(ctx, m, "verified-home", func(w io.Writer) error { return client.ExportPrivateHome(ctx, manifest, w) })
	if e != nil {
		return e
	}
	if digest != m.HomeSHA256 {
		return errors.New("target owner home differs from retained source")
	}
	return nil
}
func (b *OfflineBackend) archiveTargetHome(ctx context.Context, m *store.RuntimeMigration, client *runtime.Client) error {
	if m.HomeManifestJSON == "" {
		return nil
	}
	manifest, e := migrationHome(m)
	if e != nil {
		return e
	}
	export := func(w io.Writer) error { return client.ExportPrivateHome(ctx, manifest, w) }
	digest, e := b.saveHomeArchive(ctx, m, "rollback-home", export)
	if e != nil {
		return e
	}
	// A second export validates that the quiescence barrier held across the copy.
	if _, e = b.saveHomeArchive(ctx, m, "rollback-home", export); e != nil {
		return e
	}
	return b.Store.RecordMigrationHome(ctx, m.SandboxID, m.Phase, digest, true)
}
func (b *OfflineBackend) restoreSourceHome(ctx context.Context, m *store.RuntimeMigration) error {
	if m.HomeManifestJSON == "" {
		return nil
	}
	manifest, e := migrationHome(m)
	if e != nil {
		return e
	}
	original, _, e := b.readHomeArchive(m, "source-home", m.HomeSHA256)
	if e != nil {
		return e
	}
	original.Close()
	file, size, e := b.readHomeArchive(m, "rollback-home", m.RollbackHomeSHA256)
	if e != nil {
		return e
	}
	defer file.Close()
	home := filepath.Join(b.WorkspaceRoot, m.SandboxID)
	if e = runtime.InstallPrivateHome(ctx, home, manifest, file, size); e != nil {
		return e
	}
	for _, entry := range manifest.Entries {
		if entry.Disposition != "preserve" && entry.Disposition != "stock" {
			continue
		}
		if e = filepath.Walk(filepath.Join(home, entry.Path), func(path string, info os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			return os.Lchown(path, 1000, 1000)
		}); e != nil {
			return e
		}
	}
	digest, e := b.saveHomeArchive(ctx, m, "restored-home", func(w io.Writer) error { return runtime.ExportPrivateHome(ctx, home, manifest, w) })
	if e != nil {
		return e
	}
	if digest != m.RollbackHomeSHA256 {
		return errors.New("restored owner home checksum mismatch")
	}
	return nil
}
