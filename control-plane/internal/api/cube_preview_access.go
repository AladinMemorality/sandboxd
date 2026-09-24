package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func runtimeProviderName(sb *store.Sandbox) string {
	if sb.RuntimeProvider == "" {
		return "docker"
	}
	return sb.RuntimeProvider
}

// v1CubePreviewAccess is a service API, never a public auth exemption. The
// platform must first authorize its viewer; the tenant-scoped service actor
// can then delegate access to this one sandbox without receiving ingress keys.
func (s *Server) v1CubePreviewAccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := r.PathValue("id")
	sb, err := s.Store.Get(r.Context(), id)
	if err != nil || sb.RuntimeProvider != "cube" || !sb.AppID.Valid {
		writeV1Err(w, 404, "not_found", "no such Cube sandbox")
		return
	}
	if _, err = s.Store.GetAppForOwner(r.Context(), sb.AppID.String, tenantToken(r)); err != nil {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return
	}
	owner, err := s.Store.GetWorkspaceOwner(r.Context(), id)
	if err != nil || owner.ExternalUserID == "" {
		writeV1Err(w, 409, "preview_owner_unavailable", "preview owner unavailable")
		return
	}
	secrets := s.authCfg().PreviewSecrets
	kids := make([]string, 0, len(secrets))
	for kid, secret := range secrets {
		if secret != "" {
			kids = append(kids, kid)
		}
	}
	sort.Strings(kids)
	if len(kids) == 0 {
		writeV1Err(w, 503, "preview_auth_unavailable", "preview signing key is not configured")
		return
	}
	now := time.Now()
	expires := now.Add(5 * time.Minute)
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT", "kid": kids[0]})
	payload, _ := json.Marshal(auth.PreviewClaims{Iss: "sandboxd", Iat: now.Unix(), Exp: expires.Unix(), Aud: auth.PreviewAudience, Sub: owner.ExternalUserID, SandboxID: id})
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secrets[kids[0]]))
	_, _ = mac.Write([]byte(signed))
	token := signed + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	stable := s.previewURL(id, webPortOf(sb))
	access := stable + "/__sandboxd/preview-auth?" + url.Values{"token": {token}, "path": {"/"}}.Encode()
	writeJSON(w, http.StatusOK, map[string]any{"url": stable, "access_url": access, "token": token, "expires_at": expires.UTC().Format(time.RFC3339)})
}
