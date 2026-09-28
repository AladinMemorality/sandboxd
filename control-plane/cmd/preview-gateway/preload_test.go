package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDevelopmentModulePreloadsStayInsideAuthorizedApp(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "3000-runtime.cube.test" || r.Header.Get("Cube-Traffic-Access-Token") != "traffic" {
			t.Error("unscoped preload")
		}
		switch r.URL.Path {
		case "/src/main.tsx":
			io.WriteString(w, `import {x} from './child.ts'; import 'https://other.invalid/private.js';`)
		case "/src/child.ts":
			io.WriteString(w, `export const x=1`)
		case "/@vite/client":
			io.WriteString(w, `export const vite=1`)
		default:
			t.Errorf("unexpected prefetch %s", r.URL.Path)
		}
	}))
	defer origin.Close()
	g, e := newGateway(config{Controller: "http://127.0.0.1", Origin: origin.URL, Worker: "vps", Key: strings.Repeat("k", 32), Domain: "baarcha.tn"})
	if e != nil {
		t.Fatal(e)
	}
	body := `<html><head><script type="module" src="/@vite/client"></script><script type="module" src="/src/main.tsx"></script></head><body>unchanged</body></html>`
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
	r := httptest.NewRequest("GET", "https://s-abc-3000.baarcha.tn/", nil)
	if e = g.preload(response, r, &route{Host: "3000-runtime.cube.test", Token: "traffic"}); e != nil {
		t.Fatal(e)
	}
	out, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(out), `rel="modulepreload" href="/src/child.ts"`) || !strings.Contains(string(out), `<body>unchanged</body>`) || strings.Contains(string(out), "other.invalid") {
		t.Fatal("incorrect development preloads")
	}
	for _, s := range []string{"//evil.test/src/a.js", "https://evil.test/src/a.js", "/api/delete.js", "/src/../../private.js"} {
		if modulePath("/src/main.tsx", s) != "" {
			t.Fatal("unsafe prefetch", s)
		}
	}
}
