package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/audit"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

const cubeSourceFormat = "cube-source-v1"
const cubeSourcePresetPrefix = "cube-preset:"

// Cube snapshots published through the product are source artifacts, never VM
// memory snapshots. Their privacy boundary is the existing API tenant; a fork
// may change external user/project IDs but must never inherit source app config.
func (s *Server) createCubeSourceSnapshot(w http.ResponseWriter, r *http.Request, src *store.Sandbox, req v1CreateSnapshotReq) {
	app, err := s.Store.GetAppForOwner(r.Context(), src.AppID.String, tenantToken(r))
	if err != nil {
		writeV1Err(w, 404, "not_found", "no such source sandbox")
		return
	}
	if s.Cube == nil {
		writeV1Err(w, 503, "runtime_unavailable", "Cube is disabled")
		return
	}
	if s.Locks != nil {
		s.Locks.Lock(src.ID)
		defer s.Locks.Unlock(src.ID)
	}
	binding, err := s.Store.GetRuntimeBinding(r.Context(), src.ID)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "source runtime unavailable")
		return
	}
	preset := app.RuntimePreset.String
	if s.CubeTemplates[preset] != binding.TemplateID {
		preset = ""
		names := []string{}
		for name, id := range s.CubeTemplates {
			if id == binding.TemplateID {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) > 0 {
			preset = names[0]
		}
	}
	if preset == "" {
		writeV1Err(w, 409, "unsupported_template", "source template is no longer enabled")
		return
	}
	if err = s.connectCube(r.Context(), src.ID, 0); err != nil {
		if writeCubeAdmissionError(w, err) {
			return
		}
		writeV1Err(w, 502, "runtime_unavailable", "cannot resume source")
		return
	}
	client := s.runtimeClientFor(src.ID)
	status, err := client.Status(r.Context())
	if err != nil {
		writeV1Err(w, 502, "runtime_unavailable", "cannot inspect source")
		return
	}
	if status.ActiveTask != nil {
		writeV1Err(w, 409, "task_in_progress", "wait for the active task before publishing")
		return
	}
	archive, err := client.ExportSource(r.Context())
	if err == nil {
		archive, err = runtime.SanitizeSourceArchive(archive)
	}
	if err != nil {
		writeV1Err(w, 422, "source_export_failed", "cannot produce a bounded source artifact")
		return
	}
	id := newULID()
	path := filepath.Join(s.LibraryRoot, id+".cube.zip")
	if err = os.MkdirAll(s.LibraryRoot, 0750); err != nil {
		writeV1Err(w, 500, "internal", "cannot create artifact directory")
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0440)
	if err != nil {
		writeV1Err(w, 500, "internal", "cannot create source artifact")
		return
	}
	_, err = f.Write(archive)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		writeV1Err(w, 500, "internal", "cannot persist source artifact")
		return
	}
	_ = fsyncPath(s.LibraryRoot)
	snap := &store.Snapshot{ID: id, Name: req.Name, OwnerToken: tenantToken(r), SourceSandboxID: sql.NullString{String: src.ID, Valid: true}, SourceAppID: src.AppID, CreatedByUserID: src.ExternalUserID, BaseImage: cubeSourcePresetPrefix + preset, Visibility: "private", Format: cubeSourceFormat, Status: "ready", ImagePath: path, SizeBytes: sql.NullInt64{Int64: int64(len(archive)), Valid: true}}
	persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = s.Store.CreateSnapshot(persist, snap); err != nil {
		writeV1Err(w, 500, "internal", "cannot record source artifact")
		return
	}
	s.auditAction(r, audit.Entry{Action: "snapshot.create", Target: id, Detail: map[string]any{"source_sandbox_id": src.ID, "format": cubeSourceFormat}})
	writeJSON(w, 201, v1SnapshotFromRow(snap))
}
func (s *Server) readCubeSource(snap *store.Snapshot) ([]byte, string, error) {
	if snap.Format != cubeSourceFormat || !isULID(snap.ID) || !strings.HasPrefix(snap.BaseImage, cubeSourcePresetPrefix) {
		return nil, "", errors.New("invalid Cube source artifact")
	}
	preset := strings.TrimPrefix(snap.BaseImage, cubeSourcePresetPrefix)
	if s.Cube == nil || s.CubeTemplates[preset] == "" {
		return nil, "", errors.New("Cube source template unavailable")
	}
	expected := filepath.Join(s.LibraryRoot, snap.ID+".cube.zip")
	if snap.ImagePath != expected {
		return nil, "", errors.New("invalid source artifact path")
	}
	f, err := os.Open(expected)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, runtime.MaxWorkspaceExportBytes+1))
	if err != nil {
		return nil, "", err
	}
	data, err = runtime.SanitizeSourceArchive(data)
	return data, preset, err
}
func sourceError(code int, message string) (int, []byte) {
	data, _ := json.Marshal(map[string]string{"error": message})
	return code, data
}

// Old published cards retain their snapshot IDs. Only the frozen app subtree
// is converted; home data, runtime identity and creator credentials never enter
// a remix. Unknown legacy presets require operator repair, not a guessed image.
func (s *Server) readSourceForCube(ctx context.Context, snap *store.Snapshot) ([]byte, string, error) {
	if snap.Format == cubeSourceFormat {
		return s.readCubeSource(snap)
	}
	if snap.Format != "raw" || !isULID(snap.ID) || !snap.SourceAppID.Valid || s.LibraryRoot == "" ||
		snap.ImagePath != filepath.Join(s.LibraryRoot, snap.ID) {
		return nil, "", errors.New("unsupported legacy source artifact")
	}
	app, err := s.Store.GetAppForOwner(ctx, snap.SourceAppID.String, snap.OwnerToken)
	if err != nil {
		return nil, "", errors.New("legacy source owner unavailable")
	}
	preset := app.RuntimePreset.String
	if s.Cube == nil || preset == "" || s.CubeTemplates[preset] == "" {
		return nil, "", errors.New("legacy source requires a reviewed Cube preset")
	}
	archive, err := runtime.ExportPublishedDirectory(ctx, filepath.Join(snap.ImagePath, "workspace", "app"))
	return archive, preset, err
}

func (s *Server) createCubeFromSource(r *http.Request, app *store.App, snap *store.Snapshot) (int, []byte) {
	archive, preset, err := s.readSourceForCube(r.Context(), snap)
	if err != nil {
		return sourceError(422, "source artifact unavailable or invalid")
	}
	return s.createCubeFromArchive(r, app, archive, preset)
}

func (s *Server) createCubeFromArchive(r *http.Request, app *store.App, archive []byte, preset string) (int, []byte) {
	code, body := s.delegate(r, func(w http.ResponseWriter, req *http.Request) {
		s.createCubeAppSandbox(w, req, app, v1CreateAppSandboxReq{RuntimePreset: preset}, preset)
	}, http.MethodPost, "/sandbox", nil, nil)
	if code != 201 {
		return code, body
	}
	var created sandboxResp
	if json.Unmarshal(body, &created) != nil || created.ID == "" {
		return sourceError(502, "invalid created runtime")
	}
	if s.Locks != nil {
		s.Locks.Lock(created.ID)
		defer s.Locks.Unlock(created.ID)
	}
	fail := func(message string) (int, []byte) {
		persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Store.MarkError(persist, created.ID, message)
		return sourceError(502, message)
	}
	client := s.runtimeClientFor(created.ID)
	before, err := client.Status(r.Context())
	if err != nil {
		return fail("new supervisor unavailable")
	}
	if err = client.ImportSource(r.Context(), archive); err != nil {
		return fail("source import failed; new runtime retained for recovery")
	}
	ready, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	for {
		status, e := client.Status(ready)
		if e == nil && status.Runtimed.BootedAt.After(before.Runtimed.BootedAt) {
			// Supervisor boot after atomic import proves the manifest was reloaded.
			// Public preview status remains truthful while dependencies/install complete.
			return 201, body
		}
		select {
		case <-ready.Done():
			return fail("source imported but restarted supervisor is not ready")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Restore published source inside the existing owner's VM. The private home
// can contain a persistent database, uploads and owner tools: replacing the VM
// with a source-only artifact would silently destroy all of them. Fork still
// creates a fresh VM and never inherits this private data.
func (s *Server) restoreCubeSource(w http.ResponseWriter, r *http.Request, app *store.App, snap *store.Snapshot) bool {
	if snap.Format != cubeSourceFormat {
		bound, err := s.Store.AppUsesCube(r.Context(), app.ID)
		if err != nil {
			writeV1Err(w, 503, "runtime_unavailable", "cannot resolve app runtime")
			return true
		}
		if !bound && !s.CubeAllApps && !s.CubeApps[app.ID] {
			return false
		}
	}
	archive, preset, err := s.readSourceForCube(r.Context(), snap)
	if err != nil {
		writeV1Err(w, 422, "source_artifact_invalid", "source artifact unavailable or invalid")
		return true
	}
	if s.Locks != nil {
		s.Locks.Lock("cube-app:" + app.ID)
		defer s.Locks.Unlock("cube-app:" + app.ID)
	}
	if current, err := s.Store.CurrentSandboxForApp(r.Context(), app.ID); err == nil {
		if current.RuntimeProvider != "cube" {
			writeV1Err(w, 409, "runtime_mismatch", "Cube source restore cannot replace a Docker runtime")
			return true
		}
		s.restoreCubeSourceInPlace(w, r, app, snap, current, archive, s.CubeTemplates[preset])
		return true
	} else if !errors.Is(err, store.ErrNotFound) {
		writeV1Err(w, 503, "runtime_unavailable", "cannot inspect current runtime")
		return true
	}
	code, body := s.createCubeFromArchive(r, app, archive, preset)
	if code != 201 {
		relayV1Error(w, code, body)
		return true
	}
	s.auditAction(r, audit.Entry{Action: "app.restore", Target: app.ID, Detail: map[string]any{"snapshot_id": snap.ID}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(body)
	return true
}

func (s *Server) restoreCubeSourceInPlace(w http.ResponseWriter, r *http.Request, app *store.App, snap *store.Snapshot, current *store.Sandbox, archive []byte, template string) {
	if s.Locks != nil {
		s.Locks.Lock(current.ID)
		defer s.Locks.Unlock(current.ID)
	}
	binding, err := s.Store.GetRuntimeBinding(r.Context(), current.ID)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot inspect current runtime template")
		return
	}
	if binding.TemplateID != template {
		writeV1Err(w, 409, "source_template_mismatch", "source requires a different runtime template; migrate the runtime while preserving private data first")
		return
	}
	active, err := s.Store.SandboxHasRunningTask(r.Context(), current.ID)
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot inspect active tasks")
		return
	}
	if active {
		writeV1Err(w, 409, "task_in_progress", "finish the active task before restoring source")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err = s.connectCube(ctx, current.ID, 3600); err != nil {
		if writeCubeAdmissionError(w, err) {
			return
		}
		writeV1Err(w, 502, "runtime_unavailable", "existing runtime unavailable; private data retained")
		return
	}
	client := s.runtimeClientFor(current.ID)
	before, err := client.Status(ctx)
	if err != nil {
		writeV1Err(w, 502, "runtime_unavailable", "existing supervisor unavailable; private data retained")
		return
	}
	if before.ActiveTask != nil {
		writeV1Err(w, 409, "task_in_progress", "finish the active task before restoring source")
		return
	}
	// The authenticated supervisor validates/prepares a separate source tree,
	// exchanges only workspace/app, and self-execs with the same credentials.
	// Failure never falls back to deleting the VM or creating an empty home.
	if err = client.ImportSource(ctx, archive); err != nil {
		writeV1Err(w, 502, "source_import_failed", "source import failed; existing runtime retained for recovery")
		return
	}
	for {
		status, e := client.Status(ctx)
		if e == nil && status.Runtimed.BootedAt.After(before.Runtimed.BootedAt) {
			updated, e := s.Store.Get(ctx, current.ID)
			if e != nil {
				writeV1Err(w, 503, "runtime_unavailable", "source imported; runtime status unavailable")
				return
			}
			s.auditAction(r, audit.Entry{Action: "app.restore", Target: app.ID, Detail: map[string]any{"snapshot_id": snap.ID, "private_home_preserved": true}})
			writeJSON(w, http.StatusCreated, s.v1SandboxFromRow(r, updated))
			return
		}
		select {
		case <-ctx.Done():
			writeV1Err(w, 502, "runtime_unavailable", "source imported; supervisor readiness pending; private data retained")
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
