package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"golang.org/x/sys/unix"
)

// serveMigratedTaskEvents preserves original task replay without waking the
// stopped Docker source or mixing its supervisor identity into the new guest.
// Scope comes from the migration's durable pre-cutover task inventory, not a
// caller-selected path. Every path component is opened with O_NOFOLLOW.
func (s *Server) serveMigratedTaskEvents(w http.ResponseWriter, r *http.Request) bool {
	id, taskID := r.PathValue("id"), r.PathValue("taskId")
	sb, err := s.Store.Get(r.Context(), id)
	if err != nil || sb.RuntimeProvider != "cube" {
		return false
	}
	retained, err := s.Store.IsMigratedDockerTask(r.Context(), id, taskID)
	if err != nil {
		writeV1Err(w, 503, "history_unavailable", "cannot resolve task history")
		return true
	}
	if !retained {
		return false
	}
	if !sb.AppID.Valid {
		writeV1Err(w, 404, "not_found", "no such task")
		return true
	}
	if _, err = s.Store.GetAppForOwner(r.Context(), sb.AppID.String, tenantToken(r)); err != nil {
		writeV1Err(w, 404, "not_found", "no such task")
		return true
	}
	task, err := s.Store.GetTask(r.Context(), taskID)
	if err != nil || task.SandboxID != id || task.Status == "running" {
		writeV1Err(w, 404, "not_found", "no such finished task")
		return true
	}
	root := s.RetainedHistoryRoot
	if root == "" && !s.cubeOnly && s.Loopback != nil {
		root = s.Loopback.Root
	}
	if root == "" || !isULID(id) || !isULID(taskID) {
		writeV1Err(w, 503, "history_unavailable", "retained history storage is unavailable")
		return true
	}
	home := filepath.Join(root, id)
	file, err := openRetainedHistory(home, taskID)
	if err != nil {
		writeV1Err(w, 404, "not_found", "retained task events are unavailable")
		return true
	}
	defer file.Close()
	since := 0
	if value := r.Header.Get("Last-Event-ID"); value != "" {
		if n, e := strconv.Atoi(value); e == nil && n >= 0 && n < int(^uint(0)>>1) {
			since = n + 1
		}
	}
	if value := r.URL.Query().Get("since"); value != "" {
		if n, e := strconv.Atoi(value); e == nil && n >= 0 {
			since = n
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	_ = runtime.DecodeEvents(io.LimitReader(file, 128<<20), func(event runtime.Event) bool {
		if event.ID < since {
			return true
		}
		data := event.Data
		if len(data) == 0 {
			data = json.RawMessage("{}")
		}
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
		if flusher != nil {
			flusher.Flush()
		}
		return r.Context().Err() == nil
	})
	return true
}

func openRetainedHistory(home, taskID string) (*os.File, error) {
	// Loopback.Root is trusted operator configuration; reject a replaced sandbox
	// directory or any tenant-controlled symlink/hardlink below it.
	parent, err := unix.Open(filepath.Dir(home), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, filepath.Base(home), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range []string{".runtimed", "tasks", taskID} {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	leaf, err := unix.Openat(fd, "events.jsonl", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	unix.Close(fd)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err = unix.Fstat(leaf, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Size > 128<<20 {
		unix.Close(leaf)
		return nil, fmt.Errorf("invalid retained history file")
	}
	return os.NewFile(uintptr(leaf), "retained-task-events"), nil
}
