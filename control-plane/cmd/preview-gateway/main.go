// preview-gateway serves application traffic on its compute worker. The central
// controller still authorizes every request; no permission or placement cache
// can make a private project public or route writes to an old runtime.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type config struct{ Listen, Controller, Worker, Key, Origin, Domain, Peer, PeerWorker string }
type request struct {
	Host    string      `json:"host"`
	Method  string      `json:"method"`
	URI     string      `json:"uri"`
	Headers http.Header `json:"headers"`
}
type route struct {
	Host    string `json:"host"`
	Token   string `json:"token"`
	Private bool   `json:"private"`
	Passive bool   `json:"passive"`
}
type gateway struct {
	cfg       config
	client    *http.Client
	transport http.RoundTripper
	origin    *url.URL
	host      *regexp.Regexp
	heartbeat time.Duration
}

func newGateway(c config) (*gateway, error) {
	u, e := url.Parse(c.Origin)
	if e != nil || u.Scheme != "http" || u.Host == "" || u.Path != "" || u.User != nil || u.RawQuery != "" {
		return nil, errors.New("invalid local origin")
	}
	if c.Domain == "" || c.Worker == "" || len(c.Key) < 32 {
		return nil, errors.New("missing gateway configuration")
	}
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConns: 256, MaxIdleConnsPerHost: 128, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 140 * time.Second, DisableCompression: true}
	return &gateway{cfg: c, origin: u, host: regexp.MustCompile(`(?i)^s-[0-9a-z]{1,128}-[0-9]{1,5}\.` + regexp.QuoteMeta(c.Domain) + `$`), client: &http.Client{Transport: tr, Timeout: 150 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: tr, heartbeat: 20 * time.Second}, nil
}

func (g *gateway) authorize(r *http.Request) (*http.Response, *route, error) {
	h := make(http.Header)
	for _, name := range []string{"Cookie", "Origin", "Accept", "Upgrade", "Connection", "Sec-WebSocket-Protocol"} {
		for _, v := range r.Header.Values(name) {
			h.Add(name, v)
		}
	}
	b, _ := json.Marshal(request{r.Host, r.Method, r.URL.RequestURI(), h})
	req, e := http.NewRequestWithContext(r.Context(), "POST", g.cfg.Controller+"/preview-gateway", bytes.NewReader(b))
	if e != nil {
		return nil, nil, e
	}
	req.Header.Set("Authorization", "Bearer "+g.cfg.Key)
	req.Header.Set("X-Preview-Worker", g.cfg.Worker)
	req.Header.Set("Content-Type", "application/json")
	res, e := g.client.Do(req)
	if e != nil {
		return nil, nil, e
	}
	if res.StatusCode != 200 {
		return res, nil, nil
	}
	defer res.Body.Close()
	var v route
	if json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&v) != nil || v.Host == "" || v.Token == "" || strings.ContainsAny(v.Host+v.Token, "\r\n") {
		return nil, nil, errors.New("invalid authorization response")
	}
	return nil, &v, nil
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
func strip(h http.Header) {
	for k := range h {
		n := strings.ToLower(k)
		if strings.HasPrefix(n, "cube-") || strings.HasPrefix(n, "e2b-") || strings.HasPrefix(n, "x-cube-") || strings.HasPrefix(n, "x-sandbox-") || strings.HasPrefix(n, "x-forwarded-") || strings.HasPrefix(n, "x-preview-") || n == "forwarded" || n == "x-real-ip" || n == "proxy-authorization" {
			h.Del(k)
		}
	}
}
func platformCookie(n string) bool {
	return strings.EqualFold(n, "sandbox_preview") || strings.EqualFold(n, "sbx_session")
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && (r.Host == "127.0.0.1:8095" || r.Host == "localhost:8095") {
		w.Write([]byte("ok\n"))
		return
	}
	if !g.host.MatchString(r.Host) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)
	res, v, e := g.authorize(r)
	if e != nil {
		http.Error(w, "Preview temporarily unavailable", 502)
		return
	}
	if res != nil {
		if res.StatusCode == 409 && res.Header.Get("X-Preview-Worker-Target") == g.cfg.PeerWorker && g.cfg.Peer != "" && r.Header.Get("X-Preview-Forwarded") == "" {
			res.Body.Close()
			peer, err := url.Parse(g.cfg.Peer)
			if err != nil {
				http.Error(w, "Preview routing unavailable", 502)
				return
			}
			p := &httputil.ReverseProxy{Transport: g.transport, FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(peer)
				pr.Out.Host = r.Host
				pr.Out.Header.Set("X-Preview-Forwarded", "1")
			}, ErrorLog: log.New(io.Discard, "", 0)}
			p.ServeHTTP(w, r)
			return
		}
		defer res.Body.Close()
		res.Header.Del("X-Preview-Worker-Target")
		copyHeaders(w.Header(), res.Header)
		w.WriteHeader(res.StatusCode)
		io.Copy(w, io.LimitReader(res.Body, 65536))
		return
	}
	// Active long-lived streams refresh central activity and recheck permission.
	// Vite HMR remains passive, preserving normal idle reclamation.
	if !v.Passive {
		go func() {
			tick := time.NewTicker(g.heartbeat)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					resp, next, err := g.authorize(r)
					if resp != nil {
						resp.Body.Close()
					}
					if err != nil || next == nil || next.Host != v.Host || next.Token != v.Token {
						cancel()
						return
					}
				}
			}
		}()
	}
	p := &httputil.ReverseProxy{Transport: g.transport, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0), Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(g.origin)
		pr.Out.Host = v.Host
		strip(pr.Out.Header)
		pr.Out.Header.Del("Cookie")
		for _, c := range pr.In.Cookies() {
			if !platformCookie(c.Name) {
				pr.Out.AddCookie(c)
			}
		}
		pr.Out.Header.Set("Cube-Traffic-Access-Token", v.Token)
		pr.Out.Header.Set("X-Forwarded-Host", r.Host)
		pr.Out.Header.Set("X-Forwarded-Proto", "https")
	}, ModifyResponse: func(res *http.Response) error {
		isViteHTML := strings.Contains(res.Header.Get("Content-Type"), "text/html") && strings.Contains(res.Header.Get("Cache-Control"), "no-cache") && strings.Contains(strings.Join(res.Header.Values("Vary"), ","), "Origin")
		strip(res.Header)
		res.Header.Del("Authorization")
		res.Header.Del("X-API-Key")
		res.Header.Del("Proxy-Authenticate")
		if v.Private {
			res.Header.Set("Cache-Control", "private, no-store")
		}
		if u, e := url.Parse(res.Header.Get("Location")); e == nil && strings.EqualFold(u.Host, v.Host) {
			u.Host = r.Host
			u.Scheme = "https"
			res.Header.Set("Location", u.String())
		}
		cookies := res.Cookies()
		res.Header.Del("Set-Cookie")
		for _, c := range cookies {
			if !platformCookie(c.Name) {
				c.Domain = ""
				res.Header.Add("Set-Cookie", c.String())
			}
		}
		if strings.Contains(res.Header.Get("Content-Type"), "text/html") {
			cc := res.Header.Get("Cache-Control")
			if cc != "" {
				cc += ", "
			}
			res.Header.Set("Cache-Control", cc+"no-transform")
		}
		if isViteHTML {
			return g.preload(res, r, v)
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "Preview temporarily unavailable", 502)
	}}
	p.ServeHTTP(w, r)
}

func main() {
	b, e := os.ReadFile(os.Getenv("PREVIEW_GATEWAY_CONFIG"))
	if e != nil {
		log.Fatal("gateway config unavailable")
	}
	var c config
	if json.Unmarshal(b, &c) != nil {
		log.Fatal("invalid gateway config")
	}
	g, e := newGateway(c)
	if e != nil {
		log.Fatal(e)
	}
	s := &http.Server{Addr: c.Listen, Handler: g, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32768}
	log.Fatal(s.ListenAndServe())
}
