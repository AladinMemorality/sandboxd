package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var entryScript = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc=["']([^"']+)["'][^>]*>`)

// Vite's dependency chunks commonly split named imports across lines. Missing
// those edges makes the browser discover another network round of modules.
var staticImport = regexp.MustCompile(`(?m)(?:\b(?:import|export)\s+[^;]*?\sfrom\s*|\bimport\s*)["']([^"']+)["']`)

func modulePath(parent, raw string) string {
	if !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "./") && !strings.HasPrefix(raw, "../") {
		return ""
	}
	base, _ := url.Parse("https://preview.invalid" + parent)
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	u = base.ResolveReference(u)
	if u.Host != "preview.invalid" || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return ""
	}
	p := u.Path
	if !(strings.HasPrefix(p, "/src/") || strings.HasPrefix(p, "/node_modules/") || p == "/@vite/client" || p == "/@react-refresh") {
		return ""
	}
	if !(strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".mjs") || strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx") || strings.HasSuffix(p, ".jsx") || strings.HasSuffix(p, ".css") || strings.HasPrefix(p, "/@")) {
		return ""
	}
	return u.RequestURI()
}

// Discover development imports over the worker's local connection. Only preload
// hints are added: source files, authorization, caching, and live edits are intact.
func (g *gateway) preload(res *http.Response, r *http.Request, v *route) error {
	if r.Method != "GET" || res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	enc := res.Header.Get("Content-Encoding")
	if enc != "" && enc != "gzip" {
		return nil
	}
	original := res.Body
	raw, e := io.ReadAll(io.LimitReader(original, 1024*1024+1))
	if e != nil {
		return e
	}
	if len(raw) > 1024*1024 {
		res.Body = &combinedBody{Reader: io.MultiReader(bytes.NewReader(raw), original), Closer: original}
		return nil
	}
	original.Close()
	res.Body = io.NopCloser(bytes.NewReader(raw))
	decoded := raw
	if enc == "gzip" {
		z, e := gzip.NewReader(bytes.NewReader(raw))
		if e != nil {
			return nil
		}
		decoded, e = io.ReadAll(io.LimitReader(z, 1024*1024+1))
		z.Close()
		if e != nil || len(decoded) > 1024*1024 {
			return nil
		}
	}
	if !bytes.Contains(decoded, []byte("/@vite/client")) {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 750*time.Millisecond)
	defer cancel()
	queue := []string{}
	seen := map[string]bool{}
	for _, m := range entryScript.FindAllSubmatch(decoded, -1) {
		if p := modulePath(r.URL.Path, string(m[1])); p != "" && !seen[p] {
			seen[p] = true
			queue = append(queue, p)
		}
	}
	hints := []string{}
	var mu sync.Mutex
	for len(queue) > 0 && len(seen) <= 64 && ctx.Err() == nil {
		batch := queue
		queue = nil
		var wg sync.WaitGroup
		slots := make(chan struct{}, 12)
		for _, p := range batch {
			p := p
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-slots }()
				req, e := http.NewRequestWithContext(ctx, "GET", g.origin.String()+p, nil)
				if e != nil {
					return
				}
				req.Host = v.Host
				req.Header.Set("Cube-Traffic-Access-Token", v.Token)
				req.Header.Set("Accept-Encoding", "identity")
				req.Header.Set("Sec-Fetch-Dest", "script")
				response, e := g.transport.RoundTrip(req)
				if e != nil {
					return
				}
				defer response.Body.Close()
				if response.StatusCode != 200 {
					return
				}
				body, e := io.ReadAll(io.LimitReader(response.Body, 3*1024*1024))
				if e != nil {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				hints = append(hints, p)
				for _, m := range staticImport.FindAllSubmatch(body, -1) {
					if child := modulePath(p, string(m[1])); child != "" && !seen[child] && len(seen) < 64 {
						seen[child] = true
						queue = append(queue, child)
					}
				}
			}()
		}
		wg.Wait()
	}
	if len(hints) == 0 {
		return nil
	}
	var links strings.Builder
	for _, p := range hints {
		links.WriteString(`<link rel="modulepreload" href="` + html.EscapeString(p) + `">`)
	}
	out := bytes.Replace(decoded, []byte("</head>"), []byte(links.String()+"</head>"), 1)
	res.Body = io.NopCloser(bytes.NewReader(out))
	res.ContentLength = -1
	res.Header.Del("Content-Length")
	res.Header.Del("Content-Encoding")
	res.Header.Del("ETag")
	res.Header.Del("Content-MD5")
	res.Header.Set("Cache-Control", "private, no-store, no-transform")
	return nil
}

type combinedBody struct {
	io.Reader
	io.Closer
}
