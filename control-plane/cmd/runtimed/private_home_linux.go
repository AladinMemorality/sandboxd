package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func (a *app) handlePrivateHome(w http.ResponseWriter, r *http.Request) {
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	a.taskMu.Lock()
	allowed := a.workspaceQuiesced && !a.restartPending
	a.taskMu.Unlock()
	if !allowed {
		http.Error(w, "quiesced supervisor required", 409)
		return
	}
	if e := stopWorkspaceWriters(r.Context()); e != nil {
		http.Error(w, "cannot prove owner home quiescence", 503)
		return
	}
	var manifest runtime.HomeManifest
	var reader io.Reader
	if r.Method == http.MethodPost {
		reader = http.MaxBytesReader(w, r.Body, runtime.MaxHomeManifestBytes)
	} else {
		encoded := r.Header.Get("X-Home-Manifest")
		if len(encoded) > runtime.MaxHomeManifestBytes*4/3+4 {
			http.Error(w, "home manifest limit", 400)
			return
		}
		raw, e := base64.RawURLEncoding.Strict().DecodeString(encoded)
		if e != nil {
			http.Error(w, "invalid home manifest", 400)
			return
		}
		reader = bytes.NewReader(raw)
	}
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()
	if dec.Decode(&manifest) != nil {
		http.Error(w, "invalid home manifest", 400)
		return
	}
	var tail any
	if dec.Decode(&tail) != io.EOF {
		http.Error(w, "invalid home manifest", 400)
		return
	}
	if _, e := runtime.CanonicalHomeManifest(manifest); e != nil {
		http.Error(w, "invalid home manifest scope", 400)
		return
	}
	home := filepath.Dir(filepath.Dir(a.appDir))
	if filepath.Clean(home) != "/home/sandbox" || a.runtimeDir != filepath.Join(home, ".runtimed") {
		http.Error(w, "unsupported home layout", 409)
		return
	}
	f, e := os.CreateTemp(a.runtimeDir, ".private-home-")
	if e != nil {
		scopedError(w, e)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if r.Method == http.MethodPost {
		if e = runtime.ExportPrivateHome(r.Context(), home, manifest, f); e != nil {
			http.Error(w, "owner home export rejected", 422)
			return
		}
		size, e := f.Seek(0, io.SeekEnd)
		if e != nil {
			scopedError(w, e)
			return
		}
		if _, e = f.Seek(0, io.SeekStart); e != nil {
			scopedError(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		http.ServeContent(w, r, "owner-home.zip", time.Time{}, io.NewSectionReader(f, 0, size))
		return
	}
	n, e := io.Copy(f, http.MaxBytesReader(w, r.Body, runtime.MaxPrivateHomeBytes))
	if e != nil {
		http.Error(w, "owner home upload limit", 413)
		return
	}
	if e = f.Sync(); e != nil {
		scopedError(w, e)
		return
	}
	if e = runtime.InstallPrivateHome(r.Context(), home, manifest, f, n); e != nil {
		http.Error(w, "owner home import rejected", 422)
		return
	}
	writeJSON(w, 200, map[string]bool{"imported": true})
}
