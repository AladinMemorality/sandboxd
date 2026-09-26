package auth

import (
	"errors"
	"regexp"
	"strings"
)

var previewKeyID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidatePreviewSecrets is the stricter startup/reload contract for enabled
// Cube previews. Docker-only configuration retains its existing parser behavior.
// Errors intentionally never include key IDs or secret values.
func ValidatePreviewSecrets(raw string) error {
	invalid := errors.New("Cube requires valid SANDBOXD_PREVIEW_TOKEN_SECRETS (unique kid=secret pairs, secrets at least 32 bytes)")
	if len(raw) == 0 || len(raw) > 8192 {
		return invalid
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 8 {
		return invalid
	}
	seen := map[string]bool{}
	for _, part := range parts {
		kid, secret, ok := strings.Cut(strings.TrimSpace(part), "=")
		kid = strings.TrimSpace(kid)
		secret = strings.TrimSpace(secret)
		if !ok || !previewKeyID.MatchString(kid) || seen[kid] || len(secret) < 32 || len(secret) > 1024 {
			return invalid
		}
		for _, b := range []byte(secret) {
			if b < 33 || b > 126 {
				return invalid
			}
		}
		seen[kid] = true
	}
	return nil
}
