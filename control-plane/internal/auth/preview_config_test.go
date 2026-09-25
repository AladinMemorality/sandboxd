package auth

import (
	"strings"
	"testing"
)

func TestValidateCubePreviewSecrets(t *testing.T) {
	secret := strings.Repeat("a", 32)
	for _, raw := range []string{"v1=" + secret, " v1 = " + secret + " ,v2=" + secret + "="} {
		if err := ValidatePreviewSecrets(raw); err != nil {
			t.Fatal("valid key rejected", err)
		}
	}
	for _, raw := range []string{"", " ", "invalid", "=secret", "v1=", "v1=" + strings.Repeat("a", 31), "v1=" + secret + ",", "v1=" + secret + ",v1=" + secret, "../key=" + secret, "v1=" + secret + " bad", "v1=" + secret + "\nsecret", "v1=" + secret + "é", "v1=" + strings.Repeat("x", 1025), strings.Repeat("v=secret,", 1025)} {
		err := ValidatePreviewSecrets(raw)
		if err == nil {
			t.Errorf("invalid key configuration accepted (bytes=%d)", len(raw))
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Fatal("error leaked secret")
		}
	}
}
