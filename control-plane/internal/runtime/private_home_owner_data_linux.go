package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
)

// HomeOwnerDataFile identifies an operator-reviewed application credential file,
// not a model-provider or supervisor identity. It is private same-owner data;
// public export/remix paths do not consume this manifest.
type HomeOwnerDataFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

func ownerDataFile(m HomeManifest, name string) *HomeOwnerDataFile {
	for i := range m.OwnerDataFiles {
		if m.OwnerDataFiles[i].Path == name {
			return &m.OwnerDataFiles[i]
		}
	}
	return nil
}

func canonicalOwnerDataFiles(m *HomeManifest) error {
	if len(m.OwnerDataFiles) == 0 {
		return nil
	}
	if m.Version != 2 || len(m.OwnerDataFiles) > 32 {
		return errors.New("invalid owner data review")
	}
	m.OwnerDataFiles = append([]HomeOwnerDataFile(nil), m.OwnerDataFiles...)
	sort.Slice(m.OwnerDataFiles, func(i, j int) bool { return m.OwnerDataFiles[i].Path < m.OwnerDataFiles[j].Path })
	for i, f := range m.OwnerDataFiles {
		entry, ok := homeDisposition(f.Path, *m)
		hash, err := hex.DecodeString(f.SHA256)
		// App state must live below an app-specific data directory. A filename
		// exception cannot override any protected provider/control-plane root.
		parts := strings.Split(f.Path, "/")
		appPath := strings.HasPrefix(f.Path, ".local/share/") && len(parts) >= 4
		leaf := path.Base(f.Path)
		if !ValidArchivePath(f.Path) || !appPath || protectedHomeIdentity(f.Path) || !ok || entry.Disposition != "preserve" || (leaf != "auth.json" && leaf != "credentials.json" && leaf != ".credentials.json") || f.Bytes <= 0 || f.Bytes > 1<<20 || err != nil || len(hash) != 32 || f.SHA256 != strings.ToLower(f.SHA256) || (i > 0 && m.OwnerDataFiles[i-1].Path == f.Path) {
			return errors.New("owner data review is not a bounded application file")
		}
	}
	return nil
}

func allowedSensitiveHomeFile(m HomeManifest, scope, name string, regular bool) bool {
	return regular && ((scope == name && reviewedProviderDocument(name)) || (!protectedHomeIdentity(name) && ownerDataFile(m, name) != nil))
}

func verifyOwnerDataFile(f HomeOwnerDataFile, r io.Reader, size int64) error {
	if size != f.Bytes {
		return errors.New("reviewed owner data size differs")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, f.Bytes+1))
	if err != nil {
		return err
	}
	if n != f.Bytes || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return errors.New("reviewed owner data content differs")
	}
	return nil
}
