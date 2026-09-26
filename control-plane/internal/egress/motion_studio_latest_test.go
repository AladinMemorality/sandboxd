package egress

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMotionStudioLatestActionsRemainNarrow(t *testing.T) {
	calls := 0
	h := motionHandler(t, serviceRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Body != nil {
			io.Copy(io.Discard, r.Body)
		}
		return okMotionResponse("ok"), nil
	}))
	for _, action := range [][2]string{{"POST", "narrate"}, {"POST", "clone"}, {"DELETE", "voice"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, motionRequest(action[0], "/api/projects/"+motionUUID+"/"+action[1]))
		if w.Code != 200 {
			t.Fatalf("%v: %d", action, w.Code)
		}
	}
	if calls != 3 {
		t.Fatal("latest actions did not reach the scoped worker")
	}
	for _, action := range [][2]string{{"GET", "narrate"}, {"DELETE", "clone"}, {"POST", "voice"}, {"DELETE", "voice?all=1"}, {"DELETE", "voice/"}, {"POST", "clone/extra"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, motionRequest(action[0], "/api/projects/"+motionUUID+"/"+action[1]))
		if w.Code != 403 {
			t.Fatalf("unexpected route allowed %v: %d", action, w.Code)
		}
	}
	for _, chunked := range []bool{false, true} {
		r := motionRequest("DELETE", "/api/projects/"+motionUUID+"/voice")
		r.Body = io.NopCloser(strings.NewReader("x"))
		r.ContentLength = 1
		if chunked {
			r.ContentLength = -1
			r.TransferEncoding = []string{"chunked"}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("DELETE unexpectedly accepted a body", w.Code)
		}
	}
	if calls != 3 {
		t.Fatal("invalid latest action reached the worker")
	}
}

func TestMotionUploadLatestKindsAndExplicitConsent(t *testing.T) {
	type field struct{ name, value, filename string }
	cases := []struct {
		name   string
		fields []field
		valid  bool
	}{
		{"default", nil, true},
		{"media", []field{{"kind", "media", ""}}, true},
		{"logo", []field{{"kind", "logo", ""}}, true},
		{"narration", []field{{"kind", "narration", ""}}, true},
		{"portrait", []field{{"kind", "portrait", ""}, {"consent", "true", ""}}, true},
		{"voice", []field{{"consent", "true", ""}, {"kind", "voice", ""}}, true},
		{"ordinary-false", []field{{"kind", "media", ""}, {"consent", "false", ""}}, true},
		{"portrait-missing", []field{{"kind", "portrait", ""}}, false},
		{"voice-false", []field{{"kind", "voice", ""}, {"consent", "false", ""}}, false},
		{"voice-malformed", []field{{"kind", "voice", ""}, {"consent", "true\n", ""}}, false},
		{"duplicate-consent", []field{{"consent", "true", ""}, {"consent", "true", ""}}, false},
		{"duplicate-kind", []field{{"kind", "media", ""}, {"kind", "voice", ""}}, false},
		{"file-consent", []field{{"kind", "voice", ""}, {"consent", "true", "consent.txt"}}, false},
		{"unknown-field", []field{{"consentAt", "2026-09-25", ""}}, false},
		{"unknown-kind", []field{{"kind", "anything", ""}}, false},
		{"oversized-kind", []field{{"kind", strings.Repeat("x", 1024), ""}}, false},
		{"oversized-consent", []field{{"consent", strings.Repeat("true", 256), ""}}, false},
	}
	for _, tc := range cases {
		for _, fileFirst := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "-file-last", true: "-file-first"}[fileFirst], func(t *testing.T) {
				var input bytes.Buffer
				writer := multipart.NewWriter(&input)
				writeFile := func() {
					part, err := writer.CreateFormFile("file", "owned.dat")
					if err != nil {
						t.Fatal(err)
					}
					io.WriteString(part, "owned asset bytes")
				}
				if fileFirst {
					writeFile()
				}
				for _, f := range tc.fields {
					if f.filename != "" {
						p, err := writer.CreateFormFile(f.name, f.filename)
						if err != nil {
							t.Fatal(err)
						}
						io.WriteString(p, f.value)
					} else if err := writer.WriteField(f.name, f.value); err != nil {
						t.Fatal(err)
					}
				}
				if !fileFirst {
					writeFile()
				}
				writer.Close()
				u, err := newMotionUpload(context.Background(), io.NopCloser(bytes.NewReader(input.Bytes())), writer.FormDataContentType())
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(u)
				u.Close()
				_, params, err := mime.ParseMediaType(u.contentType)
				if err != nil {
					t.Fatal(err)
				}
				if !tc.valid {
					if readErr == nil || u.complete.Load() || bytes.HasSuffix(body, []byte("--"+params["boundary"]+"--\r\n")) {
						t.Fatal("invalid consent/form reached a completed worker request")
					}
					return
				}
				if readErr != nil || !u.complete.Load() {
					t.Fatal("valid asset rejected", readErr)
				}
				reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
				forwarded := map[string]string{}
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					value, err := io.ReadAll(part)
					if err != nil {
						t.Fatal(err)
					}
					forwarded[part.FormName()] = string(value)
				}
				if forwarded["file"] != "owned asset bytes" || len(forwarded) != len(tc.fields)+1 {
					t.Fatal("asset bytes or fields changed")
				}
				for _, f := range tc.fields {
					if forwarded[f.name] != f.value {
						t.Fatal("field changed", f.name)
					}
				}
			})
		}
	}
}
