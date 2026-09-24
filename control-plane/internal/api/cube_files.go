package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/audit"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

// serveCubeFiles resolves the immutable provider before touching any host path.
// Ownership is checked here too, so internal handler delegation stays scoped.
func (s *Server) serveCubeFiles(w http.ResponseWriter, r *http.Request, operation string) bool {
	if s.Store == nil {
		return false
	}
	id := r.PathValue("id")
	sb, err := s.Store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		writeV1Err(w, 503, "runtime_unavailable", "cannot resolve sandbox runtime")
		return true
	}
	if sb.RuntimeProvider == "" || sb.RuntimeProvider == "docker" {
		return false
	}
	if sb.RuntimeProvider != "cube" {
		writeV1Err(w, 503, "runtime_unavailable", "unsupported sandbox runtime")
		return true
	}
	if !sb.AppID.Valid {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return true
	}
	if _, err = s.Store.GetAppForOwner(r.Context(), sb.AppID.String, tenantToken(r)); err != nil {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return true
	}
	if s.Locks != nil {
		s.Locks.Lock(id)
		defer s.Locks.Unlock(id)
	}
	// Re-read under the lifecycle lock; deletion and pause must not race writes.
	sb, err = s.Store.Get(r.Context(), id)
	if err != nil {
		writeV1Err(w, 404, "not_found", "no such sandbox")
		return true
	}
	c := s.runtimeClientFor(id)
	probe, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	_, statusErr := c.Status(probe)
	cancel()
	if sb.Status != "running" || statusErr != nil {
		if err = s.connectCube(r.Context(), id, 0); err != nil {
			writeV1Err(w, 502, "runtime_unavailable", "cannot resume guest workspace")
			return true
		}
	}
	switch operation {
	case "v1ListFiles":
		var out *runtime.FileList
		out, err = c.ListFiles(r.Context(), r.URL.Query().Get("path"), r.URL.Query().Get("recursive") == "true")
		if err == nil {
			writeJSON(w, 200, out)
		}
	case "v1FileContent":
		var data []byte
		data, err = c.ReadFile(r.Context(), r.URL.Query().Get("path"))
		if err == nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(data)
		}
	case "v1Export":
		var data []byte
		data, err = c.ExportWorkspace(r.Context())
		if err == nil {
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.zip"`)
			w.Write(data)
		}
	case "v1PutFile":
		if r.ContentLength > runtime.MaxFileWriteBytes {
			writeV1Err(w, 413, "invalid_request", "file exceeds the 25 MiB limit")
			return true
		}
		body := io.LimitReader(r.Body, runtime.MaxFileWriteBytes+1)
		var out *runtime.FileWrite
		out, err = c.PutFile(r.Context(), r.URL.Query().Get("path"), body)
		if err == nil {
			s.auditAction(r, audit.Entry{Action: "file.put", Target: id, Detail: map[string]any{"path": out.Path, "size": out.Size}})
			writeJSON(w, 200, out)
		}
	case "v1ProcessLogs":
		tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
		var out *runtime.ProcessLog
		out, err = c.ProcessLogs(r.Context(), r.PathValue("name"), tail)
		if err == nil {
			writeJSON(w, 200, out)
		}
	default:
		writeV1Err(w, 501, "unsupported", "unsupported guest operation")
		return true
	}
	if err != nil {
		var guest *runtime.ResponseError
		if errors.As(err, &guest) {
			switch guest.StatusCode {
			case 400, 404, 409, 413:
				writeV1Err(w, guest.StatusCode, "guest_operation_failed", "guest workspace operation rejected")
				return true
			}
		}
		writeV1Err(w, 502, "runtime_unavailable", "guest workspace operation failed")
	}
	return true
}
