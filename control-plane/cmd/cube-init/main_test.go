package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBootstrapFreshTokenOnceAndRejectsRotation(t *testing.T) {
	var calls atomic.Int32
	token := strings.Repeat("a", 64)
	b := &bootstrap{start: func(env map[string]string) error {
		calls.Add(1)
		if env["RUNTIMED_HTTP_TOKEN"] != token {
			t.Error("wrong token")
		}
		return nil
	}}
	body := fmt.Sprintf(`{"envVars":{"RUNTIMED_HTTP_ADDR":":3031","RUNTIMED_HTTP_TOKEN":"%s"}}`, token)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			b.ServeHTTP(w, httptest.NewRequest("POST", "/init", strings.NewReader(body)))
			if w.Code != 204 {
				t.Errorf("init: %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("started %d supervisors", calls.Load())
	}
	w := httptest.NewRecorder()
	b.ServeHTTP(w, httptest.NewRequest("POST", "/init", strings.NewReader(strings.ReplaceAll(body, token, strings.Repeat("b", 64)))))
	if w.Code != 409 {
		t.Fatalf("allowed credential replacement: %d", w.Code)
	}
}

func TestBootstrapRejectsInvalidConfiguration(t *testing.T) {
	for _, body := range []string{`{}`, `{"envVars":{}}`, `{"envVars":{"RUNTIMED_HTTP_ADDR":":80","RUNTIMED_HTTP_TOKEN":"short"}}`, strings.Repeat("x", 9000), `{"envVars":{"RUNTIMED_HTTP_ADDR":":3031","RUNTIMED_HTTP_TOKEN":"` + strings.Repeat("a", 64) + `","LD_PRELOAD":"evil"}}`} {
		b := &bootstrap{start: func(map[string]string) error { t.Fatal("invalid config started process"); return nil }}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, httptest.NewRequest("POST", "/init", strings.NewReader(body)))
		if w.Code != 400 {
			t.Errorf("invalid config: %d", w.Code)
		}
	}
}
