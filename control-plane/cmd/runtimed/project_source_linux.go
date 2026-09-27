package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/manifest"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/projectsource"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func (a *app) handleProjectSourceExport(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("RUNTIMED_PROJECT_DEPLOYMENTS") != "canary" {
		http.NotFound(w, r)
		return
	}
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	a.taskMu.Lock()
	allowed := a.workspaceQuiesced && !a.restartPending
	a.taskMu.Unlock()
	if !allowed {
		http.Error(w, "quiesced workspace required", 409)
		return
	}
	if err := stopWorkspaceWriters(r.Context()); err != nil {
		http.Error(w, "cannot prove workspace quiescence", 503)
		return
	}
	var recipe projectsource.Recipe
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&recipe); err != nil {
		http.Error(w, "invalid recipe", 400)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		http.Error(w, "trailing recipe data", 400)
		return
	}
	source, m, err := runtime.ExportProjectSource(r.Context(), a.appDir, recipe)
	if err != nil {
		http.Error(w, "source inventory requires review", 422)
		return
	}
	mw := multipart.NewWriter(w)
	w.Header().Set("Content-Type", mw.FormDataContentType())
	part, err := mw.CreateFormField("manifest")
	if err != nil {
		return
	}
	if err = json.NewEncoder(part).Encode(m); err != nil {
		return
	}
	part, err = mw.CreateFormFile("source", "source.zip")
	if err != nil {
		return
	}
	if _, err = part.Write(source); err != nil {
		return
	}
	_ = mw.Close()
}

// This opt-in import is for a NEW, disposable target guest. The source host is
// still fenced by the coordinator. It does not restore persistent data, secrets,
// task history or routing and must not be enabled as an in-place restore API.
func (a *app) handleProjectSourceImport(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("RUNTIMED_PROJECT_DEPLOYMENTS") != "canary" {
		http.NotFound(w, r)
		return
	}
	a.workspaceMu.Lock()
	defer a.workspaceMu.Unlock()
	a.taskMu.Lock()
	allowed := a.workspaceQuiesced && !a.restartPending && a.requestRestart != nil
	a.taskMu.Unlock()
	if !allowed {
		http.Error(w, "quiesced disposable target required", 409)
		return
	}
	if err := stopWorkspaceWriters(r.Context()); err != nil {
		http.Error(w, "cannot prove target quiescence", 503)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, projectsource.MaxBytes+(2<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "multipart manifest and source required", 400)
		return
	}
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "manifest" {
		http.Error(w, "manifest must be first", 400)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(part, (1<<20)+1))
	part.Close()
	if err != nil || len(raw) > 1<<20 {
		http.Error(w, "manifest too large", 413)
		return
	}
	var m projectsource.Manifest
	if err = json.Unmarshal(raw, &m); err != nil || m.Recipe.RuntimeImage != os.Getenv("RUNTIMED_PROJECT_IMAGE") {
		http.Error(w, "runtime image or manifest differs", 422)
		return
	}
	if len(m.Recipe.DataPaths) != 0 || len(m.Recipe.SecretPaths) != 0 {
		http.Error(w, "stateful project restore is not enrolled", 409)
		return
	}
	part, err = mr.NextPart()
	if err != nil || part.FormName() != "source" {
		http.Error(w, "source must be second", 400)
		return
	}
	source, err := io.ReadAll(io.LimitReader(part, projectsource.MaxBytes+1))
	part.Close()
	if err != nil || len(source) > projectsource.MaxBytes {
		http.Error(w, "source too large", 413)
		return
	}
	if _, err = mr.NextPart(); err != io.EOF {
		http.Error(w, "unexpected multipart data", 400)
		return
	}
	if err = projectsource.Verify(source, m); err != nil {
		http.Error(w, "source verification failed", 422)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	err = runtime.InstallPrivateWorkspacePrepared(a.appDir, source, func(staged string) error {
		if e := validateProjectCommands(staged, m.Recipe); e != nil {
			return e
		}
		if e := prepareSourceDependencies(ctx, staged, a.appDir); e != nil {
			return e
		}
		if len(m.Recipe.Build) > 0 {
			home, e := os.MkdirTemp("", "project-build-home-")
			if e != nil {
				return e
			}
			defer os.RemoveAll(home)
			env := []string{"PATH=/usr/local/bin:/usr/bin:/bin:/home/sandbox/.local/bin:/home/sandbox/.bun/bin", "HOME=" + home, "CI=true"}
			if e = runDependencyCommand(ctx, staged, env, m.Recipe.Build[0], m.Recipe.Build[1:]...); e != nil {
				return e
			}
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		return stopWorkspaceWriters(ctx)
	})
	if err != nil {
		http.Error(w, "project preparation failed; previous tree retained unless exchange completed", 422)
		return
	}
	a.taskMu.Lock()
	a.restartPending = true
	a.taskMu.Unlock()
	writeJSON(w, 200, map[string]any{"prepared": true, "source_sha256": m.SourceSHA256, "dependency_key": m.DependencyKey, "restarting": true, "quiesced": true, "deployment_ready": false})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() { time.Sleep(100 * time.Millisecond); a.requestRestart() }()
}
func validateProjectCommands(root string, r projectsource.Recipe) error {
	// Use the existing supervisor contract so a prepared recipe cannot start a
	// different command after reexec. Background workers need explicit enrollment.
	raw, err := os.ReadFile(filepath.Join(root, "sandbox.yaml"))
	if err != nil {
		return err
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return err
	}
	if m.Web == nil || len(m.Workers) > 0 || m.Web.Port != r.Port || m.Web.HealthPath != r.HealthPath || !reflect.DeepEqual(r.Start, []string{"sh", "-lc", m.Web.Command}) {
		return errors.New("recipe must match sandbox.yaml web process")
	}
	if r.PackageManager != "none" {
		binary, args, e := nodeDependencyCommand(root)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(append([]string{binary}, args...), r.Install) {
			return errors.New("selected installer differs from deployment recipe")
		}
	}
	return nil
}
