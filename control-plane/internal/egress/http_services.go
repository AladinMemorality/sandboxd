package egress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// HTTPService is operator configuration for one app-specific fixed L7 origin.
// Routes are literal paths or contain one whole-segment {uuid} placeholder.
// DELETE is permitted only when explicitly configured for a matching route.
// It never changes generic public egress policy or permits opaque CONNECT.
type HTTPService struct {
	Origin string              `json:"origin"`
	Routes map[string][]string `json:"routes"`
}

const maxHTTPServiceBody int64 = 16 << 20

func (s HTTPService) Address() string {
	u, err := url.Parse(s.Origin)
	if err != nil {
		return ""
	}
	return u.Host
}
func serviceURL(s HTTPService) (*url.URL, error) {
	u, err := url.Parse(s.Origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrDenied
	}
	ap, err := netip.ParseAddrPort(u.Host)
	if err != nil || !ap.Addr().Is4() || !ap.Addr().IsPrivate() || ap.Port() == 0 || u.Host != ap.String() || s.Origin != "http://"+ap.String() {
		return nil, ErrDenied
	}
	return u, nil
}
func copyService(s HTTPService) (HTTPService, error) {
	if _, err := serviceURL(s); err != nil {
		return HTTPService{}, err
	}
	out := HTTPService{Origin: s.Origin, Routes: make(map[string][]string)}
	if len(s.Routes) == 0 || len(s.Routes) > 3 {
		return out, ErrDenied
	}
	total := 0
	for method, routes := range s.Routes {
		if (method != "GET" && method != "POST" && method != "DELETE") || len(routes) == 0 {
			return out, ErrDenied
		}
		seen := map[string]bool{}
		for _, route := range routes {
			total++
			if total > 32 || len(route) == 0 || len(route) > 512 || !strings.HasPrefix(route, "/") || route != path.Clean(route) || strings.ContainsAny(route, "%?\\#\r\n\x00\t ") || seen[route] || !validServiceRoutePlaceholder(route) {
				return out, ErrDenied
			}
			seen[route] = true
			out.Routes[method] = append(out.Routes[method], route)
		}
	}
	return out, nil
}

// Only one complete typed segment is variable; no regex, glob, partial segment
// or arbitrary placeholder name can expand an operator's configured route.
func validServiceRoutePlaceholder(route string) bool {
	count := 0
	for _, segment := range strings.Split(route, "/") {
		if segment == "{uuid}" {
			count++
		} else if strings.ContainsAny(segment, "{}*") {
			return false
		}
	}
	return count <= 1
}
func canonicalServiceUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
		} else if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}
func serviceRouteMatches(route, requested string) bool {
	prefix, suffix, variable := strings.Cut(route, "{uuid}")
	if !variable {
		return route == requested
	}
	if len(requested) != len(prefix)+36+len(suffix) || !strings.HasPrefix(requested, prefix) || !strings.HasSuffix(requested, suffix) {
		return false
	}
	return canonicalServiceUUID(requested[len(prefix) : len(prefix)+36])
}

// ParseHTTPServices validates exact numeric RFC1918 origins against the explicit
// management/local-worker inventory. Empty configuration disables the feature.
func ParseHTTPServices(raw string, p Policy) (map[string][]HTTPService, error) {
	result := map[string][]HTTPService{}
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return result, nil
	}
	if len(raw) > 64<<10 {
		return nil, errors.New("app HTTP service configuration exceeds limit")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&result); err != nil {
		return nil, errors.New("invalid app HTTP service configuration")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("invalid trailing app HTTP service configuration")
	}
	if len(result) == 0 {
		return map[string][]HTTPService{}, nil
	}
	if len(result) > 128 {
		return nil, ErrDenied
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	for app, services := range result {
		if len(app) != 26 || strings.Trim(app, "0123456789ABCDEFGHJKMNPQRSTVWXYZ") != "" || len(services) == 0 || len(services) > 8 {
			return nil, ErrDenied
		}
		seen := map[string]bool{}
		for i, s := range services {
			parsed, err := copyService(s)
			if err != nil {
				return nil, err
			}
			ap, _ := netip.ParseAddrPort(parsed.Address())
			for _, prefix := range p.ProtectedPrefixes {
				if prefix.Contains(ap.Addr()) {
					return nil, ErrDenied
				}
			}
			if seen[parsed.Address()] {
				return nil, ErrDenied
			}
			seen[parsed.Address()] = true
			result[app][i] = parsed
		}
	}
	return result, nil
}

// Handler permits only reviewed methods/paths, with no cookies, host credentials,
// redirects, DNS or ambient proxy discovery. Guest Authorization is copied only
// to this fixed origin. Online/offline callers must additionally fence app and
// runtime generation authorization before invoking this handler.
func (s HTTPService) Handler() http.Handler {
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, DisableKeepAlives: true, DisableCompression: true, MaxConnsPerHost: 4, ResponseHeaderTimeout: 120 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	return s.handler(transport)
}
func (s HTTPService) handler(transport http.RoundTripper) http.Handler {
	validated, err := copyService(s)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "service unavailable", 503) })
	}
	target, _ := serviceURL(validated)
	proxy := &httputil.ReverseProxy{
		Transport: transport, ErrorLog: log.New(io.Discard, "", 0), FlushInterval: -1,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = target.Host
			// Do not forward platform cookies, bridge/API tokens or forwarded identity.
			p.Out.Header = make(http.Header)
			for _, key := range []string{"Authorization", "Content-Type", "Accept", "Accept-Language"} {
				if values := p.In.Header.Values(key); len(values) > 0 {
					p.Out.Header[key] = append([]string{}, values...)
				}
			}
			p.Out.Header.Set("Accept-Encoding", "identity")
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return errors.New("redirect forbidden")
			}
			if resp.ContentLength > maxHTTPServiceBody {
				return errors.New("response body exceeds limit")
			}
			filtered := make(http.Header)
			for _, key := range []string{"Content-Type", "Content-Length", "Content-Encoding", "Cache-Control", "ETag", "Last-Modified"} {
				if values := resp.Header.Values(key); len(values) > 0 {
					filtered[http.CanonicalHeaderKey(key)] = append([]string{}, values...)
				}
			}
			resp.Header = filtered
			resp.Trailer = nil
			resp.Body = &boundedServiceBody{ReadCloser: resp.Body, remaining: maxHTTPServiceBody}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
			status := http.StatusBadGateway
			if errors.Is(e, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			http.Error(w, "fixed service unavailable", status)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := SourceIdentity(r.Context())
		allowed := false
		for _, route := range validated.Routes[r.Method] {
			if serviceRouteMatches(route, r.URL.Path) {
				allowed = true
			}
		}
		if !ok || identity.SandboxID == "" || identity.Generation == "" || !allowed || r.URL.IsAbs() || r.URL.Host != "" || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" || r.URL.Opaque != "" {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.ContentLength > maxHTTPServiceBody {
			http.Error(w, "body too large", 413)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		request := r.Clone(ctx)
		request.Body = &boundedServiceBody{ReadCloser: r.Body, remaining: maxHTTPServiceBody}
		defer request.Body.Close()
		proxy.ServeHTTP(w, request)
	})
}

type boundedServiceBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedServiceBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		var extra [1]byte
		n, e := b.ReadCloser.Read(extra[:])
		if n > 0 {
			return 0, errors.New("fixed service body exceeds " + strconv.FormatInt(maxHTTPServiceBody, 10))
		}
		return 0, e
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, e := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, e
}
