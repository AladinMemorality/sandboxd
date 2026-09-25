package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// No environment proxy discovery: guest ingress credentials may only be sent
// to the configured CubeProxy origin. No overall timeout, so HMR/WebSocket and
// event streams remain open; connection and response-header waits are bounded.
var cubePreviewTransport = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   20,
}

var cubePreviewDNSLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// TryServeCubePreview is called before the Docker wake catch-all. It retains
// Baarcha's stable preview host and checks browser authentication itself: this
// path intentionally sits outside the service-token API middleware.
func (s *Server) TryServeCubePreview(w http.ResponseWriter, r *http.Request) bool {
	if s.Store == nil {
		return false
	}
	re := cachedRE("cube-preview|"+s.PreviewDomain, `(?i)^s-([0-9a-z]{1,128})-([0-9]{1,5})\.preview\.`+regexp.QuoteMeta(s.PreviewDomain)+`(?::[0-9]{1,5})?$`)
	m := re.FindStringSubmatch(r.Host)
	if m == nil {
		return false
	}
	id := m[1]
	sb, err := s.Store.Get(r.Context(), id)
	// DNS authorities are case-insensitive; browsers lowercase ULID hosts.
	if errors.Is(err, store.ErrNotFound) && isULID(id) {
		sb, err = s.Store.Get(r.Context(), strings.ToUpper(id))
	}
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		writeErr(w, 503, "preview runtime unavailable")
		return true
	}
	if sb.RuntimeProvider != "cube" {
		return false
	}
	id = sb.ID
	port, _ := strconv.Atoi(m[2])
	allowed := false
	for _, p := range sb.Ports {
		if p == port {
			allowed = true
		}
	}
	// A corrupt/misconfigured Ports row must never expose the supervisor.
	if !allowed || !sb.WebPort.Valid || int(sb.WebPort.Int64) != port || port < 1 || port > 65535 || port == 3031 || port == 49983 {
		http.NotFound(w, r)
		return true
	}
	if r.URL.IsAbs() && !strings.EqualFold(r.URL.Host, r.Host) {
		http.NotFound(w, r)
		return true
	}
	if r.URL.Path == "/__sandboxd/preview-auth" {
		s.cubePreviewAuthHandoff(w, r, sb.ID)
		return true
	}
	if sb.Visibility != "public" {
		owner, e := s.Store.GetWorkspaceOwner(r.Context(), id)
		if e != nil || owner.ExternalUserID == "" {
			writeErr(w, 403, "preview owner unavailable")
			return true
		}
		cookie := ""
		if c, e := r.Cookie("sandbox_preview"); e == nil {
			cookie = c.Value
		}
		_, reason := auth.CheckPreviewAccess(cookie, id, owner.ExternalUserID, s.authCfg().PreviewSecrets, time.Now())
		if reason != "" {
			s.forwardAuthDeny(w, r, id, r.Host, r.URL.RequestURI())
			return true
		}
		// A sibling app must not drive authenticated writes or WebSockets into
		// this private app using cookies scoped to the shared preview domain.
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			scheme, _ := s.previewScheme()
			if e != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, r.Host) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				writeErr(w, 403, "preview origin not allowed")
				return true
			}
		}
	}
	if r.URL.Path == "/__sandboxd/preview-ready" {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeErr(w, 405, "method not allowed")
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return true
	}
	if s.Cube == nil || s.Secrets == nil {
		writeErr(w, 503, "preview runtime unavailable")
		return true
	}
	target, err := cubePreviewOrigin(s.CubeProxyURL)
	if err != nil {
		writeErr(w, 503, "preview runtime unavailable")
		return true
	}
	b, err := s.Store.GetRuntimeBinding(r.Context(), id)
	if err != nil || b.Provider != "cube" || b.SandboxID != id {
		writeErr(w, 503, "preview runtime unavailable")
		return true
	}
	upstreamHost, err := cubePreviewHost(port, b.RuntimeID, b.Domain)
	if err != nil || !strings.EqualFold(b.Domain, s.CubeDomain) {
		writeErr(w, 503, "preview runtime unavailable")
		return true
	}
	plain, err := s.Secrets.Open(b.TokenCiphertext, b.TokenNonce)
	var credentials cubeCredentials
	if err != nil || json.Unmarshal(plain, &credentials) != nil || !cubePreviewHeaderToken(credentials.TrafficAccessToken) {
		writeErr(w, 503, "preview credential unavailable")
		return true
	}
	// Verify/renew the runtime lease before forwarding the original body.
	// Assets reuse a 30-second running lease instead of serializing a management
	// round trip and supervisor probe for every file. Never replay a failed POST.
	passive := passiveCubePreview(r)
	// Serialize activity registration against the idle/reclamation decision.
	// Release before ensureCubePreviewLease, which takes this same lock.
	if !passive && s.Inflight != nil {
		if s.Locks != nil {
			s.Locks.Lock(id)
		}
		s.Inflight.Enter(id)
		if s.Locks != nil {
			s.Locks.Unlock(id)
		}
		defer s.Inflight.Exit(id)
	}
	if passive {
		if err := s.checkPassiveCubePreview(r.Context(), id, b); err != nil {
			writePassiveCubeUnavailable(w)
			return true
		}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 140*time.Second)
		err = s.ensureCubePreviewLease(ctx, sb)
		cancel()
		if err != nil {
			if writeCubePreviewAdmission(w, r, err) {
				return true
			}
			writeErr(w, 502, "preview resume failed")
			return true
		}
		if err = s.Store.BumpLastActive(r.Context(), id, time.Now().UTC()); err != nil {
			writeErr(w, 503, "preview activity unavailable")
			return true
		}
	}
	scheme, _ := s.previewScheme()
	proxy := &httputil.ReverseProxy{
		Transport:     cubePreviewTransport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = upstreamHost
			stripCubePreviewHeaders(pr.Out.Header)
			pr.Out.Header.Del("Cookie")
			for _, c := range pr.In.Cookies() {
				if !cubePlatformCookie(c.Name) {
					pr.Out.AddCookie(c)
				}
			}
			pr.Out.Header.Set("Cube-Traffic-Access-Token", credentials.TrafficAccessToken)
			pr.Out.Header.Set("X-Forwarded-Host", r.Host)
			pr.Out.Header.Set("X-Forwarded-Proto", scheme)
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode >= 500 {
				s.cubePreviewLeases.Delete(id)
			}
			stripCubePreviewResponseHeaders(resp.Header)
			if sb.Visibility != "public" {
				resp.Header.Set("Cache-Control", "private, no-store")
			}
			// Absolute redirects emitted against the internal routing authority
			// must keep the browser on its authenticated stable preview host.
			if location, e := url.Parse(resp.Header.Get("Location")); e == nil && strings.EqualFold(location.Host, upstreamHost) {
				location.Host = r.Host
				location.Scheme = scheme
				resp.Header.Set("Location", location.String())
			}
			cookies := resp.Cookies()
			resp.Header.Del("Set-Cookie")
			for _, c := range cookies {
				if cubePlatformCookie(c.Name) {
					continue
				}
				// Guest applications cannot set cookies on sibling sandbox hosts.
				c.Domain = ""
				resp.Header.Add("Set-Cookie", c.String())
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.cubePreviewLeases.Delete(id)
			writeErr(w, 502, "preview upstream unavailable")
		},
	}
	// Ordinary streams retain their registered activity until ServeHTTP returns.
	proxy.ServeHTTP(w, r)
	return true
}

// cubePreviewAuthHandoff consumes the signed service-issued capability only at
// the stable preview origin. Neither the token nor this reserved URL reaches
// user code, and the browser is immediately redirected to a clean local path.
func (s *Server) cubePreviewAuthHandoff(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeErr(w, 405, "method not allowed")
		return
	}
	query := r.URL.Query()
	// The logging middleware records only URL.Path; clear residual query data
	// before any downstream logger/error handler has a chance to inspect it.
	r.URL.RawQuery = ""
	r.RequestURI = r.URL.EscapedPath()
	values := query["token"]
	if len(values) != 1 {
		writeErr(w, 401, "invalid preview access token")
		return
	}
	owner, err := s.Store.GetWorkspaceOwner(r.Context(), id)
	if err != nil || owner.ExternalUserID == "" {
		writeErr(w, 403, "preview owner unavailable")
		return
	}
	claims, reason := auth.CheckPreviewAccess(values[0], id, owner.ExternalUserID, s.authCfg().PreviewSecrets, time.Now())
	if reason != "" {
		writeErr(w, 401, "invalid preview access token")
		return
	}
	redirect := "/"
	if values := query["path"]; len(values) > 1 {
		writeErr(w, 400, "invalid preview return path")
		return
	} else if len(values) == 1 {
		redirect = values[0]
	}
	if !validCubePreviewReturnPath(redirect) {
		writeErr(w, 400, "invalid preview return path")
		return
	}
	maxAge := int(claims.Exp - time.Now().Unix())
	if maxAge > 300 {
		maxAge = 300
	}
	if maxAge < 1 {
		writeErr(w, 401, "invalid preview access token")
		return
	}
	scheme, _ := s.previewScheme()
	sameSite := http.SameSiteLaxMode
	if scheme == "https" {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{Name: "sandbox_preview", Value: values[0], Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: scheme == "https", SameSite: sameSite})
	http.Redirect(w, r, redirect, http.StatusFound)
}

func validCubePreviewReturnPath(raw string) bool {
	controls := func(s string) bool {
		return strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "\\") || controls(raw) {
		return false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" || strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, "\\") || controls(u.Path) {
		return false
	}
	return path.Clean(u.Path) != "/__sandboxd/preview-auth"
}

func (s *Server) ensureCubePreviewLease(ctx context.Context, sb *store.Sandbox) error {
	valid := func(sb *store.Sandbox) bool {
		if sb.Status != "running" {
			return false
		}
		lease, ok := s.cubePreviewLeases.Load(sb.ID)
		return ok && time.Now().Before(lease.(time.Time))
	}
	if valid(sb) {
		return nil
	}
	if s.Locks != nil {
		s.Locks.Lock(sb.ID)
		defer s.Locks.Unlock(sb.ID)
	}
	latest, err := s.Store.Get(ctx, sb.ID)
	if err != nil {
		return err
	}
	if latest.RuntimeProvider != "cube" {
		return errors.New("runtime provider changed")
	}
	if valid(latest) {
		return nil
	}
	if err := s.connectCube(ctx, sb.ID, 3600); err != nil {
		s.cubePreviewLeases.Delete(sb.ID)
		return err
	}
	s.cubePreviewLeases.Store(sb.ID, time.Now().Add(30*time.Second))
	return nil
}

func cubePreviewOrigin(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("invalid CubeProxy origin")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid CubeProxy port")
		}
	}
	u.Path = ""
	return u, nil
}

func cubePreviewHost(port int, id, domain string) (string, error) {
	label := fmt.Sprintf("%d-%s", port, id)
	if len(label) > 63 || !cubePreviewDNSLabel.MatchString(label) || domain == "" || len(domain) > 253 {
		return "", errors.New("invalid Cube proxy host")
	}
	for _, part := range strings.Split(domain, ".") {
		if len(part) > 63 || !cubePreviewDNSLabel.MatchString(part) {
			return "", errors.New("invalid Cube proxy domain")
		}
	}
	return label + "." + domain, nil
}

func cubePreviewHeaderToken(token string) bool {
	return token != "" && len(token) <= 4096 && strings.IndexFunc(token, func(r rune) bool { return r < 0x21 || r > 0x7e }) < 0
}

func cubePlatformCookie(name string) bool {
	name = strings.ToLower(name)
	// Reserve only cookies actually consumed by sandboxd. Framework session
	// names such as authjs.session-token belong to user-built applications.
	return name == "sandbox_preview" || name == auth.SessionCookie
}

func stripCubePreviewHeaders(h http.Header) {
	for k := range h {
		l := strings.ToLower(k)
		// Authorization/X-API-Key belong to the application. The preview gate
		// consumes only sandbox_preview and never injects management credentials.
		if l == "proxy-authorization" || l == "forwarded" || l == "x-real-ip" || strings.HasPrefix(l, "x-forwarded-") || strings.HasPrefix(l, "x-sandbox-") || strings.HasPrefix(l, "x-cube-") || strings.HasPrefix(l, "cube-") || strings.HasPrefix(l, "e2b-") {
			h.Del(k)
		}
	}
}

func stripCubePreviewResponseHeaders(h http.Header) {
	for k := range h {
		l := strings.ToLower(k)
		if l == "authorization" || l == "x-api-key" || l == "proxy-authenticate" || strings.HasPrefix(l, "x-sandbox-") || strings.HasPrefix(l, "x-cube-") || strings.HasPrefix(l, "cube-") || strings.HasPrefix(l, "e2b-") {
			h.Del(k)
		}
	}
}
