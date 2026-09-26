package egress

import (
	"context"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Motion Studio is a separate named HTTP capability, never a public-proxy
// destination or private-network exception. Only this operator-owned socket is
// dialled. Its directory must be mounted read-only into the controller.
const MotionStudioSocket = "/run/baarcha-motion-studio/worker.sock"

const (
	MotionStudioFileLimit int64 = 50 << 20
	MotionStudioWireLimit int64 = 51 << 20
	motionJSONLimit       int64 = 65000
	motionResponseLimit   int64 = 16 << 20
	motionMediaLimit      int64 = 1 << 30
)

// MotionStudioAuthorize must inspect the authoritative current binding, including
// app, provider, running state and generation. It must not trust request headers.
// The owning channel context must be cancelled when that binding is revoked.
type MotionStudioAuthorize func(context.Context, Identity, string) bool

type MotionStudio struct {
	appID     string
	authorize MotionStudioAuthorize
	transport http.RoundTripper
	slots     chan struct{}
}

func NewMotionStudio(appID string, authorize MotionStudioAuthorize) (*MotionStudio, error) {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		MaxConnsPerHost: 4, MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: 120 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			info, err := os.Lstat(MotionStudioSocket)
			if err != nil || info.Mode()&os.ModeSocket == 0 {
				return nil, errors.New("Motion Studio socket unavailable")
			}
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", MotionStudioSocket)
		}}
	return newMotionStudio(appID, authorize, transport)
}

func newMotionStudio(appID string, authorize MotionStudioAuthorize, transport http.RoundTripper) (*MotionStudio, error) {
	if len(appID) != 26 || strings.Trim(appID, "0123456789ABCDEFGHJKMNPQRSTVWXYZ") != "" || authorize == nil || transport == nil {
		return nil, errors.New("Motion Studio requires a fixed app and authoritative authorizer")
	}
	return &MotionStudio{appID: appID, authorize: authorize, transport: transport, slots: make(chan struct{}, 4)}, nil
}

var motionFilename = regexp.MustCompile(`^[a-zA-Z0-9_-]+\.(png|jpg|webp|mp4|wav|json)$`)
var motionRange = regexp.MustCompile(`^bytes=([0-9]*)-([0-9]*)$`)

func validMotionRange(value string) bool {
	m := motionRange.FindStringSubmatch(value)
	if m == nil || (m[1] == "" && m[2] == "") {
		return false
	}
	var first, last uint64
	var err error
	if m[1] != "" {
		first, err = strconv.ParseUint(m[1], 10, 63)
		if err != nil {
			return false
		}
	}
	if m[2] != "" {
		last, err = strconv.ParseUint(m[2], 10, 63)
		if err != nil {
			return false
		}
	}
	return (m[1] == "" && last > 0) || (m[1] != "" && (m[2] == "" || last >= first))
}

// No decoding, path cleaning, wildcard routes or arbitrary query forwarding.
func motionRoute(r *http.Request) (media, upload, allowed bool) {
	u := r.URL
	if u == nil || u.IsAbs() || u.Host != "" || u.RawPath != "" || u.Opaque != "" || u.Fragment != "" || strings.ContainsAny(u.Path, "%\\\x00\r\n") {
		return
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) == 4 && parts[1] == "media" && canonicalServiceUUID(parts[2]) && motionFilename.MatchString(parts[3]) {
		return true, false, (r.Method == "GET" || r.Method == "HEAD") && ((u.RawQuery == "" && !u.ForceQuery) || u.RawQuery == "download" || u.RawQuery == "download=1")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return
	}
	if r.Method == "GET" && (u.Path == "/api/health" || u.Path == "/api/status" || u.Path == "/api/projects") {
		return false, false, true
	}
	if r.Method == "POST" && u.Path == "/api/projects" {
		return false, false, true
	}
	if len(parts) < 4 || parts[1] != "api" || parts[2] != "projects" || !canonicalServiceUUID(parts[3]) {
		return
	}
	if len(parts) == 4 {
		return false, false, r.Method == "GET" || r.Method == "PATCH"
	}
	if len(parts) == 5 && r.Method == "DELETE" && parts[4] == "voice" {
		return false, false, true
	}
	if len(parts) == 5 && r.Method == "POST" {
		switch parts[4] {
		case "plan", "render", "cancel", "duplicate", "narrate", "clone":
			return false, false, true
		case "assets":
			return false, true, true
		}
	}
	return
}

func (s *MotionStudio) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deadline, stopDeadline := context.WithTimeout(r.Context(), 120*time.Second)
	defer stopDeadline()
	r = r.WithContext(deadline)
	id, ok := SourceIdentity(r.Context())
	if !ok || id.SandboxID == "" || id.Generation == "" || r.Context().Err() != nil || !s.authorize(r.Context(), id, s.appID) {
		http.Error(w, "forbidden", 403)
		return
	}
	media, upload, allowed := motionRoute(r)
	if !allowed {
		http.Error(w, "forbidden", 403)
		return
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") || len(auth[0]) < 8 || len(auth[0]) > 4096 || strings.ContainsAny(strings.TrimPrefix(auth[0], "Bearer "), " \t\r\n\x00") {
		http.Error(w, "worker authorization required", 403)
		return
	}
	ranges := r.Header.Values("Range")
	if len(ranges) > 1 || (len(ranges) == 1 && (!media || !validMotionRange(ranges[0]))) {
		http.Error(w, "invalid range", 400)
		return
	}
	if (r.Method == "GET" || r.Method == "HEAD" || r.Method == "DELETE") && (r.ContentLength != 0 || len(r.TransferEncoding) != 0) {
		http.Error(w, "unexpected body", 400)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "worker busy", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	// Existing channel replacement cancels immediately. Also recheck the app
	// binding during long uploads/downloads so administrative revocation cannot
	// retain access until a 120-second request completes.
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probe, stop := context.WithTimeout(ctx, 2*time.Second)
				active := s.authorize(probe, id, s.appID)
				stop()
				if !active {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-watchDone }()
	request := r.Clone(ctx)
	inputBody := r.Body
	stopBody := context.AfterFunc(ctx, func() {
		if inputBody != nil {
			inputBody.Close()
		}
	})
	defer stopBody()
	var multipartBody *motionUpload
	if upload {
		if r.ContentLength > MotionStudioWireLimit {
			http.Error(w, "upload too large", 413)
			return
		}
		var err error
		multipartBody, err = newMotionUpload(ctx, http.MaxBytesReader(w, r.Body, MotionStudioWireLimit), r.Header.Get("Content-Type"))
		if err != nil {
			http.Error(w, "invalid upload", 400)
			return
		}
		defer multipartBody.Close()
		request.Body = multipartBody
		request.ContentLength = -1
		request.Header.Set("Content-Type", multipartBody.contentType)
	} else if r.Method == "POST" || r.Method == "PATCH" {
		typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || typ != "application/json" {
			http.Error(w, "JSON required", 415)
			return
		}
		if r.ContentLength > motionJSONLimit {
			http.Error(w, "request too large", 413)
			return
		}
		request.Body = http.MaxBytesReader(w, r.Body, motionJSONLimit)
		defer request.Body.Close()
	}
	proxy := &httputil.ReverseProxy{Transport: s.transport, ErrorLog: log.New(io.Discard, "", 0), FlushInterval: -1,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(&url.URL{Scheme: "http", Host: "motion-studio.invalid"})
			p.Out.Host = "motion-studio.invalid"
			p.Out.Header = make(http.Header)
			for _, name := range []string{"Authorization", "Content-Type", "Range"} {
				if v := p.In.Header.Get(name); v != "" {
					p.Out.Header.Set(name, v)
				}
			}
			p.Out.Header.Set("Accept-Encoding", "identity")
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return errors.New("redirect forbidden")
			}
			if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
				return errors.New("unexpected encoded response")
			}
			if multipartBody != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && !multipartBody.complete.Load() {
				return errors.New("worker accepted incomplete upload")
			}
			limit := motionResponseLimit
			if media {
				limit = motionMediaLimit
			}
			if resp.ContentLength > limit {
				return errors.New("response too large")
			}
			clean := make(http.Header)
			for _, name := range []string{"Content-Type", "Content-Length"} {
				if v := resp.Header.Get(name); v != "" {
					clean.Set(name, v)
				}
			}
			if media {
				for _, name := range []string{"Content-Range", "Accept-Ranges", "Content-Disposition"} {
					if v := resp.Header.Get(name); v != "" {
						clean.Set(name, v)
					}
				}
			}
			clean.Set("Cache-Control", "no-store")
			resp.Header = clean
			resp.Trailer = nil
			resp.Body = &motionBoundedBody{ReadCloser: resp.Body, remaining: limit}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			status := 502
			var max *http.MaxBytesError
			if errors.As(err, &max) {
				status = 413
			}
			if multipartBody != nil {
				if code := multipartBody.failure.Load(); code != 0 {
					status = int(code)
				}
			}
			http.Error(w, "worker request failed", status)
		},
	}
	proxy.ServeHTTP(w, request)
}

type motionBoundedBody struct {
	io.ReadCloser
	remaining int64
}

type motionStreamBody struct {
	body   io.Reader
	stream *stream
	length int64
	read   atomic.Int64
	eof    atomic.Bool
}

func (b *motionStreamBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.read.Add(int64(n))
	if err == io.EOF {
		b.eof.Store(true)
	}
	return n, err
}
func (b *motionStreamBody) Close() error {
	if !b.eof.Load() && (b.length < 0 || b.read.Load() < b.length) {
		return b.stream.Close()
	}
	return nil
}

func (b *motionBoundedBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		var one [1]byte
		n, err := b.ReadCloser.Read(one[:])
		if n > 0 {
			return 0, errors.New("Motion Studio response exceeds limit")
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

// Re-encode only supported multipart fields with bounded streaming copies. No
// completed closing boundary is sent on overflow. The reviewed worker awaits
// its entire request + formData() and validates file size before writing assets.
type motionUpload struct {
	*io.PipeReader
	contentType string
	input       io.ReadCloser
	done        chan struct{}
	stop        func() bool
	once        sync.Once
	complete    atomic.Bool
	failure     atomic.Int32
}

func newMotionUpload(ctx context.Context, input io.ReadCloser, contentType string) (*motionUpload, error) {
	typ, params, err := mime.ParseMediaType(contentType)
	if err != nil || typ != "multipart/form-data" || len(params["boundary"]) == 0 || len(params["boundary"]) > 70 {
		return nil, errors.New("multipart required")
	}
	reader, writer := io.Pipe()
	out := multipart.NewWriter(writer)
	u := &motionUpload{PipeReader: reader, contentType: out.FormDataContentType(), input: input, done: make(chan struct{})}
	u.stop = context.AfterFunc(ctx, func() { input.Close(); writer.CloseWithError(ctx.Err()); reader.CloseWithError(ctx.Err()) })
	go func() {
		defer close(u.done)
		defer input.Close()
		err := u.copyParts(multipart.NewReader(input, params["boundary"]), out)
		// Multipart permits an epilogue. Consume it through the original wire
		// limiter before completing the forwarded form, including chunked input.
		if err == nil {
			_, err = io.Copy(io.Discard, input)
		}
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			u.failure.Store(413)
		}
		if err == nil {
			err = out.Close()
		}
		if err == nil {
			u.complete.Store(true)
		} else if u.failure.Load() == 0 {
			u.failure.Store(400)
		}
		writer.CloseWithError(err)
	}()
	return u, nil
}
func (u *motionUpload) Close() error {
	u.once.Do(func() { u.stop(); u.input.Close(); u.PipeReader.Close(); <-u.done })
	return nil
}
func (u *motionUpload) copyParts(in *multipart.Reader, out *multipart.Writer) error {
	file, kind, consent := false, false, false
	kindValue, consentValue := "media", ""
	for count := 0; ; count++ {
		part, err := in.NextRawPart()
		if err == io.EOF {
			if !file {
				return errors.New("missing file")
			}
			if (kindValue == "portrait" || kindValue == "voice") && consentValue != "true" {
				return errors.New("explicit consent required")
			}
			return nil
		}
		if err != nil {
			var max *http.MaxBytesError
			if errors.As(err, &max) {
				u.failure.Store(413)
			}
			return err
		}
		if count >= 3 {
			return errors.New("too many fields")
		}
		headerBytes := 0
		for name, values := range part.Header {
			if name != "Content-Disposition" && name != "Content-Type" {
				return errors.New("unsupported multipart header")
			}
			if len(values) != 1 {
				return errors.New("duplicate multipart header")
			}
			headerBytes += len(name) + len(values[0])
		}
		if headerBytes > 8192 {
			return errors.New("multipart headers too large")
		}
		switch part.FormName() {
		case "file":
			if file || part.FileName() == "" || len(part.FileName()) > 255 {
				return errors.New("invalid file field")
			}
			// RFC 5987 filename*= values are decoded by mime/multipart. Never
			// re-emit decoded controls as part of a new MIME header.
			for _, c := range part.FileName() {
				if c < 32 || c == 127 {
					return errors.New("invalid file name")
				}
			}
			file = true
			dst, err := out.CreateFormFile("file", part.FileName())
			if err != nil {
				return err
			}
			n, err := io.Copy(dst, io.LimitReader(part, MotionStudioFileLimit))
			if err != nil {
				return err
			}
			var extra [1]byte
			more, end := part.Read(extra[:])
			if more != 0 {
				u.failure.Store(413)
				return errors.New("file too large")
			}
			if n == 0 || end != io.EOF {
				return errors.New("invalid file content")
			}
		case "kind":
			if kind || part.FileName() != "" {
				return errors.New("invalid kind field")
			}
			kind = true
			value, err := io.ReadAll(io.LimitReader(part, 10))
			if err != nil {
				return err
			}
			kindValue = string(value)
			switch kindValue {
			case "media", "logo", "portrait", "voice", "narration":
			default:
				return errors.New("invalid kind")
			}
			if err := out.WriteField("kind", kindValue); err != nil {
				return err
			}
		case "consent":
			if consent || part.FileName() != "" {
				return errors.New("invalid consent field")
			}
			consent = true
			value, err := io.ReadAll(io.LimitReader(part, 6))
			if err != nil {
				return err
			}
			consentValue = string(value)
			if consentValue != "true" && consentValue != "false" {
				return errors.New("invalid consent")
			}
			if err := out.WriteField("consent", consentValue); err != nil {
				return err
			}
		default:
			return errors.New("unsupported upload field")
		}
	}
}
