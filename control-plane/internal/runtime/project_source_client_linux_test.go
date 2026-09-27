package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/projectsource"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectRevisionClientChecksIdentityAndScopesCredentials(t *testing.T) {
	r := projectsource.Recipe{Version: 1, RuntimeImage: "example/runtime@sha256:" + strings.Repeat("a", 64), PackageManager: "none", Start: []string{"node", "server.js"}, Port: 3000, HealthPath: "/"}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("server.js")
	f.Write([]byte("server code"))
	z.Close()
	source, m, err := projectsource.Build(b.Bytes(), r)
	if err != nil {
		t.Fatal(err)
	}
	badReceipt := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != "owner.cube.test" || req.Header.Get("Authorization") != "Bearer "+testRemoteToken || req.Header.Get("cube-traffic-access-token") != "private-ingress" {
			t.Error("wrong credentials or routing")
		}
		if req.URL.Path == "/export/project-source" {
			mw := multipart.NewWriter(w)
			w.Header().Set("Content-Type", mw.FormDataContentType())
			part, _ := mw.CreateFormField("manifest")
			json.NewEncoder(part).Encode(m)
			part, _ = mw.CreateFormFile("source", "source.zip")
			part.Write(source)
			mw.Close()
			return
		}
		if req.URL.Path != "/import/project-source" {
			t.Fatal("wrong endpoint")
		}
		mr, e := req.MultipartReader()
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		part, _ := mr.NextPart()
		if part.FormName() != "manifest" {
			t.Error("manifest order")
		}
		io.Copy(io.Discard, part)
		part, _ = mr.NextPart()
		got, _ := io.ReadAll(part)
		if !bytes.Equal(got, source) {
			t.Error("source changed")
		}
		hash := m.SourceSHA256
		if badReceipt {
			hash = strings.Repeat("f", 64)
		}
		json.NewEncoder(w).Encode(map[string]any{"prepared": true, "quiesced": true, "source_sha256": hash, "dependency_key": m.DependencyKey})
	}))
	defer srv.Close()
	c, err := NewRemoteClient(RemoteConfig{BaseURL: srv.URL, Token: testRemoteToken, Host: "owner.cube.test", TrafficAccessToken: "private-ingress"})
	if err != nil {
		t.Fatal(err)
	}
	got, manifest, err := c.ExportProjectRevision(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.ImportProjectRevision(context.Background(), got, manifest); err != nil {
		t.Fatal(err)
	}
	badReceipt = true
	if err = c.ImportProjectRevision(context.Background(), got, manifest); err == nil {
		t.Fatal("mismatched receipt accepted")
	}
}
