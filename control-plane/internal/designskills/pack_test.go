package designskills

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func TestPackContainsEverySkillAndLocalReferences(t *testing.T) {
	want := map[string]bool{"GUIDE.md": false, "taste-redesign/SKILL.md": false, "taste-minimalist/SKILL.md": false, "design-inspiration/SKILL.md": false, "image-to-code/SKILL.md": false, "self-screenshot/SKILL.md": false, "web-design-guidelines/SKILL.md": false, "web-design-guidelines/command.md": false}
	for _, f := range Files() {
		if len(f.Content) == 0 {
			t.Fatalf("empty resource %s", f.Path)
		}
		want[f.Path] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing resource %s", name)
		}
	}
	if !strings.Contains(Prompt(), Directory()+"/") {
		t.Fatal("prompt does not point to the delivered pack")
	}
}

func TestDeliveryRepairsMissingOrChangedFilesAndPreservesCustomSkills(t *testing.T) {
	var mu sync.Mutex
	custom := ".claude/skills/taste-redesign/SKILL.md"
	files := map[string][]byte{custom: []byte("owner's custom instructions")}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		path := r.URL.Query().Get("path")
		if !strings.HasPrefix(path, Directory()+"/") {
			t.Errorf("unexpected write/read outside pack: %s", path)
		}
		if r.Method == "GET" {
			if b, ok := files[path]; ok {
				w.Write(b)
			} else {
				w.WriteHeader(404)
			}
			return
		}
		b, _ := io.ReadAll(r.Body)
		files[path] = b
		writes++
		json.NewEncoder(w).Encode(runtime.FileWrite{Path: path, Size: int64(len(b))})
	}))
	defer server.Close()
	client, err := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: server.URL, Token: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := Ensure(context.Background(), client); err != nil {
			t.Fatal(err)
		}
	}
	if writes != len(Files()) {
		t.Fatalf("unchanged pack was rewritten: %d writes", writes)
	}
	mu.Lock()
	delete(files, Directory()+"/self-screenshot/SKILL.md")
	files[Directory()+"/GUIDE.md"] = []byte("stale")
	mu.Unlock()
	if err := Ensure(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if writes != len(Files())+2 {
		t.Fatalf("repair writes: %d", writes)
	}
	for _, f := range Files() {
		if !bytes.Equal(files[Directory()+"/"+f.Path], f.Content) {
			t.Errorf("wrong contents %s", f.Path)
		}
	}
	if string(files[custom]) != "owner's custom instructions" {
		t.Fatal("custom skill overwritten")
	}
}

func TestDeliveryFailsOnRejectedPathsTransportErrorsAndBadAcknowledgements(t *testing.T) {
	for _, status := range []int{400, 403, 409, 500, 502, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && status == 200 {
					w.WriteHeader(404)
					return
				}
				w.WriteHeader(status)
				if status == 200 {
					io.WriteString(w, `{"path":"wrong","size":1}`)
				}
			}))
			defer server.Close()
			client, _ := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: server.URL, Token: strings.Repeat("a", 64)})
			if err := Ensure(context.Background(), client); err == nil {
				t.Fatal("unavailable pack accepted")
			}
		})
	}
}
