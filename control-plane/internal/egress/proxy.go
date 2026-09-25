package egress

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func stripHopHeaders(h http.Header) {
	for _, value := range h.Values("Connection") {
		for _, key := range strings.Split(value, ",") {
			h.Del(strings.TrimSpace(key))
		}
	}
	for _, key := range []string{"Connection", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "Keep-Alive", "Transfer-Encoding", "TE", "Trailer", "Upgrade", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		h.Del(key)
	}
}
func loopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func destination(u *url.URL, defaultPort uint16) (string, uint16, error) {
	if u.User != nil || u.Fragment != "" || u.Host == "" {
		return "", 0, ErrDenied
	}
	host, err := canonicalHost(u.Hostname())
	if err != nil {
		return "", 0, err
	}
	port := defaultPort
	if u.Port() != "" {
		n, err := strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || n == 0 {
			return "", 0, ErrDenied
		}
		port = uint16(n)
	}
	return host, port, nil
}

// ProxyHandler serves proxy-aware applications on a loopback-only listener.
// HTTPS/WSS can use CONNECT. It does not transparently intercept sockets, fetch,
// UDP, database drivers, or clients that ignore HTTP(S)_PROXY.
func (g *Guest) ProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackRequest(r) {
			http.Error(w, "loopback only", 403)
			return
		}
		if r.Method == http.MethodConnect {
			g.connect(w, r)
			return
		}
		if r.URL.Scheme != "http" || r.Header.Get("Upgrade") != "" {
			http.Error(w, "absolute HTTP proxy request required", 400)
			return
		}
		host, port, err := destination(r.URL, 80)
		if err != nil {
			http.Error(w, "destination denied", 403)
			return
		}
		g.forward(w, r, "public", host, port)
	})
}

// ServiceHandler carries only regular relative HTTP requests to the named L7
// callback. A caller may StripPrefix a private loopback namespace before this.
func (g *Guest) ServiceHandler(kind string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackRequest(r) {
			http.Error(w, "loopback only", 403)
			return
		}
		if (kind != "model" && kind != "bridge") || r.URL.IsAbs() || r.Method == http.MethodConnect {
			http.Error(w, "invalid fixed service", 400)
			return
		}
		g.forward(w, r, kind, "", 0)
	})
}
func (g *Guest) forward(w http.ResponseWriter, r *http.Request, kind, host string, port uint16) {
	// A fixed callback can reject before an upload completes. Disable HTTP/1
	// auto-draining so its response can reach the client while the writer stops.
	_ = http.NewResponseController(w).EnableFullDuplex()
	// The upload can still be reading when an early response arrives. Its
	// cancellation sets a connection read deadline and closes the request body;
	// that HTTP/1 connection must not be reused for a subsequent request. In
	// particular, Go's body EOF background reader can otherwise race the next
	// request after full-duplex cleanup. Each request still shares the existing
	// authenticated reverse channel; only this guest-loopback TCP socket closes.
	if r.ProtoMajor == 1 {
		w.Header().Set("Connection", "close")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, 12*time.Second)
	st, err := g.open(openCtx, kind, host, port)
	openCancel()
	if err != nil {
		http.Error(w, "egress unavailable", 502)
		return
	}
	defer st.Close()
	stop := context.AfterFunc(ctx, func() { st.Close() })
	defer stop()
	req := r.Clone(ctx)
	req.RequestURI = ""
	req.Close = true
	stripHopHeaders(req.Header)
	if kind == "public" {
		req.Host = req.URL.Host
	} else {
		req.Host = "cube-fixed-service"
	}
	req.URL.Scheme = ""
	req.URL.Host = ""
	writeDone := make(chan error, 1)
	go func() {
		err := req.Write(st)
		if err != nil {
			st.Close()
		} else {
			_ = st.CloseWrite()
		}
		writeDone <- err
	}()
	defer func() {
		st.Close()
		select {
		case <-writeDone:
			if r.Body != nil {
				_ = r.Body.Close()
			}
		default:
			// A peer may reject before consuming its upload. Interrupt that
			// incomplete read before joining the request writer.
			_ = http.NewResponseController(w).SetReadDeadline(time.Now())
			if r.Body != nil {
				_ = r.Body.Close()
			}
			<-writeDone
		}
	}()
	resp, err := readProxyResponse(st, req)
	if err != nil {
		http.Error(w, "egress response failed", 502)
		return
	}
	defer func() { st.Close(); resp.Body.Close() }()
	stripHopHeaders(resp.Header)
	for key, values := range resp.Header {
		w.Header()[key] = values
	}
	w.WriteHeader(resp.StatusCode)
	buf := make([]byte, MaxData)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err = w.Write(buf[:n]); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				panic(http.ErrAbortHandler)
			}
			return
		}
	}
}
func (g *Guest) connect(w http.ResponseWriter, r *http.Request) {
	host, portText, err := net.SplitHostPort(r.Host)
	if err != nil || r.URL.RawQuery != "" || r.URL.Fragment != "" {
		http.Error(w, "invalid CONNECT target", 400)
		return
	}
	host, err = canonicalHost(host)
	port, portErr := strconv.ParseUint(portText, 10, 16)
	if err != nil || portErr != nil || port == 0 {
		http.Error(w, "destination denied", 403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, 12*time.Second)
	st, err := g.open(openCtx, "public", host, uint16(port))
	openCancel()
	if err != nil {
		http.Error(w, "egress unavailable", 502)
		return
	}
	defer st.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "tunnel unavailable", 500)
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close(); st.Close() })
	defer stop()
	stopStream := context.AfterFunc(st.ctx, func() { conn.Close() })
	defer stopStream()
	if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if rw.Flush() != nil {
		return
	}
	done := make(chan struct{}, 1)
	go func() {
		_, err := io.Copy(st, rw.Reader)
		if err != nil {
			st.Close()
		} else {
			_ = st.CloseWrite()
		}
		done <- struct{}{}
	}()
	_, _ = io.Copy(conn, st)
	conn.Close()
	st.Close()
	<-done
}

// net/http.ReadResponse alone does not limit response header size. Bound the
// raw header block before its MIME parser allocates a header map.
func readProxyResponse(st io.Reader, req *http.Request) (*http.Response, error) {
	br := bufio.NewReaderSize(st, maxServiceHeaders)
	for interim := 0; interim < 8; interim++ {
		var header bytes.Buffer
		for {
			line, err := br.ReadSlice('\n')
			if err != nil {
				return nil, err
			}
			if header.Len()+len(line) > maxServiceHeaders {
				return nil, errors.New("response headers too large")
			}
			header.Write(line)
			if bytes.Equal(line, []byte("\r\n")) {
				break
			}
		}
		resp, err := http.ReadResponse(bufio.NewReader(io.MultiReader(bytes.NewReader(header.Bytes()), br)), req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusSwitchingProtocols {
			return nil, errors.New("HTTP Upgrade requires a CONNECT tunnel")
		}
		if resp.StatusCode >= 200 {
			return resp, nil
		}
		// 100 Continue and103 Early Hints are not the final response. Keep the
		// same buffered reader so a coalesced final response/body is not discarded.
		_ = resp.Body.Close()
	}
	return nil, errors.New("too many informational responses")
}
