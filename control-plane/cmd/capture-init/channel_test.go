package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var captureTestToken = strings.Repeat("ab", 32)

func initCapture(t *testing.T, b *captureBootstrap, token string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"envVars": map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": token}})
	req := httptest.NewRequest("POST", "/init", bytes.NewReader(body))
	res := httptest.NewRecorder()
	b.bootstrap(res, req)
	return res.Code
}
func checkBootstrap(b *captureBootstrap, path string) int {
	res := httptest.NewRecorder()
	b.bootstrap(res, httptest.NewRequest("GET", path, nil))
	return res.Code
}
func TestSnapshotReadinessPrecedesCredentialInitialization(t *testing.T) {
	b := &captureBootstrap{}
	if got := checkBootstrap(b, "/health"); got != 503 {
		t.Fatalf("cold health=%d", got)
	}
	if got := initCapture(t, b, captureTestToken); got != 503 {
		t.Fatalf("cold init=%d", got)
	}
	b.ready = true
	if got := checkBootstrap(b, "/snapshot-ready"); got != 200 {
		t.Fatalf("warm pristine=%d", got)
	}
	if got := initCapture(t, b, captureTestToken); got != 204 {
		t.Fatalf("init=%d", got)
	}
	if got := checkBootstrap(b, "/snapshot-ready"); got != 503 {
		t.Fatalf("initialized snapshot must fail: %d", got)
	}
	if got := checkBootstrap(b, "/health"); got != 200 {
		t.Fatalf("Cubelet health afterinit=%d", got)
	}
	if got := initCapture(t, b, captureTestToken); got != 204 {
		t.Fatalf("idempotent init=%d", got)
	}
	if got := initCapture(t, b, strings.Repeat("cd", 32)); got != 409 {
		t.Fatalf("token replacement=%d", got)
	}
}
func TestInitRefusesCredentialsBeforeReadyAndUnexpectedConfiguration(t *testing.T) {
	for _, body := range []string{
		`{"envVars":{"RUNTIMED_HTTP_ADDR":":3031","RUNTIMED_HTTP_TOKEN":"short"}}`,
		`{"envVars":{"RUNTIMED_HTTP_ADDR":"127.0.0.1:3031","RUNTIMED_HTTP_TOKEN":"` + captureTestToken + `"}}`,
		`{"envVars":{"RUNTIMED_HTTP_ADDR":":3031","RUNTIMED_HTTP_TOKEN":"` + captureTestToken + `","OPENAI_API_KEY":"not-permitted"}}`,
		strings.Repeat("x", 8193),
	} {
		b := &captureBootstrap{ready: true}
		res := httptest.NewRecorder()
		b.bootstrap(res, httptest.NewRequest("POST", "/init", strings.NewReader(body)))
		if res.Code != 400 || b.initialized {
			t.Fatal("invalid init accepted")
		}
	}
	b := &captureBootstrap{ready: true}
	r := httptest.NewRequest("POST", "/init", nil)
	r.Header.Set("Origin", "https://page.test")
	w := httptest.NewRecorder()
	b.bootstrap(w, r)
	if w.Code != 403 {
		t.Fatalf("browser bootstrap=%d", w.Code)
	}
}
func TestControlAuthenticationCannotConsumePristineWorker(t *testing.T) {
	b := &captureBootstrap{ready: true}
	initCapture(t, b, captureTestToken)
	for _, token := range []string{"", "Bearer wrong"} {
		req := httptest.NewRequest("GET", "/capture/channel", nil)
		req.Header.Set("Authorization", token)
		res := httptest.NewRecorder()
		b.control(res, req)
		if res.Code != 401 || b.used {
			t.Fatal("unauthenticated control accepted")
		}
	}
	req := httptest.NewRequest("GET", "/capture/channel", nil)
	req.Header.Set("Authorization", "Bearer "+captureTestToken)
	req.Header.Set("Origin", "https://page.test")
	res := httptest.NewRecorder()
	b.control(res, req)
	if res.Code != 403 || b.used {
		t.Fatal("browser control accepted")
	}
	req = httptest.NewRequest("GET", "/files/content", nil)
	req.Header.Set("Authorization", "Bearer "+captureTestToken)
	res = httptest.NewRecorder()
	b.control(res, req)
	if res.Code != 404 {
		t.Fatal("capture supervisor exposed filesystem endpoint")
	}
}
func TestCaptureStreamBridgesOneJobAndRejectsReuse(t *testing.T) {
	worker, guest := net.Pipe()
	defer guest.Close()
	b := &captureBootstrap{ready: true, worker: worker, timeout: time.Second}
	initCapture(t, b, captureTestToken)
	server := httptest.NewServer(http.HandlerFunc(b.control))
	defer server.Close()
	headers := http.Header{"Authorization": []string{"Bearer " + captureTestToken}}
	ws, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/capture/channel", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if response.StatusCode != 101 {
		t.Fatal(response.StatusCode)
	}
	kind, ready, err := ws.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(ready) != "{\"type\":\"ready\"}\n" {
		t.Fatal("missing ready frame", err)
	}
	job := []byte("{\"type\":\"capture\"}\n")
	received := make(chan error, 1)
	go func() {
		body := make([]byte, len(job))
		_, e := io.ReadFull(guest, body)
		if e == nil && !bytes.Equal(body, job) {
			e = io.ErrUnexpectedEOF
		}
		if e == nil {
			_, e = guest.Write([]byte("{\"type\":\"result\"}\n"))
		}
		received <- e
	}()
	if ws.WriteMessage(websocket.BinaryMessage, job) != nil {
		t.Fatal("cannot send capture")
	}
	_, data, err := ws.ReadMessage()
	if err != nil || string(data) != "{\"type\":\"result\"}\n" {
		t.Fatal("missing result", err)
	}
	if err = <-received; err != nil {
		t.Fatal(err)
	}
	_ = ws.Close()
	_, response, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/capture/channel", headers)
	if err == nil || response == nil || response.StatusCode != 409 {
		t.Fatal("capture connection was reused")
	}
}
func TestCaptureStreamDeadlineAndBinaryChunkLimit(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "chunk"}[oversized], func(t *testing.T) {
			worker, guest := net.Pipe()
			defer guest.Close()
			b := &captureBootstrap{ready: true, worker: worker, timeout: 50 * time.Millisecond}
			initCapture(t, b, captureTestToken)
			server := httptest.NewServer(http.HandlerFunc(b.control))
			defer server.Close()
			ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/capture/channel", http.Header{"Authorization": []string{"Bearer " + captureTestToken}})
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			_, _, err = ws.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			if oversized {
				_ = ws.WriteMessage(websocket.BinaryMessage, make([]byte, captureChunk+1))
			}
			_ = ws.SetReadDeadline(time.Now().Add(time.Second))
			if _, _, err = ws.ReadMessage(); err == nil {
				t.Fatal("unbounded channel survived")
			}
		})
	}
}
func TestCaptureLineLimitSpansWebsocketChunks(t *testing.T) {
	budget := captureBytes{}
	for i := 0; i < captureFrame/captureChunk; i++ {
		if budget.add(bytes.Repeat([]byte{'a'}, captureChunk)) != nil {
			t.Fatal("valid frame rejected")
		}
	}
	if budget.add([]byte{'a'}) == nil {
		t.Fatal("fragmentation bypassed frame limit")
	}
}
func TestWorkerEnvironmentNeverInheritsCredentials(t *testing.T) {
	t.Setenv("RUNTIMED_HTTP_TOKEN", captureTestToken)
	t.Setenv("OPENAI_API_KEY", "private-provider-key")
	env := strings.Join(workerEnvironment("/tmp/control.sock"), "\n")
	if strings.Contains(env, captureTestToken) || strings.Contains(env, "private-provider") || strings.Contains(env, "RUNTIMED_HTTP_TOKEN") {
		t.Fatal("worker inherited control credentials")
	}
	if !strings.Contains(env, "CAPTURE_WORKER_SOCKET=/tmp/control.sock") {
		t.Fatal("missing private IPC")
	}
}
