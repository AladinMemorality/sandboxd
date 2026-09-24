package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"golang.org/x/sys/unix"
)

func TestScopedWorkspaceRoundTripAndExclusions(t *testing.T) {
	root := t.TempDir()
	if err := scopedWrite(root, "src/main.js", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := scopedRead(root, "src/main.js", 100, true)
	if err != nil || string(data) != "hello" {
		t.Fatalf("read %q %v", data, err)
	}
	os.MkdirAll(filepath.Join(root, "node_modules"), 0755)
	os.WriteFile(filepath.Join(root, "node_modules", "secret"), []byte("dependency"), 0644)
	list, err := scopedList(context.Background(), root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 || list.Entries[1].Path != "src/main.js" {
		t.Fatalf("list: %+v", list)
	}
	data, err = scopedExport(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "src/main.js" {
		t.Fatalf("export: %+v", zr.File)
	}
	f, _ := zr.File[0].Open()
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != "hello" {
		t.Fatalf("zip data: %q", got)
	}
}
func TestScopedWorkspaceRejectsTraversalLinksAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	os.WriteFile(secret, []byte("outside"), 0644)
	os.Symlink(secret, filepath.Join(root, "leaf"))
	os.Symlink(outside, filepath.Join(root, "dir"))
	os.Link(secret, filepath.Join(root, "hard"))
	unix.Mkfifo(filepath.Join(root, "fifo"), 0600)
	for _, name := range []string{".", "./secret", "a/./secret", "/etc/passwd", "../secret", "a/../secret", "a//b", "a\\b", ".git/config", "node_modules/secret", ".runtimed/tasks", "leaf", "dir/secret", "hard", "fifo"} {
		t.Run(name, func(t *testing.T) {
			if _, err := scopedRead(root, name, 100, true); err == nil {
				t.Fatal("unsafe read accepted")
			}
			if err := scopedWrite(root, name, []byte("overwrite")); err == nil {
				t.Fatal("unsafe write accepted")
			}
		})
	}
	list, err := scopedList(context.Background(), root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 0 {
		t.Fatalf("special entries leaked: %+v", list)
	}
	data, _ := os.ReadFile(secret)
	if string(data) != "outside" {
		t.Fatal("outside file changed")
	}
	linkRoot := filepath.Join(t.TempDir(), "root")
	os.Symlink(root, linkRoot)
	if _, err := scopedList(context.Background(), linkRoot, "", false); err == nil {
		t.Fatal("symlink workspace root accepted")
	}
}
func TestScopedWorkspaceResistsDirectorySymlinkRace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.Mkdir(filepath.Join(root, "dir"), 0755)
	os.WriteFile(filepath.Join(root, "dir", "value"), []byte("inside"), 0644)
	os.WriteFile(filepath.Join(outside, "value"), []byte("outside-secret"), 0644)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "held")) == nil {
				os.Symlink(outside, filepath.Join(root, "dir"))
				os.Remove(filepath.Join(root, "dir"))
				os.Rename(filepath.Join(root, "held"), filepath.Join(root, "dir"))
			}
		}
	}()
	for i := 0; i < 300; i++ {
		data, err := scopedRead(root, "dir/value", 100, true)
		if err == nil && string(data) == "outside-secret" {
			close(stop)
			wg.Wait()
			t.Fatal("racing directory link escaped")
		}
		_ = scopedWrite(root, "dir/value", []byte("safe-write"))
	}
	close(stop)
	wg.Wait()
	data, _ := os.ReadFile(filepath.Join(outside, "value"))
	if string(data) != "outside-secret" {
		t.Fatal("racing write escaped")
	}
}
func TestScopedWorkspaceBounds(t *testing.T) {
	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "large"))
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(runtime.MaxFileWriteBytes + 1)
	f.Close()
	if _, err = scopedRead(root, "large", runtime.MaxFileReadBytes, true); !errors.Is(err, errScopedLimit) {
		t.Fatalf("read bound: %v", err)
	}
	if _, err = scopedExport(context.Background(), root); !errors.Is(err, errScopedLimit) {
		t.Fatalf("export bound: %v", err)
	}
	if _, err = scopedParts(strings.Repeat("a/", 33)+"f", false, true); !errors.Is(err, errScopedLimit) {
		t.Fatalf("depth bound: %v", err)
	}
	seen := runtime.MaxWorkspaceEntries
	dir, _ := openScopedRoot(root)
	defer dir.Close()
	err = scopedWalk(context.Background(), dir, "", true, 0, &seen, func(string, *os.File, os.FileInfo) error { return nil })
	if !errors.Is(err, errScopedLimit) {
		t.Fatalf("entry bound: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = scopedList(ctx, root, "", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel bound: %v", err)
	}
}
func TestGuestFileProtocolAndTaskResult(t *testing.T) {
	root := t.TempDir()
	runtimeDir := t.TempDir()
	a := &app{appDir: root, runtimeDir: runtimeDir, web: &process{name: "web"}}
	os.WriteFile(filepath.Join(runtimeDir, "web.log"), []byte("one\ntwo\nthree\n"), 0644)
	os.MkdirAll(filepath.Join(runtimeDir, "tasks", "task1"), 0755)
	result, _ := json.Marshal(runtime.TaskResult{ID: "task1", Status: runtime.TaskSucceeded})
	os.WriteFile(filepath.Join(runtimeDir, "tasks", "task1", "result.json"), result, 0644)
	server := httptest.NewServer(authenticatedControl(controlTestToken, a.controlHandler()))
	defer server.Close()
	c, err := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: server.URL, Token: controlTestToken})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	written, err := c.PutFile(ctx, "hello.txt", strings.NewReader("guest bytes"))
	if err != nil || written.Size != 11 {
		t.Fatalf("put: %+v %v", written, err)
	}
	data, err := c.ReadFile(ctx, "hello.txt")
	if err != nil || string(data) != "guest bytes" {
		t.Fatalf("read: %s %v", data, err)
	}
	if list, err := c.ListFiles(ctx, "", true); err != nil || len(list.Entries) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	// This is the platform client.ts file-browser/pages query. Returned paths
	// must remain app-relative so its subsequent read requests work unchanged.
	list, err := c.ListFiles(ctx, ".", true)
	if err != nil || list.Path != "." || !list.Recursive || len(list.Entries) != 1 || list.Entries[0].Path != "hello.txt" || list.Entries[0].Type != "file" || list.Entries[0].Size != 11 {
		t.Fatalf("platform listing: %+v %v", list, err)
	}
	if data, err := c.ReadFile(ctx, list.Entries[0].Path); err != nil || string(data) != "guest bytes" {
		t.Fatalf("platform listing read: %q %v", data, err)
	}
	for _, path := range []string{"./", "./hello.txt", "hello.txt/..", "../"} {
		if _, err := c.ListFiles(ctx, path, true); err == nil {
			t.Fatalf("non-root dot/traversal listing accepted: %q", path)
		}
	}
	if data, err := c.ExportWorkspace(ctx); err != nil || len(data) == 0 {
		t.Fatalf("export %v", err)
	}
	if logs, err := c.ProcessLogs(ctx, "web", 2); err != nil || strings.Join(logs.Lines, ",") != "two,three" {
		t.Fatalf("logs: %+v %v", logs, err)
	}
	if result, err := c.TaskResult(ctx, "task1"); err != nil || result.ID != "task1" {
		t.Fatalf("result: %+v %v", result, err)
	}
	if _, err := c.TaskResult(ctx, "missing"); err == nil {
		t.Fatal("missing result accepted")
	}
	req := httptest.NewRequest(http.MethodPut, "/files?path=large", nil)
	req.ContentLength = runtime.MaxFileWriteBytes + 1
	out := httptest.NewRecorder()
	a.controlHandler().ServeHTTP(out, req)
	if out.Code != 413 {
		t.Fatalf("write cap %d", out.Code)
	}
	for _, url := range []string{"/files", "/files/content?path=hello.txt", "/export", "/processes/web/logs", "/tasks/task1/result"} {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		out := httptest.NewRecorder()
		authenticatedControl(controlTestToken, a.controlHandler()).ServeHTTP(out, req)
		if out.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", url, out.Code)
		}
	}
}
