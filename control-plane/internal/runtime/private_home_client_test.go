package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateHomeClientStreamsScopedManifestAndBothCredentials(t *testing.T) {
	root, m := homeFixture(t)
	archive := homeZip(t, root, m)
	st, _ := archive.Stat()
	raw, _ := CanonicalHomeManifest(m)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+testRemoteToken || r.Header.Get("cube-traffic-access-token") != "private-ingress" || r.Host != "owner.cube.test" {
			t.Error("private credentials/host missing")
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method == http.MethodPost {
			got, _ := io.ReadAll(r.Body)
			if !bytes.Equal(raw, got) {
				t.Error("manifest differs")
			}
			io.Copy(w, io.NewSectionReader(archive, 0, st.Size()))
			return
		}
		decoded, e := base64.RawURLEncoding.DecodeString(r.Header.Get("X-Home-Manifest"))
		if e != nil || !bytes.Equal(decoded, raw) {
			t.Error("header manifest differs")
		}
		if r.ContentLength != st.Size() {
			t.Error("streaming body length missing")
		}
		data, _ := io.ReadAll(r.Body)
		got, e := PrivateHomeDigest(m, bytes.NewReader(data), int64(len(data)))
		want, _ := PrivateHomeDigest(m, archive, st.Size())
		if e != nil || got != want {
			t.Error("streaming content differs", e)
		}
	}))
	defer server.Close()
	client, e := NewRemoteClient(RemoteConfig{BaseURL: server.URL, Token: testRemoteToken, TrafficAccessToken: "private-ingress", Host: "owner.cube.test"})
	if e != nil {
		t.Fatal(e)
	}
	var received bytes.Buffer
	if e = client.ExportPrivateHome(context.Background(), m, &received); e != nil {
		t.Fatal(e)
	}
	if e = client.ImportPrivateHome(context.Background(), m, bytes.NewReader(received.Bytes()), int64(received.Len())); e != nil {
		t.Fatal(e)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestPrivateHomeTransportRejectsUnavailableAndRedirects(t *testing.T) {
	_, m := homeFixture(t)
	unavailable := errors.New("missing binding")
	c := NewUnavailableClient(unavailable)
	if e := c.ExportPrivateHome(context.Background(), m, io.Discard); !errors.Is(e, unavailable) {
		t.Fatal(e)
	}
	if e := c.ImportPrivateHome(context.Background(), m, strings.NewReader(""), 0); !errors.Is(e, unavailable) {
		t.Fatal(e)
	}
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	c, e := NewRemoteClient(RemoteConfig{BaseURL: redirect.URL, Token: testRemoteToken})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ExportPrivateHome(context.Background(), m, io.Discard); e == nil {
		t.Fatal("redirect accepted")
	}
	if hits != 0 {
		t.Fatal("owner credential leaked to redirect")
	}
	var decoded HomeManifest
	if e = json.Unmarshal([]byte(`{"version":1,"entries":[{"path":".runtimed","disposition":"separate"}]}`), &decoded); e != nil {
		t.Fatal(e)
	}
}
