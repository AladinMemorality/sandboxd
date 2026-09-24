package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ManifestSourceDigest identifies the bytes actually parsed at supervisor boot.
// Missing and empty manifests have different identities.
func ManifestSourceDigest(raw []byte, present bool) string {
	if !present {
		raw = []byte("sandboxd:absent-manifest:v1")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// DockerConfigRevision binds the runtime-visible app environment at container
// creation. It includes removed keys and never exposes plaintext in status.
func DockerConfigRevision(env []string) string {
	values := append([]string{}, env...)
	sort.Strings(values)
	raw, _ := json.Marshal(values)
	digest := sha256.Sum256(raw)
	return "docker:" + hex.EncodeToString(digest[:])
}
