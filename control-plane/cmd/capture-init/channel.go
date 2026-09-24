package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

const captureChunk = 64 << 10
const captureFrame = 20 << 20
const captureTransfer = 192 << 20

// The only credential holder is this protected Go supervisor. The Node worker
// receives neither this token nor a platform/Cube API credential.
type captureBootstrap struct {
	mu          sync.Mutex
	ready       bool
	initialized bool
	used        bool
	token       [32]byte
	worker      net.Conn
	timeout     time.Duration
}

func (b *captureBootstrap) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser access refused", 403)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.Method == "GET" && (r.URL.Path == "/health" || r.URL.Path == "/snapshot-ready") {
		if !b.ready || (r.URL.Path == "/snapshot-ready" && b.initialized) {
			http.Error(w, "not pristine and ready", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ready": b.ready, "initialized": b.initialized})
		return
	}
	if r.Method != "POST" || r.URL.Path != "/init" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	var req struct {
		EnvVars map[string]string `json:"envVars"`
	}
	if err != nil || len(body) > 8192 || json.Unmarshal(body, &req) != nil || len(req.EnvVars) != 2 || req.EnvVars["RUNTIMED_HTTP_ADDR"] != ":3031" || runtime.ValidateRemoteToken(req.EnvVars["RUNTIMED_HTTP_TOKEN"]) != nil {
		http.Error(w, "invalid capture init", 400)
		return
	}
	if !b.ready {
		http.Error(w, "browser not ready", 503)
		return
	}
	token := sha256.Sum256([]byte("Bearer " + req.EnvVars["RUNTIMED_HTTP_TOKEN"]))
	if b.initialized {
		if subtle.ConstantTimeCompare(token[:], b.token[:]) != 1 {
			http.Error(w, "already initialized", 409)
			return
		}
		w.WriteHeader(204)
		return
	}
	b.token = token
	b.initialized = true
	w.WriteHeader(204)
}

func (b *captureBootstrap) control(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser access refused", 403)
		return
	}
	token := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	b.mu.Lock()
	if !b.initialized || subtle.ConstantTimeCompare(token[:], b.token[:]) != 1 {
		b.mu.Unlock()
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/health" {
		ready := b.ready && !b.used
		b.mu.Unlock()
		if !ready {
			http.Error(w, "capture unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"ready\":true}\n")
		return
	}
	if r.Method != "GET" || r.URL.Path != "/capture/channel" {
		b.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if !b.ready || b.used {
		b.mu.Unlock()
		http.Error(w, "capture already consumed", 409)
		return
	}
	upgrader := websocket.Upgrader{ReadBufferSize: captureChunk, WriteBufferSize: captureChunk, HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		b.mu.Unlock()
		return
	}
	b.used = true
	peer := b.worker
	b.mu.Unlock()
	defer ws.Close()
	defer peer.Close()
	duration := b.timeout
	if duration <= 0 {
		duration = 45 * time.Second
	}
	deadline := time.Now().Add(duration)
	_ = peer.SetDeadline(deadline)
	_ = ws.SetReadDeadline(deadline)
	_ = ws.SetWriteDeadline(deadline)
	ws.SetReadLimit(captureChunk)
	if ws.WriteMessage(websocket.BinaryMessage, []byte("{\"type\":\"ready\"}\n")) != nil {
		return
	}
	completed := make(chan error, 2)
	transferred := &atomic.Int64{}
	go func() { completed <- copyCaptureToWebsocket(ws, peer, transferred) }()
	go func() { completed <- copyCaptureFromWebsocket(peer, ws, transferred) }()
	<-completed
	// Both copies have fixed bounds/deadlines. Closing both directions wakes the
	// other goroutine; no client can hold a renderer past its one-job lifetime.
	_ = ws.Close()
	_ = peer.Close()
	<-completed
}

type captureBytes struct {
	line, total int
	shared      *atomic.Int64
}

func (b *captureBytes) add(data []byte) error {
	b.total += len(data)
	if b.total > captureTransfer || (b.shared != nil && b.shared.Add(int64(len(data))) > captureTransfer) {
		return errors.New("capture transfer limit")
	}
	for _, v := range data {
		if v == '\n' {
			b.line = 0
		} else {
			b.line++
			if b.line > captureFrame {
				return errors.New("capture frame limit")
			}
		}
	}
	return nil
}
func copyCaptureToWebsocket(ws *websocket.Conn, peer io.Reader, transferred *atomic.Int64) error {
	buffer := make([]byte, captureChunk)
	budget := captureBytes{shared: transferred}
	for {
		n, err := peer.Read(buffer)
		if n > 0 {
			if e := budget.add(buffer[:n]); e != nil {
				return e
			}
			if e := ws.WriteMessage(websocket.BinaryMessage, buffer[:n]); e != nil {
				return e
			}
		}
		if err != nil {
			return err
		}
	}
}
func copyCaptureFromWebsocket(peer io.Writer, ws *websocket.Conn, transferred *atomic.Int64) error {
	budget := captureBytes{shared: transferred}
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		if kind != websocket.BinaryMessage {
			return errors.New("capture requires binary frames")
		}
		if err = budget.add(data); err != nil {
			return err
		}
		for len(data) > 0 {
			n, e := peer.Write(data)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			data = data[n:]
		}
	}
}
