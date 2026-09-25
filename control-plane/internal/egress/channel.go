package egress

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Identity comes only from the host's persisted binding, never from guest frames.
// Generation must change whenever a runtime binding is replaced.
type Identity struct {
	SandboxID  string
	Generation string
}
type identityKey struct{}

func SourceIdentity(ctx context.Context) (Identity, bool) {
	v, ok := ctx.Value(identityKey{}).(Identity)
	return v, ok
}

type HostOptions struct {
	Identity Identity
	Policy   Policy
	// Only fixed, trusted L7 callbacks. They must additionally verify live task /
	// bridge capabilities and bind them to SourceIdentity before invoking a service.
	Services map[string]http.Handler
	// Exact operator-selected private HTTP services for THIS persisted app.
	// These are parsed HTTP callbacks, never raw TCP or network allow rules.
	HTTPServices map[string]http.Handler
	// DialContext is an optional trusted test/transport hook. It receives only the
	// numeric policy-approved address. Production normally leaves it nil.
	DialContext func(context.Context, string, string) (net.Conn, error)
}

// RunHost owns conn until ctx cancellation or channel failure. Callers must dial
// the existing authenticated runtime endpoint and cancel the previous generation
// before replacement. Direct guest egress is unaffected.
func RunHost(ctx context.Context, conn *websocket.Conn, opts HostOptions) error {
	if opts.Identity.SandboxID == "" || opts.Identity.Generation == "" {
		conn.Close()
		return errors.New("host sandbox identity and generation required")
	}
	if err := opts.Policy.Validate(); err != nil {
		conn.Close()
		return err
	}
	opts.Policy.ProtectedPrefixes = append(opts.Policy.ProtectedPrefixes[:0:0], opts.Policy.ProtectedPrefixes...)
	opts.Policy.ProtectedDomains = append([]string(nil), opts.Policy.ProtectedDomains...)
	opts.Policy.Ports = append([]uint16(nil), opts.Policy.Ports...)
	services := make(map[string]http.Handler, len(opts.Services))
	for k, v := range opts.Services {
		if k != "model" && k != "bridge" && k != "motion" {
			conn.Close()
			return errors.New("unknown fixed service")
		}
		services[k] = v
	}
	opts.Services = services
	httpServices := make(map[string]http.Handler, len(opts.HTTPServices))
	if len(opts.HTTPServices) > 8 {
		conn.Close()
		return errors.New("too many scoped HTTP services")
	}
	for address, handler := range opts.HTTPServices {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || ap.String() != address || !ap.Addr().Is4() || !ap.Addr().IsPrivate() || ap.Port() == 0 || handler == nil {
			conn.Close()
			return errors.New("scoped service requires exact private IPv4 and port")
		}
		for _, prefix := range opts.Policy.ProtectedPrefixes {
			if prefix.Contains(ap.Addr()) {
				conn.Close()
				return errors.New("scoped HTTP service overlaps protected infrastructure")
			}
		}
		httpServices[address] = handler
	}
	s := newSession(ctx, conn, nil)
	s.onOpen = func(st *stream, f frame) {
		streamCtx, cancel := context.WithTimeout(st.ctx, 15*time.Minute)
		defer cancel()
		stop := context.AfterFunc(streamCtx, func() { st.Close() })
		defer stop()
		reject := func() { _ = s.send(frame{Type: "error", ID: st.id}); st.Close() }
		if f.Kind == "public" {
			// Match the literal bytes only. DNS, alternate numeric forms and
			// guest-selected ports cannot acquire the operator's service route.
			if handler := httpServices[net.JoinHostPort(f.Host, strconv.Itoa(int(f.Port)))]; handler != nil {
				if s.send(frame{Type: "opened", ID: st.id}) != nil {
					return
				}
				serveHTTPCallback(streamCtx, st, opts.Identity, handler, func(r *http.Request) bool {
					return (r.Method == "GET" || r.Method == "POST" || r.Method == "DELETE") && !r.URL.IsAbs() && r.URL.Host == "" &&
						r.URL.RawPath == "" && r.URL.RawQuery == "" && !r.URL.ForceQuery && r.URL.Fragment == ""
				})
				<-streamCtx.Done()
				return
			}
			dialCtx, cancelDial := context.WithTimeout(streamCtx, 10*time.Second)
			addr, err := opts.Policy.Destination(dialCtx, f.Host, f.Port)
			if err != nil {
				cancelDial()
				reject()
				return
			}
			dial := opts.DialContext
			if dial == nil {
				dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
			}
			c, err := dial(dialCtx, "tcp4", addr)
			cancelDial()
			if err != nil {
				reject()
				return
			}
			defer c.Close()
			closeConn := context.AfterFunc(streamCtx, func() { c.Close() })
			defer closeConn()
			if s.send(frame{Type: "opened", ID: st.id}) != nil {
				return
			}
			done := make(chan struct{}, 1)
			go func() {
				_, err := io.Copy(c, st)
				if cw, ok := c.(interface{ CloseWrite() error }); ok {
					_ = cw.CloseWrite()
				}
				if err != nil {
					st.Close()
				}
				done <- struct{}{}
			}()
			_, err = io.Copy(st, c)
			if err != nil {
				st.Close()
			} else {
				_ = st.CloseWrite()
			}
			<-done
		} else {
			h := opts.Services[f.Kind]
			if h == nil || f.Host != "" || f.Port != 0 {
				reject()
				return
			}
			if s.send(frame{Type: "opened", ID: st.id}) != nil {
				return
			}
			if f.Kind == "motion" {
				serveHTTPCallbackLimit(streamCtx, st, opts.Identity, h, func(r *http.Request) bool { _, _, allowed := motionRoute(r); return allowed }, MotionStudioWireLimit, []string{"Range"})
			} else {
				serveFixed(streamCtx, st, f.Kind, opts.Identity, h)
			}
		}
		// EOF is distinct from cancellation. Retain the bounded stream until the
		// receiver drains its buffered response and acknowledges by closing it.
		<-streamCtx.Done()
	}
	s.run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrDisconnected
}

type GuestOptions struct{ Authenticate func(*http.Request) bool }
type Guest struct {
	auth    func(*http.Request) bool
	mu      sync.Mutex
	current *session
	changed chan struct{}
	closed  bool
}

func NewGuest(opts GuestOptions) (*Guest, error) {
	if opts.Authenticate == nil {
		return nil, errors.New("channel authentication required")
	}
	return &Guest{auth: opts.Authenticate, changed: make(chan struct{})}, nil
}
func (g *Guest) notify() { close(g.changed); g.changed = make(chan struct{}) }
func (g *Guest) Ready() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.closed && g.current != nil && g.current.ctx.Err() == nil
}
func (g *Guest) WaitReady(ctx context.Context) error {
	for {
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			return ErrDisconnected
		}
		if g.current != nil && g.current.ctx.Err() == nil {
			g.mu.Unlock()
			return nil
		}
		ch := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}
func (g *Guest) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed {
		g.closed = true
		if g.current != nil {
			g.current.close()
		}
		g.notify()
	}
	return nil
}
func (g *Guest) ChannelHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.auth(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// The authenticated host client sends no browser Origin.
		up := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s := newSession(context.Background(), c, nil)
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			c.Close()
			return
		}
		old := g.current
		g.current = s
		g.notify()
		g.mu.Unlock()
		if old != nil {
			old.close()
		}
		s.run()
		g.mu.Lock()
		if g.current == s {
			g.current = nil
			g.notify()
		}
		g.mu.Unlock()
	})
}
func (g *Guest) open(ctx context.Context, kind, host string, port uint16) (*stream, error) {
	g.mu.Lock()
	s := g.current
	g.mu.Unlock()
	if s == nil {
		return nil, ErrDisconnected
	}
	return s.open(ctx, kind, host, port)
}

const maxServiceHeaders = 16 << 10
const maxServiceBody = 16 << 20

func readServiceRequest(st *stream) (*http.Request, error) {
	return readServiceRequestLimit(st, maxServiceBody)
}

func readServiceRequestLimit(st *stream, bodyLimit int64) (*http.Request, error) {
	br := bufio.NewReaderSize(st, maxServiceHeaders)
	var header bytes.Buffer
	for {
		line, err := br.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		if header.Len()+len(line) > maxServiceHeaders {
			return nil, errors.New("request headers too large")
		}
		header.Write(line)
		if bytes.Equal(line, []byte("\r\n")) {
			break
		}
	}
	r, err := http.ReadRequest(bufio.NewReader(io.MultiReader(bytes.NewReader(header.Bytes()), br)))
	if err != nil {
		return nil, err
	}
	if r.ContentLength > bodyLimit {
		return nil, errors.New("request body too large")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, bodyLimit)
	return r, nil
}
func validFixedPath(r *http.Request, kind string, id Identity) bool {
	if r.Method != "POST" || r.URL.IsAbs() || r.URL.Host != "" || r.URL.RawPath != "" || r.URL.Fragment != "" {
		return false
	}
	if kind == "bridge" {
		return r.URL.Path == "/api/bridge" && r.URL.RawQuery == ""
	}
	if r.URL.RawQuery != "" && r.URL.RawQuery != "beta=true" {
		return false
	}
	prefix := "/v1/cube-model/" + id.SandboxID + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || len(parts[0]) != 26 || (parts[1] != "v1/messages" && parts[1] != "v1/messages/count_tokens") {
		return false
	}
	for _, c := range parts[0] {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", c) {
			return false
		}
	}
	return true
}
func serveFixed(ctx context.Context, st *stream, kind string, id Identity, h http.Handler) {
	serveHTTPCallback(ctx, st, id, h, func(r *http.Request) bool { return validFixedPath(r, kind, id) })
}

func serveHTTPCallback(ctx context.Context, st *stream, id Identity, h http.Handler, valid func(*http.Request) bool) {
	serveHTTPCallbackLimit(ctx, st, id, h, valid, maxServiceBody, nil)
}

func serveHTTPCallbackLimit(ctx context.Context, st *stream, id Identity, h http.Handler, valid func(*http.Request) bool, bodyLimit int64, extraHeaders []string) {
	out := &streamResponse{stream: st, header: make(http.Header)}
	defer func() {
		if recover() != nil {
			st.Close()
			return
		}
		out.finish()
		_ = st.CloseWrite()
	}()
	r, err := readServiceRequestLimit(st, bodyLimit)
	if err != nil {
		http.Error(out, "invalid service request", 400)
		return
	}
	// Only the Motion capability admits HEAD. Preserve bounded upstream media
	// metadata without chunk framing or a response body for this method.
	out.head = bodyLimit == MotionStudioWireLimit && r.Method == http.MethodHead
	if !valid(r) {
		http.Error(out, "service request denied", 403)
		return
	}
	if bodyLimit == MotionStudioWireLimit {
		// A rejected/aborted large upload must not synchronously drain an
		// untrusted unfinished HTTP body while its sender waits for a response.
		r.Body = &motionStreamBody{body: r.Body, stream: st, length: r.ContentLength}
	}
	clean := make(http.Header)
	for _, name := range append([]string{"Content-Type", "Accept", "X-Api-Key", "X-Baarcha-Bridge", "Authorization", "Anthropic-Version", "Anthropic-Beta"}, extraHeaders...) {
		if values := r.Header.Values(name); len(values) > 0 {
			clean[name] = append([]string(nil), values...)
		}
	}
	r.Header = clean
	r.Host = "cube-fixed-service"
	r.RemoteAddr = ""
	r = r.WithContext(context.WithValue(ctx, identityKey{}, id))
	h.ServeHTTP(out, r)
}

// streamResponse streams SSE/JSON without aggregating a response in host memory.
type streamResponse struct {
	stream  *stream
	header  http.Header
	written bool
	head    bool
	err     error
}

func (w *streamResponse) Header() http.Header { return w.header }
func (w *streamResponse) WriteHeader(status int) {
	if w.written {
		return
	}
	w.written = true
	if status < 200 || status > 599 {
		status = 502
	}
	h := w.header.Clone()
	stripHopHeaders(h)
	if !w.head {
		h.Del("Content-Length")
		h.Set("Transfer-Encoding", "chunked")
	}
	h.Set("Connection", "close")
	var b bytes.Buffer
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	if err := h.Write(&b); err != nil {
		w.err = err
		w.stream.Close()
		return
	}
	b.WriteString("\r\n")
	if b.Len() > maxServiceHeaders {
		w.err = errors.New("response headers too large")
		w.stream.Close()
		return
	}
	_, w.err = w.stream.Write(b.Bytes())
}
func (w *streamResponse) Write(p []byte) (int, error) {
	if !w.written {
		w.WriteHeader(200)
	}
	if w.err != nil {
		return 0, w.err
	}
	if w.head {
		return len(p), nil
	}
	if len(p) == 0 {
		return 0, nil
	}
	_, w.err = fmt.Fprintf(w.stream, "%x\r\n", len(p))
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.stream.Write(p)
	if err == nil {
		_, err = io.WriteString(w.stream, "\r\n")
	}
	w.err = err
	return n, err
}
func (w *streamResponse) Flush() {
	if !w.written {
		w.WriteHeader(200)
	}
}
func (w *streamResponse) finish() {
	if !w.written {
		w.WriteHeader(200)
	}
	if w.err == nil && !w.head {
		_, w.err = io.WriteString(w.stream, "0\r\n\r\n")
	}
}
