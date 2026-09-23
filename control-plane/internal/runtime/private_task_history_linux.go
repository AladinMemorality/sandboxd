package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const ImportedTaskHistoryMarker = ".history-imported"
const MaxPrivateTaskHistoryBytes = 64 << 20
const MaxPrivateTaskHistoryExpandedBytes = 256 << 20
const MaxPrivateTaskHistoryFileBytes = 32 << 20
const MaxPrivateTaskHistoryTasks = 10000

var privateTaskID = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

func ValidPrivateTaskID(id string) bool { return privateTaskID.MatchString(id) }
func ValidatePrivateTaskIDs(ids []string) error {
	if len(ids) > MaxPrivateTaskHistoryTasks {
		return errors.New("task history count limit")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !ValidPrivateTaskID(id) || seen[id] {
			return errors.New("invalid or duplicate task history ID")
		}
		seen[id] = true
	}
	return nil
}

type PrivateTaskHistoryRequest struct {
	TaskIDs []string `json:"task_ids"`
}

func readPrivateTaskFile(dir *os.File, name string) ([]byte, error) {
	f, e := privateChild(dir, name, false)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(int(f.Fd()), &st); e != nil {
		return nil, e
	}
	limit := int64(MaxPrivateTaskHistoryFileBytes)
	if name == "result.json" {
		limit = MaxFileReadBytes
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Size > limit {
		return nil, errors.New("invalid task history file")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("task history file limit")
	}
	return b, nil
}
func validPrivateTaskResult(id string, data []byte) bool {
	var result TaskResult
	if json.Unmarshal(data, &result) != nil || result.ID != id {
		return false
	}
	return result.Status == TaskSucceeded || result.Status == TaskFailed || result.Status == TaskCancelled
}

// Export requires the caller to prove task IDs belong to this sandbox's owner
// from durable task rows. It never enumerates or copies the whole runtime dir.
func ExportPrivateTaskHistory(ctx context.Context, tasksRoot string, ids []string) ([]byte, error) {
	if e := ValidatePrivateTaskIDs(ids); e != nil {
		return nil, e
	}
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	root, e := privateDirectory(tasksRoot)
	if e != nil && !(os.IsNotExist(e) && len(ids) == 0) {
		return nil, e
	}
	if root != nil {
		defer root.Close()
	}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	total := 0
	for _, id := range ids {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		dir, e := privateChild(root, id, true)
		if e != nil {
			return nil, e
		}
		result, e := readPrivateTaskFile(dir, "result.json")
		if e != nil || !validPrivateTaskResult(id, result) {
			dir.Close()
			return nil, errors.New("task history has no matching terminal result")
		}
		events, e := readPrivateTaskFile(dir, "events.jsonl")
		dir.Close()
		if e != nil {
			return nil, e
		}
		for _, file := range []struct {
			name string
			data []byte
		}{{"events.jsonl", events}, {"result.json", result}} {
			total += len(file.data)
			if total > MaxPrivateTaskHistoryExpandedBytes {
				return nil, errors.New("expanded task history limit")
			}
			h := &zip.FileHeader{Name: id + "/" + file.name, Method: zip.Deflate}
			h.SetMode(0644)
			w, e := z.CreateHeader(h)
			if e != nil {
				return nil, e
			}
			if _, e = w.Write(file.data); e != nil {
				return nil, e
			}
			if out.Len() > MaxPrivateTaskHistoryBytes {
				return nil, errors.New("compressed task history limit")
			}
		}
	}
	if e = z.Close(); e != nil {
		return nil, e
	}
	if out.Len() > MaxPrivateTaskHistoryBytes {
		return nil, errors.New("compressed task history limit")
	}
	return out.Bytes(), nil
}
func privateTaskHistoryFiles(data []byte) (map[string]map[string][]byte, error) {
	if len(data) > MaxPrivateTaskHistoryBytes {
		return nil, errors.New("compressed task history limit")
	}
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return nil, e
	}
	files := z.File
	if len(files) > MaxPrivateTaskHistoryTasks*2 {
		return nil, errors.New("task history count limit")
	}
	tasks := map[string]map[string][]byte{}
	seen := map[string]bool{}
	total := uint64(0)
	for _, f := range files {
		if seen[f.Name] {
			return nil, errors.New("duplicate task history path")
		}
		seen[f.Name] = true
		parts := strings.Split(f.Name, "/")
		if len(parts) != 2 || !ValidPrivateTaskID(parts[0]) || !f.Mode().IsRegular() || (parts[1] != "events.jsonl" && parts[1] != "result.json") {
			return nil, errors.New("task archive contains unexpected file")
		}
		limit := uint64(MaxPrivateTaskHistoryFileBytes)
		if parts[1] == "result.json" {
			limit = MaxFileReadBytes
		}
		total += f.UncompressedSize64
		if f.UncompressedSize64 > limit || total > MaxPrivateTaskHistoryExpandedBytes {
			return nil, errors.New("expanded task history limit")
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(r, int64(limit)+1))
		r.Close()
		if e != nil || uint64(len(b)) != f.UncompressedSize64 {
			return nil, errors.New("invalid task history content")
		}
		if tasks[parts[0]] == nil {
			tasks[parts[0]] = map[string][]byte{}
		}
		tasks[parts[0]][parts[1]] = b
	}
	for id, files := range tasks {
		if len(files) != 2 || !validPrivateTaskResult(id, files["result.json"]) {
			return nil, errors.New("task archive lacks matching terminal result")
		}
	}
	return tasks, nil
}
func ValidatePrivateTaskHistoryArchive(data []byte) error {
	_, e := privateTaskHistoryFiles(data)
	return e
}
func PrivateTaskHistoryIDs(data []byte) ([]string, error) {
	tasks, e := privateTaskHistoryFiles(data)
	if e != nil {
		return nil, e
	}
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// Install merges selected finalized tasks. Existing tasks must match exactly;
// unknown/colliding history is never overwritten. Every new task directory is
// installed atomically and durably; retries complete partial multi-task imports.
func InstallPrivateTaskHistory(tasksRoot string, data []byte) error {
	tasks, e := privateTaskHistoryFiles(data)
	if e != nil {
		return e
	}
	if len(tasks) == 0 {
		return nil
	}
	parent, e := privateDirectory(filepath.Dir(tasksRoot))
	if e != nil {
		return e
	}
	defer parent.Close()
	if e = unix.Mkdirat(int(parent.Fd()), filepath.Base(tasksRoot), 0755); e != nil && e != unix.EEXIST {
		return e
	}
	root, e := privateChild(parent, filepath.Base(tasksRoot), true)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = parent.Sync(); e != nil {
		return e
	}
	ids := make([]string, 0, len(tasks))
	skip := map[string]bool{}
	for id, files := range tasks {
		ids = append(ids, id)
		dir, e := privateChild(root, id, true)
		if errors.Is(e, unix.ENOENT) {
			continue
		}
		if e != nil {
			return e
		}
		for name, want := range files {
			got, e := readPrivateTaskFile(dir, name)
			if e != nil || !bytes.Equal(got, want) {
				dir.Close()
				return errors.New("existing task history conflicts with import")
			}
		}
		dir.Close()
		skip[id] = true
	}
	sort.Strings(ids)
	for _, id := range ids {
		if skip[id] {
			dir, e := privateChild(root, id, true)
			if e != nil {
				return e
			}
			e = markImportedTaskHistory(dir)
			dir.Close()
			if e != nil {
				return e
			}
			continue
		}
		stage, e := os.MkdirTemp(tasksRoot, ".task-import-")
		if e != nil {
			return e
		}
		install := func() error {
			defer os.RemoveAll(stage)
			for name, content := range tasks[id] {
				f, e := os.OpenFile(filepath.Join(stage, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
				if e != nil {
					return e
				}
				_, e = f.Write(content)
				if e == nil {
					e = f.Sync()
				}
				ce := f.Close()
				if e != nil {
					return e
				}
				if ce != nil {
					return ce
				}
			}
			if e = os.Chmod(stage, 0755); e != nil {
				return e
			}
			dir, e := privateDirectory(stage)
			if e != nil {
				return e
			}
			e = markImportedTaskHistory(dir)
			dir.Close()
			if e != nil {
				return e
			}
			if e = syncPrivateTree(stage); e != nil {
				return e
			}
			if e = unix.Renameat2(int(root.Fd()), filepath.Base(stage), int(root.Fd()), id, unix.RENAME_NOREPLACE); e != nil {
				return e
			}
			return root.Sync()
		}
		if e = install(); e != nil {
			return e
		}
	}
	return nil
}
func (c *Client) ExportPrivateTaskHistory(ctx context.Context, ids []string) ([]byte, error) {
	if e := ValidatePrivateTaskIDs(ids); e != nil {
		return nil, e
	}
	body, _ := json.Marshal(PrivateTaskHistoryRequest{TaskIDs: ids})
	return c.bounded(ctx, http.MethodPost, "/export/private-task-history", body, MaxPrivateTaskHistoryBytes)
}
func (c *Client) ImportPrivateTaskHistory(ctx context.Context, data []byte) error {
	if e := ValidatePrivateTaskHistoryArchive(data); e != nil {
		return e
	}
	_, e := c.bounded(ctx, http.MethodPut, "/import/private-task-history", data, 4096)
	return e
}

func markImportedTaskHistory(dir *os.File) error {
	// Replace the leaf atomically instead of truncating a possible hardlink.
	f, e := os.CreateTemp("/proc/self/fd/"+strconv.FormatUint(uint64(dir.Fd()), 10), ".history-marker-")
	if e != nil {
		return e
	}
	name := filepath.Base(f.Name())
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	_, e = f.Write([]byte("canonical history only; start a fresh agent session\n"))
	if e == nil {
		e = f.Chmod(0644)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = unix.Renameat(int(dir.Fd()), name, int(dir.Fd()), ImportedTaskHistoryMarker); e != nil {
		return e
	}
	return dir.Sync()
}
