package api

// Production builds are served by a dedicated host service. This boundary
// never creates, wakes, renews or edits a development guest.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func (s *Server) productionOrigin() (*url.URL, error) {
	u, err := url.Parse(s.ProductionURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(s.ProductionToken) < 32 {
		return nil, errors.New("production hosting unavailable")
	}
	return u, nil
}

func (s *Server) productionActive(ctx context.Context, appID string) (bool, error) {
	if s.ProductionURL == "" {
		return false, nil
	}
	target, err := s.productionOrigin()
	if err != nil {
		return false, err
	}
	target.Path = "/v1/projects/" + url.PathEscape(appID)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Baarcha-Hosting-Token", s.ProductionToken)
	client := &http.Client{Transport: cubePreviewTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return false, errors.New("production status unavailable")
	}
	var state struct {
		Current json.RawMessage `json:"current"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20)).Decode(&state); err != nil {
		return false, err
	}
	if string(state.Current) == "null" {
		return false, nil
	}
	var current string
	if json.Unmarshal(state.Current, &current) != nil || current == "" {
		return false, errors.New("invalid production status")
	}
	return true, nil
}

func (s *Server) v1ProductionHosting(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.GetAppForOwner(r.Context(), r.PathValue("id"), tenantToken(r)); err != nil {
		writeV1Err(w, 404, "not_found", "no such app")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	active, err := s.productionActive(r.Context(), r.PathValue("id"))
	if err != nil {
		writeV1Err(w, 503, "production_unavailable", "production hosting unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"configured": s.ProductionURL != "", "active": active})
}

func (s *Server) tryProductionPreview(w http.ResponseWriter, r *http.Request, sb *store.Sandbox) bool {
	if s.ProductionURL == "" || !sb.AppID.Valid {
		return false
	}
	active, err := s.productionActive(r.Context(), sb.AppID.String)
	if err != nil {
		writeErr(w, 503, "production hosting unavailable")
		return true
	}
	if !active {
		return false
	}
	target, err := s.productionOrigin()
	if err != nil {
		writeErr(w, 503, "production hosting unavailable")
		return true
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(out *http.Request) {
		originalDirector(out)
		stripCubePreviewHeaders(out.Header)
		out.URL.Path = "/serve/" + sb.AppID.String + r.URL.Path
		out.URL.RawPath = ""
		for name := range out.Header {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-baarcha-hosting-") || strings.HasPrefix(lower, "x-sandbox") || strings.HasPrefix(lower, "cube-") {
				out.Header.Del(name)
			}
		}
		out.Header.Set("X-Baarcha-Hosting-Token", s.ProductionToken)
		scheme, _ := s.previewScheme()
		out.Header.Set("X-Forwarded-Host", r.Host)
		out.Header.Set("X-Forwarded-Proto", scheme)
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		stripCubePreviewResponseHeaders(response.Header)
		cookies := response.Cookies()
		response.Header.Del("Set-Cookie")
		for _, cookie := range cookies {
			if cubePlatformCookie(cookie.Name) || strings.EqualFold(cookie.Name, "punicas_platform") {
				continue
			}
			cookie.Domain = ""
			response.Header.Add("Set-Cookie", cookie.String())
		}
		if sb.Visibility != "public" {
			response.Header.Set("Cache-Control", "private, no-store")
		}
		return nil
	}
	proxy.Transport = cubePreviewTransport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { writeErr(w, 503, "production app unavailable") }
	proxy.ServeHTTP(w, r)
	return true
}
