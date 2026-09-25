package api

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFileReadAllowsOnlyAppInternalSymlinks(t *testing.T) {
	s, id := fileSymlinkServer(t)
	app := s.appDirFor(id)
	if err := os.Mkdir(filepath.Join(app, "real"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "real", "file"), []byte("internal"), 0644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"alias": "real", "file-link": "real/file", "escape-absolute": "/etc/passwd", "escape-relative": "../../../outside", "magic": "/proc/self/fd/0"} {
		if err := os.Symlink(target, filepath.Join(app, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"alias/file", "file-link"} {
		if w := getContent(t, s, id, path); w.Code != 200 || w.Body.String() != "internal" {
			t.Fatalf("internal link %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"escape-absolute", "escape-relative", "magic"} {
		if w := getContent(t, s, id, path); w.Code != 404 {
			t.Fatalf("escaping link accepted: %s", path)
		}
	}
	r := httptest.NewRequest("GET", "/files?path=alias", nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.v1ListFiles(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "alias/file") {
		t.Fatalf("internal directory listing: %d %s", w.Code, w.Body.String())
	}
	// Swap an internal link for an escaping link while real GETs resolve it.
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte(secretMarker), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 400; i++ {
			target := "real/file"
			if i%2 == 0 {
				target = outside
			}
			tmp := filepath.Join(app, "swap-next")
			_ = os.Remove(tmp)
			_ = os.Symlink(target, tmp)
			_ = os.Rename(tmp, filepath.Join(app, "file-link"))
		}
	}()
	for i := 0; i < 400; i++ {
		w := getContent(t, s, id, "file-link")
		if w.Code == 200 && w.Body.String() != "internal" {
			t.Errorf("link race leaked data")
			break
		}
	}
	wg.Wait()
}

type fileMutationReader struct {
	mutate func()
	reader io.Reader
	done   bool
}

func (r *fileMutationReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		r.mutate()
	}
	return r.reader.Read(p)
}

func putFileRequest(t *testing.T, s *Server, id, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("PUT", "/v1/sandboxes/"+id+"/files?path="+url.QueryEscape(path), body)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.v1PutFile(w, r)
	return w
}

func TestPutFileDescriptorSafety(t *testing.T) {
	for _, attack := range []string{"leaf", "parent", "workspace", "app", "mount", "fifo"} {
		t.Run(attack, func(t *testing.T) {
			s, id := fileSymlinkServer(t)
			_, mnt := s.Loopback.Paths(id)
			outside := t.TempDir()
			canary := filepath.Join(outside, "file")
			if err := os.WriteFile(canary, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			path := "file"
			var link string
			switch attack {
			case "leaf":
				link = filepath.Join(s.appDirFor(id), "file")
			case "parent":
				link = filepath.Join(s.appDirFor(id), "escape-new")
				path = "escape-new/new/file"
			case "workspace":
				link = filepath.Join(mnt, "workspace")
			case "app":
				link = s.appDirFor(id)
			case "mount":
				link = mnt
			case "fifo":
				if err := unix.Mkfifo(filepath.Join(s.appDirFor(id), "file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if link != "" {
				if _, err := os.Lstat(link); err == nil {
					if err = os.Rename(link, link+"-saved"); err != nil {
						t.Fatal(err)
					}
				}
				target := outside
				if attack == "leaf" {
					target = canary
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			}
			w := putFileRequest(t, s, id, path, strings.NewReader("ATTACK"))
			if w.Code != 400 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			got, err := os.ReadFile(canary)
			if err != nil || string(got) != "untouched" {
				t.Fatal("outside file modified", err)
			}
			st, _ := os.Stat(canary)
			if st.Mode().Perm() != 0600 {
				t.Fatal("outside mode modified")
			}
			if _, err = os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
				t.Fatal("outside directory created")
			}
		})
	}
}

func TestPutFileConcurrentParentAndTemporaryReplacement(t *testing.T) {
	for _, attack := range []string{"parent", "app", "temporary", "leaf"} {
		t.Run(attack, func(t *testing.T) {
			s, id := fileSymlinkServer(t)
			app := s.appDirFor(id)
			parent := filepath.Join(app, "nested")
			if err := os.Mkdir(parent, 0755); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			canary := filepath.Join(outside, "file")
			if err := os.WriteFile(canary, []byte("secret"), 0600); err != nil {
				t.Fatal(err)
			}
			body := &fileMutationReader{reader: strings.NewReader("replacement"), mutate: func() {
				target := parent
				switch attack {
				case "app":
					target = app
				case "temporary":
					files, err := filepath.Glob(filepath.Join(parent, ".put-*.tmp"))
					if err != nil || len(files) != 1 {
						t.Fatalf("temp: %v %v", files, err)
					}
					if err = os.Remove(files[0]); err != nil {
						t.Fatal(err)
					}
					if err = os.Symlink(canary, files[0]); err != nil {
						t.Fatal(err)
					}
					return
				case "leaf":
					if err := os.Symlink(canary, filepath.Join(parent, "file")); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err := os.Rename(target, target+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, target); err != nil {
					t.Fatal(err)
				}
			}}
			w := putFileRequest(t, s, id, "nested/file", body)
			if w.Code != 400 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			got, _ := os.ReadFile(canary)
			if string(got) != "secret" {
				t.Fatal("outside write")
			}
			st, _ := os.Stat(canary)
			if st.Mode().Perm() != 0600 {
				t.Fatal("outside chmod")
			}
		})
	}
}

func TestPutFileRegularAtomicReplacementAndSize(t *testing.T) {
	s, id := fileSymlinkServer(t)
	path := ".motion-releases/reviewed/dist/asset.txt"
	for _, body := range []string{"first", "second"} {
		w := putFileRequest(t, s, id, path, strings.NewReader(body))
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		got, err := os.ReadFile(filepath.Join(s.appDirFor(id), path))
		if err != nil || string(got) != body {
			t.Fatal("content", err)
		}
	}
	st, err := os.Stat(filepath.Join(s.appDirFor(id), path))
	if err != nil || st.Mode().Perm() != 0644 {
		t.Fatal("mode", err)
	}
	_, mnt := s.Loopback.Paths(id)
	var owner, file unix.Stat_t
	if unix.Stat(mnt, &owner) != nil || unix.Stat(filepath.Join(s.appDirFor(id), path), &file) != nil || owner.Uid != file.Uid || owner.Gid != file.Gid {
		t.Fatal("ownership")
	}
	// Unknown Content-Length exercises the streaming cap, not only the header.
	w := putFileRequest(t, s, id, path, io.LimitReader(zeroFileReader{}, maxPutFileBytes+1))
	if w.Code != 413 {
		t.Fatalf("oversized status=%d", w.Code)
	}
	got, _ := os.ReadFile(filepath.Join(s.appDirFor(id), path))
	if string(got) != "second" {
		t.Fatal("failed write replaced original")
	}
	files, _ := filepath.Glob(filepath.Join(s.appDirFor(id), filepath.Dir(path), ".put-*.tmp"))
	if len(files) != 0 {
		t.Fatal("temporary leak")
	}
}

func TestPutFileReplacesHardlinkWithoutMutatingLinkedFile(t *testing.T) {
	s, id := fileSymlinkServer(t)
	outside := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(s.appDirFor(id), "linked")); err != nil {
		t.Fatal(err)
	}
	w := putFileRequest(t, s, id, "linked", strings.NewReader("new app file"))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "private" {
		t.Fatal("linked file modified", err)
	}
	st, _ := os.Stat(outside)
	if st.Mode().Perm() != 0600 {
		t.Fatal("linked file permissions changed")
	}
}

func TestFileReadUsesBoundedReader(t *testing.T) {
	s, id := fileSymlinkServer(t)
	path := filepath.Join(s.appDirFor(id), "large")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(maxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if w := getContent(t, s, id, "large"); w.Code != 400 {
		t.Fatalf("large status %d", w.Code)
	}
}

type zeroFileReader struct{}

func (zeroFileReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestReadDescriptorsDoNotFollowConcurrentDirectoryReplacement(t *testing.T) {
	s, id := fileSymlinkServer(t)
	app := s.appDirFor(id)
	if err := os.Mkdir(filepath.Join(app, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "nested", "file"), []byte("safe"), 0644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte(secretMarker), 0600); err != nil {
		t.Fatal(err)
	}
	_, mnt := s.Loopback.Paths(id)
	dirs, err := openAppDirs(mnt, "")
	if err != nil {
		t.Fatal(err)
	}
	defer dirs.close()
	var contents bytes.Buffer
	err = walkAppFiles(dirs.last(), "", true, func(path string, isDir bool, f *os.File) error {
		if path == "nested" && isDir {
			if err := os.Rename(filepath.Join(app, "nested"), filepath.Join(app, "saved")); err != nil {
				return err
			}
			return os.Symlink(outside, filepath.Join(app, "nested"))
		}
		if f != nil {
			_, err := io.Copy(&contents, f)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(contents.String(), secretMarker) || !strings.Contains(contents.String(), "safe") {
		t.Fatalf("unsafe content %q", contents.String())
	}
	if _, err = openAppDirs(mnt, "nested"); !errors.Is(err, errUnsafeFilePath) {
		t.Fatalf("new symlink accepted: %v", err)
	}
}

func TestFileReadRejectsRootSymlinkAndSpecialLeaf(t *testing.T) {
	s, id := fileSymlinkServer(t)
	if err := unix.Mkfifo(filepath.Join(s.appDirFor(id), "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := getContent(t, s, id, "pipe"); w.Code != 404 {
		t.Fatalf("fifo status %d", w.Code)
	}
	app := s.appDirFor(id)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte(secretMarker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(app, app+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, app); err != nil {
		t.Fatal(err)
	}
	if w := getContent(t, s, id, "file"); w.Code != 404 {
		t.Fatalf("root symlink status %d", w.Code)
	}
	for _, kind := range []string{"list", "export"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		if kind == "list" {
			s.v1ListFiles(w, r)
		} else {
			s.v1Export(w, r)
		}
		if w.Code != 404 {
			t.Fatalf("%s root symlink status %d", kind, w.Code)
		}
	}
}
