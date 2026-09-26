package egress

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const motionUUID = "b60c4fa2-252d-4ebc-bae5-9d16142d2020"

func motionRequest(method, target string) *http.Request {
	r := serviceRequest(method, target)
	r.Header.Set("Authorization", "Bearer dedicated-worker-key")
	if method == "POST" || method == "PATCH" {
		r.Header.Set("Content-Type", "application/json")
		r.Body = io.NopCloser(strings.NewReader("{}"))
		r.ContentLength = 2
	}
	return r
}
func motionHandler(t *testing.T, rt http.RoundTripper) *MotionStudio {
	t.Helper()
	h, e := newMotionStudio(serviceApp, func(ctx context.Context, id Identity, app string) bool {
		return ctx.Err() == nil && id == (Identity{"owned", "generation"}) && app == serviceApp
	}, rt)
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func okMotionResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}
func TestMotionStudioRouteAndIdentityBoundaries(t *testing.T) {
	var calls atomic.Int32
	h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Body != nil {
			io.Copy(io.Discard, r.Body)
		}
		return okMotionResponse("ok"), nil
	}))
	allowed := [][2]string{{"GET", "/api/health"}, {"GET", "/api/status"}, {"GET", "/api/projects"}, {"POST", "/api/projects"}, {"GET", "/api/projects/" + motionUUID}, {"PATCH", "/api/projects/" + motionUUID}, {"POST", "/api/projects/" + motionUUID + "/plan"}, {"POST", "/api/projects/" + motionUUID + "/render"}, {"POST", "/api/projects/" + motionUUID + "/cancel"}, {"POST", "/api/projects/" + motionUUID + "/duplicate"}, {"GET", "/media/" + motionUUID + "/film-20260925_final.mp4?download=1"}, {"HEAD", "/media/" + motionUUID + "/poster.png?download"}}
	for _, a := range allowed {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, motionRequest(a[0], a[1]))
		if w.Code != 200 {
			t.Fatalf("%v: %d", a, w.Code)
		}
	}
	before := calls.Load()
	denied := [][2]string{{"DELETE", "/api/projects/" + motionUUID}, {"PUT", "/api/projects"}, {"PATCH", "/api/projects"}, {"CONNECT", "/api/status"}, {"GET", "/api/projects?all=1"}, {"GET", "/api/projects?"}, {"GET", "/api/projects/" + strings.ToUpper(motionUUID)}, {"GET", "/api/projects/%62" + motionUUID[1:]}, {"GET", "/api/projects/" + motionUUID + "/../status"}, {"GET", "/api//status"}, {"GET", "/media/" + motionUUID + "/../worker.env"}, {"GET", "/media/" + motionUUID + "/x%2f.mp4"}, {"GET", "/media/" + motionUUID + "/x%5c.mp4"}, {"GET", "/media/" + motionUUID + "/x.mp4?download=1&host=evil"}, {"GET", "/media/" + motionUUID + "/x.mp4?download=0"}, {"GET", "http://host/api/status"}, {"GET", "/media/" + motionUUID + "/.mp4"}}
	for _, a := range denied {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, motionRequest(a[0], a[1]))
		if w.Code != 403 {
			t.Errorf("%v: %d", a, w.Code)
		}
	}
	for _, id := range []Identity{{"sibling", "generation"}, {"owned", "old-generation"}, {"", ""}} {
		r := motionRequest("GET", "/api/status")
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, id))
		r.Header.Set("X-App-Id", serviceApp)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Error("untrusted identity accepted")
		}
	}
	other, _ := newMotionStudio("01M3CKN983PFRGMD711PCEPDFD", h.authorize, h.transport)
	w := httptest.NewRecorder()
	other.ServeHTTP(w, motionRequest("GET", "/api/status"))
	if w.Code != 403 {
		t.Error("different app accepted")
	}
	if calls.Load() != before {
		t.Fatal("denied request reached worker")
	}
}
func TestMotionStudioRangeAndCredentialIsolation(t *testing.T) {
	h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "motion-studio.invalid" || r.Host != "motion-studio.invalid" || r.Header.Get("Authorization") != "Bearer dedicated-worker-key" || r.Header.Get("Range") != "bytes=1-3" {
			t.Error("fixed upstream contract changed")
		}
		for _, k := range []string{"Cookie", "X-Api-Key", "X-Baarcha-Bridge", "X-Forwarded-For", "Proxy-Authorization"} {
			if r.Header.Get(k) != "" {
				t.Errorf("leaked %s", k)
			}
		}
		v := okMotionResponse("bcd")
		v.StatusCode = 206
		v.Header = http.Header{"Content-Range": {"bytes 1-3/6"}, "Accept-Ranges": {"bytes"}, "Content-Disposition": {"attachment; filename=film.mp4"}, "Set-Cookie": {"secret"}, "Authorization": {"secret"}}
		return v, nil
	}))
	r := motionRequest("GET", "/media/"+motionUUID+"/film.mp4?download=1")
	r.Header.Set("Range", "bytes=1-3")
	for _, k := range []string{"Cookie", "X-Api-Key", "X-Baarcha-Bridge", "X-Forwarded-For", "Proxy-Authorization"} {
		r.Header.Set(k, "never-forward")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "bcd" || w.Header().Get("Content-Range") != "bytes 1-3/6" || w.Header().Get("Accept-Ranges") != "bytes" || w.Header().Get("Content-Disposition") == "" || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Authorization") != "" {
		t.Fatal("range response lost or secret leaked", w.Code, w.Header())
	}
	for _, v := range []string{"bytes=0-0", "bytes=1-", "bytes=-1"} {
		if !validMotionRange(v) {
			t.Error("valid range rejected", v)
		}
	}
	for _, v := range []string{"bytes=0-1,3-4", "bytes=-", "bytes=-0", "bytes=9-2", "bytes=0-999999999999999999999999", "items=1-3", "bytes= 1-3", "bytes=+1-3"} {
		r := motionRequest("GET", "/media/"+motionUUID+"/film.mp4")
		r.Header.Set("Range", v)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Error("bad range accepted", v, w.Code)
		}
	}
	r = motionRequest("GET", "/media/"+motionUUID+"/film.mp4")
	r.Header["Range"] = []string{"bytes=0-1", "bytes=2-3"}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Error("duplicate ranges accepted")
	}
}

type zeroMotionReader struct{}

func (zeroMotionReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func motionForm(size int64, duplicate bool) (io.Reader, string) {
	var prefix bytes.Buffer
	mw := multipart.NewWriter(&prefix)
	mw.SetBoundary("motion-owned-boundary")
	mw.WriteField("kind", "media")
	mw.CreateFormFile("file", "film.mp4")
	start := append([]byte{}, prefix.Bytes()...)
	prefix.Reset()
	if duplicate {
		mw.CreateFormFile("file", "duplicate.mp4")
		prefix.WriteString("x")
	}
	mw.Close()
	return io.MultiReader(bytes.NewReader(start), io.LimitReader(zeroMotionReader{}, size), bytes.NewReader(prefix.Bytes())), mw.FormDataContentType()
}
func TestMotionStudioStreamingUploadBoundary(t *testing.T) {
	for _, item := range []struct {
		name      string
		size      int64
		duplicate bool
		status    int
	}{{"exact50MiB", MotionStudioFileLimit, false, 200}, {"oneByteOver", MotionStudioFileLimit + 1, false, 413}, {"empty", 0, false, 400}, {"duplicate", 1, true, 400}} {
		t.Run(item.name, func(t *testing.T) {
			committed := false
			h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
				_, p, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				mr := multipart.NewReader(r.Body, p["boundary"])
				var fileBytes int64
				for {
					part, e := mr.NextPart()
					if e == io.EOF {
						break
					}
					if e != nil {
						return nil, e
					}
					n, e := io.Copy(io.Discard, part)
					if e != nil {
						return nil, e
					}
					if part.FormName() == "file" {
						fileBytes = n
					}
				}
				if _, e := io.Copy(io.Discard, r.Body); e != nil {
					return nil, e
				}
				if fileBytes != item.size || fileBytes > MotionStudioFileLimit {
					t.Fatal("invalid worker bytes")
				}
				committed = true
				return okMotionResponse("saved"), nil
			}))
			source, ct := motionForm(item.size, item.duplicate)
			r := motionRequest("POST", "/api/projects/"+motionUUID+"/assets")
			r.Body = io.NopCloser(source)
			r.ContentLength = -1
			r.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != item.status || committed != (item.status == 200) {
				t.Fatalf("status%d committed%t", w.Code, committed)
			}
		})
	}
}
func TestMotionStudioChunkedAndResponseLimits(t *testing.T) {
	h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		_, e := io.Copy(io.Discard, r.Body)
		if e != nil {
			return nil, e
		}
		return okMotionResponse("ok"), nil
	}))
	r := motionRequest("POST", "/api/projects")
	r.Body = io.NopCloser(io.LimitReader(zeroMotionReader{}, motionJSONLimit+1))
	r.ContentLength = -1
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal("chunked JSON overflow", w.Code)
	}
	source, ct := motionForm(1, false)
	r = motionRequest("POST", "/api/projects/"+motionUUID+"/assets")
	r.Body = io.NopCloser(io.MultiReader(source, io.LimitReader(zeroMotionReader{}, MotionStudioWireLimit)))
	r.ContentLength = -1
	r.Header.Set("Content-Type", ct)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal("multipart epilogue overflow", w.Code)
	}
	for _, n := range []int64{4, 5} {
		b := &motionBoundedBody{ReadCloser: io.NopCloser(io.LimitReader(zeroMotionReader{}, n)), remaining: 4}
		v, e := io.ReadAll(b)
		if len(v) != 4 || (n == 4 && e != nil) || (n == 5 && e == nil) {
			t.Fatal("unknown-length response cap", n, len(v), e)
		}
	}
	for _, status := range []int{302, 307} {
		h := motionHandler(t, serviceRoundTrip(func(*http.Request) (*http.Response, error) {
			v := okMotionResponse("")
			v.StatusCode = status
			v.Header.Set("Location", "http://127.0.0.1/admin")
			return v, nil
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, motionRequest("GET", "/api/status"))
		if w.Code != 502 || w.Header().Get("Location") != "" {
			t.Fatal("redirect escaped")
		}
	}
}
func TestMotionStudioUnixTransportAndCancellation(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "motion-uds-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "w.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(cancelled) })}
	go server.Serve(listener)
	defer server.Close()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != "motion-studio.invalid:80" {
			t.Error("untrusted upstream", address)
		}
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	h := motionHandler(t, tr)
	r := motionRequest("GET", "/api/status")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), r.WithContext(ctx)) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("UDS not reached")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("worker not cancelled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler leaked")
	}
}
func TestMotionStudioBoundedAdmissionAndUploadCancel(t *testing.T) {
	entered := make(chan struct{}, 4)
	h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			r := motionRequest("GET", "/api/status")
			r = r.WithContext(context.WithValue(ctx, identityKey{}, Identity{"owned", "generation"}))
			h.ServeHTTP(httptest.NewRecorder(), r)
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("slot not entered")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, motionRequest("GET", "/api/status"))
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("unbounded admission")
	}
	cancel()
	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("slot not released")
		}
	}
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel = context.WithCancel(context.Background())
	u, e := newMotionUpload(ctx, pr, "multipart/form-data; boundary=owned")
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	finished := make(chan struct{})
	go func() { u.Close(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("multipart cancellation leaked")
	}
}

func TestMotionStudioNamedChannelPreservesUploadAndRanges(t *testing.T) {
	var calls atomic.Int32
	h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Method == "POST" {
			if _, e := io.Copy(io.Discard, r.Body); e != nil {
				return nil, e
			}
			return okMotionResponse("saved"), nil
		}
		if r.Method == "HEAD" {
			v := okMotionResponse("")
			v.ContentLength = 37
			v.Header.Set("Content-Length", "37")
			v.Header.Set("Accept-Ranges", "bytes")
			return v, nil
		}
		if r.Header.Get("Range") != "bytes=0-2" {
			t.Error("range stripped by channel")
		}
		v := okMotionResponse("abc")
		v.StatusCode = 206
		v.Header.Set("Content-Range", "bytes 0-2/3")
		return v, nil
	}))
	opts := fixtureOptions("")
	opts.Identity = Identity{"owned", "generation"}
	opts.Services = map[string]http.Handler{"motion": h}
	f := startFixture(t, opts)
	server := httptest.NewServer(f.g.ServiceHandler("motion"))
	defer server.Close()
	source, ct := motionForm(MotionStudioFileLimit, false)
	r, _ := http.NewRequest("POST", server.URL+"/api/projects/"+motionUUID+"/assets", source)
	r.Header.Set("Content-Type", ct)
	r.Header.Set("Authorization", "Bearer dedicated-worker-key")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || resp.StatusCode != 200 || string(body) != "saved" {
		t.Fatal("50MiB channel upload", resp.StatusCode, e)
	}
	r, _ = http.NewRequest("GET", server.URL+"/media/"+motionUUID+"/film.mp4?download=1", nil)
	r.Header.Set("Authorization", "Bearer dedicated-worker-key")
	r.Header.Set("Range", "bytes=0-2")
	resp, e = client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	body, e = io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || resp.StatusCode != 206 || string(body) != "abc" || resp.Header.Get("Content-Range") != "bytes 0-2/3" {
		t.Fatal("channel range", resp.StatusCode, e)
	}
	r, _ = http.NewRequest("HEAD", server.URL+"/media/"+motionUUID+"/film.mp4", nil)
	r.Header.Set("Authorization", "Bearer dedicated-worker-key")
	resp, e = client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	body, e = io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || resp.StatusCode != 200 || len(body) != 0 || resp.ContentLength != 37 || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatal("HEAD media size/body contract lost", resp.StatusCode, resp.ContentLength, e)
	}
	opts.Identity = Identity{"other-app-sandbox", "other-generation"}
	foreign := startFixture(t, opts)
	other := httptest.NewServer(foreign.g.ServiceHandler("motion"))
	defer other.Close()
	r, _ = http.NewRequest("GET", other.URL+"/api/status", nil)
	r.Header.Set("Authorization", "Bearer dedicated-worker-key")
	resp, e = client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 || calls.Load() != 3 {
		t.Fatal("foreign channel reached worker")
	}
}
func TestMotionStudioRevocationCancelsActiveResponse(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	h, e := newMotionStudio(serviceApp, func(context.Context, Identity, string) bool { return active.Load() }, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
		return nil, r.Context().Err()
	}))
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), motionRequest("GET", "/api/status")) }()
	<-entered
	active.Store(false)
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked stream retained")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("revoked handler leaked")
	}
}

func TestMotionStudioIncompleteChannelUploadDoesNotDrainOrLeak(t *testing.T) {
	h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		v := okMotionResponse("forbidden")
		v.StatusCode = 403
		return v, nil
	}))
	opts := fixtureOptions("")
	opts.Identity = Identity{"owned", "generation"}
	opts.Services = map[string]http.Handler{"motion": h}
	f := startFixture(t, opts)
	server := httptest.NewServer(f.g.ServiceHandler("motion"))
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	prefix := "--owned\r\nContent-Disposition: form-data; name=\"file\"; filename=\"film.mp4\"\r\nContent-Type: video/mp4\r\n\r\n"
	io.WriteString(conn, "POST /api/projects/"+motionUUID+"/assets HTTP/1.1\r\nHost: local\r\nAuthorization: Bearer dedicated-worker-key\r\nContent-Type: multipart/form-data; boundary=owned\r\nContent-Length: 100000\r\n\r\n"+prefix)
	var response [256]byte
	if _, err = conn.Read(response[:]); err != nil {
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("early rejection blocked on draining upload")
		}
	}
	deadline := time.Now().Add(time.Second)
	for len(h.slots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(h.slots) != 0 {
		t.Fatal("aborted upload retained slot")
	}
}

func TestMotionUploadRejectsDecodedFilenameControls(t *testing.T) {
	for _, filename := range []string{"bad%0D%0AX-Injected%3Aevil.png", "bad%00.png", "bad%09.png", "bad%7F.png"} {
		input := "--owned\r\nContent-Disposition: form-data; name=\"file\"; filename*=UTF-8''" + filename + "\r\nContent-Type: image/png\r\n\r\nx\r\n--owned--\r\n"
		upload, err := newMotionUpload(context.Background(), io.NopCloser(strings.NewReader(input)), "multipart/form-data; boundary=owned")
		if err != nil {
			t.Fatal(err)
		}
		result, readErr := io.ReadAll(upload)
		upload.Close()
		if readErr == nil || len(result) != 0 {
			t.Fatalf("decoded filename control forwarded: error=%v bytes=%d", readErr, len(result))
		}
	}
}
