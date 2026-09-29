package publication

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

const appID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
const firstID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
const secondID = "01ARZ3NDEKTSV4RRFFQ69G5FAX"

type fixture struct {
	files    map[string][]byte
	broken   string
	reads    map[string]int
	changing bool
}

func source() *fixture {
	return &fixture{files: map[string][]byte{"package.json": []byte(`{"devDependencies":{"vite":"1"}}`), "dist/index.html": []byte(`<html><script type="module" src="/assets/app.js"></script></html>`), "dist/assets/app.js": []byte(`document.body.innerHTML="version one"`)}, reads: map[string]int{}}
}
func (f *fixture) ListFiles(context.Context, string, bool) (*runtime.FileList, error) {
	out := &runtime.FileList{}
	for name, data := range f.files {
		if name != "package.json" {
			out.Entries = append(out.Entries, runtime.FileEntry{Path: name, Type: "file", Size: int64(len(data))})
		}
	}
	return out, nil
}
func (f *fixture) ReadFile(_ context.Context, name string) ([]byte, error) {
	f.reads[name]++
	if f.broken == name {
		return nil, errors.New("transfer failed")
	}
	if f.changing && name == "dist/assets/app.js" && f.reads[name] > 1 {
		return []byte("changed"), nil
	}
	return f.files[name], nil
}
func TestAtomicPublishAndPinnedAssets(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	if err := Capture(ctx, root, appID, firstID, source()); err != nil {
		t.Fatal(err)
	}
	next := source()
	next.broken = "dist/assets/app.js"
	if err := Capture(ctx, root, appID, secondID, next); err == nil {
		t.Fatal("partial transfer published")
	}
	if got, _ := Current(root, appID); got != firstID {
		t.Fatal("last good build replaced")
	}
	next = source()
	next.files["dist/assets/app.js"] = []byte("version two")
	if err := Capture(ctx, root, appID, secondID, next); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{{"/", "/__sandboxd/build/" + secondID + "/assets/app.js"}, {"/__sandboxd/build/" + firstID + "/assets/app.js", "version one"}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", tc.path, nil)
		if !Serve(w, r, root, appID) || w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
func TestRejectChangingUnsafeOrDevelopmentOutput(t *testing.T) {
	for _, mode := range []string{"changing", "traversal", "development", "ssr"} {
		t.Run(mode, func(t *testing.T) {
			f := source()
			switch mode {
			case "changing":
				f.changing = true
			case "traversal":
				f.files["dist/../secret"] = []byte("secret")
			case "development":
				f.files["dist/index.html"] = []byte(`<script src="/@vite/client"></script>`)
			case "ssr":
				f.files["package.json"] = []byte(`{"dependencies":{"next":"1"}}`)
			}
			root := t.TempDir()
			if err := Capture(context.Background(), root, appID, firstID, f); err == nil {
				t.Fatal("unsafe build accepted")
			}
			if _, err := Current(root, appID); err == nil {
				t.Fatal("build made visible")
			}
		})
	}
}
func TestSPAFallbackDoesNotSwallowAPIOrMissingAssets(t *testing.T) {
	root := t.TempDir()
	if err := Capture(context.Background(), root, appID, firstID, source()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path      string
		navigate, handled bool
		code              int
	}{{"GET", "/dashboard", true, true, 200}, {"GET", "/api/count", false, false, 200}, {"POST", "/api/count", false, false, 200}, {"GET", "/assets/missing.js", false, true, 404}, {"GET", "/.env", false, true, 404}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.navigate {
			r.Header.Set("Sec-Fetch-Mode", "navigate")
			r.Header.Set("Accept", "text/html")
		}
		if got := Serve(w, r, root, appID); got != tc.handled || w.Code != tc.code {
			t.Fatalf("%s: handled=%v code=%d", tc.path, got, w.Code)
		}
	}
}
