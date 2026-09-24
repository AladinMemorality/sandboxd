package migration

import (
	"context"
	"database/sql"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type InventoryRow struct {
	SandboxID              string              `json:"sandbox_id"`
	AppID                  string              `json:"app_id"`
	Provider               string              `json:"provider"`
	Status                 string              `json:"status"`
	ActiveTasks            int                 `json:"active_tasks"`
	AppBytes               int64               `json:"app_bytes"`
	AppEntries             int                 `json:"app_entries"`
	HardlinkedFiles        int                 `json:"hardlinked_files"`
	CompressedArchiveLimit int64               `json:"compressed_archive_limit"`
	UnhandledHomeEntries   int                 `json:"unhandled_home_entries"`
	UnhandledHomePaths     []string            `json:"unhandled_home_paths,omitempty"`
	HomeReport             *runtime.HomeReport `json:"home_report,omitempty"`
	Eligible               bool                `json:"eligible"`
	Reasons                []string            `json:"reasons,omitempty"`
}

// RollbackCheck is a preliminary read-only eligibility report. The engine
// rechecks these predicates under exclusive maintenance before quiescing Cube.
func RollbackCheck(ctx context.Context, db *sql.DB, id string) error {
	var phase, appID string
	err := db.QueryRowContext(ctx, `SELECT m.phase,s.app_id FROM runtime_migration m JOIN sandbox s ON s.id=m.sandbox_id WHERE m.sandbox_id=?`, id).Scan(&phase, &appID)
	if err != nil {
		return err
	}
	if phase != "complete" {
		return errors.New("rollback eligibility requires a completed migration")
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task WHERE sandbox_id=? AND status='running'`, id).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("active task prevents rollback")
	}
	_, err = store.RuntimeConfigFingerprintDB(ctx, db, appID)
	if err != nil {
		return err
	}
	// A changed fingerprint now selects guarded recreation; eligibility remains
	// preliminary until the backend validates source identity and config delivery.
	return nil
}

// Inventory is genuinely read-only: it accepts a SQLite mode=ro connection,
// does not apply migrations, does not stop workloads, and does not read file
// contents or decrypted app config. Size is an estimate while apps are running.
func Inventory(ctx context.Context, db *sql.DB, workspaceRoot, id string) ([]InventoryRow, error) {
	return InventoryWithHome(ctx, db, workspaceRoot, id, nil)
}

func InventoryWithHome(ctx context.Context, db *sql.DB, workspaceRoot, id string, homes map[string]runtime.HomeManifest) ([]InventoryRow, error) {
	provider := "'docker'"
	columns, err := db.QueryContext(ctx, `PRAGMA table_info(sandbox)`)
	if err != nil {
		return nil, err
	}
	for columns.Next() {
		var index, notnull, pk int
		var name, kind string
		var value any
		if err = columns.Scan(&index, &name, &kind, &notnull, &value, &pk); err != nil {
			columns.Close()
			return nil, err
		}
		if name == "runtime_provider" {
			provider = "runtime_provider"
		}
	}
	columns.Close()
	rows, err := db.QueryContext(ctx, `SELECT id,COALESCE(app_id,''),status,COALESCE(container_id,''),workspace_mnt,`+provider+` FROM sandbox WHERE (?='' OR id=?) ORDER BY id`, id, id)
	if err != nil {
		return nil, err
	}
	type raw struct {
		row              InventoryRow
		container, mount string
	}
	items := []raw{}
	for rows.Next() {
		var v raw
		if err = rows.Scan(&v.row.SandboxID, &v.row.AppID, &v.row.Status, &v.container, &v.mount, &v.row.Provider); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []InventoryRow{}
	for _, item := range items {
		row := item.row
		if row.SandboxID == "" || strings.ContainsAny(row.SandboxID, "/\\.") {
			row.Reasons = append(row.Reasons, "unsupported sandbox identity")
			out = append(out, row)
			continue
		}
		row.CompressedArchiveLimit = runtime.MaxPrivateWorkspaceStreamBytes
		categories := map[string]bool{}
		if row.Provider != "docker" {
			row.Reasons = append(row.Reasons, "provider is not Docker")
		}
		if row.AppID == "" {
			row.Reasons = append(row.Reasons, "sandbox has no durable app owner")
		}
		if item.container == "" {
			row.Reasons = append(row.Reasons, "source container identity missing")
		}
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task WHERE sandbox_id=? AND status='running'`, row.SandboxID).Scan(&row.ActiveTasks); err != nil {
			return nil, err
		}
		if row.ActiveTasks > 0 {
			row.Reasons = append(row.Reasons, "active coding tasks must finish")
		}
		ids, queryErr := db.QueryContext(ctx, `SELECT task_id FROM task WHERE sandbox_id=?`, row.SandboxID)
		if queryErr != nil {
			return nil, queryErr
		}
		historyMissing := false
		taskCount := 0
		for ids.Next() {
			var taskID string
			if err = ids.Scan(&taskID); err != nil {
				ids.Close()
				return nil, err
			}
			taskCount++
			if !runtime.ValidPrivateTaskID(taskID) {
				historyMissing = true
				continue
			}
			for _, name := range []string{"events.jsonl", "result.json"} {
				info, e := os.Lstat(filepath.Join(workspaceRoot, row.SandboxID, ".runtimed", "tasks", taskID, name))
				if e != nil || !info.Mode().IsRegular() {
					historyMissing = true
				}
			}
		}
		err = ids.Err()
		ids.Close()
		if err != nil {
			return nil, err
		}
		if historyMissing {
			row.Reasons = append(row.Reasons, "canonical task history is missing or unsafe; repair before migration")
		}
		if taskCount > runtime.MaxPrivateTaskHistoryTasks {
			row.Reasons = append(row.Reasons, "task history exceeds transfer count limit")
		}
		home := filepath.Join(workspaceRoot, row.SandboxID)
		if row.SandboxID == "" || strings.ContainsAny(row.SandboxID, "/\\.") {
			row.Reasons = append(row.Reasons, "unsupported sandbox identity")
			out = append(out, row)
			continue
		}
		if item.mount != "" && filepath.Clean(item.mount) != home {
			row.Reasons = append(row.Reasons, "workspace is not the supported directory layout")
			out = append(out, row)
			continue
		}
		app := filepath.Join(home, "workspace", "app")
		ancestrySafe := true
		for _, directory := range []string{home, filepath.Join(home, "workspace"), app} {
			info, e := os.Lstat(directory)
			if e != nil || !info.IsDir() {
				ancestrySafe = false
				break
			}
		}
		if !ancestrySafe {
			row.Reasons = append(row.Reasons, "workspace ancestry contains a link or is unavailable")
			out = append(out, row)
			continue
		}
		if info, e := os.Lstat(app); e != nil || !info.IsDir() {
			row.Reasons = append(row.Reasons, "workspace app directory unavailable")
			out = append(out, row)
			continue
		}
		err = filepath.WalkDir(home, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if e = ctx.Err(); e != nil {
				return e
			}
			if path == home {
				return nil
			}
			rel, e := filepath.Rel(home, path)
			if e != nil {
				return e
			}
			if path == app || strings.HasPrefix(path, app+string(os.PathSeparator)) {
				if path == app {
					return nil
				}
				row.AppEntries++
				info, e := d.Info()
				if e != nil {
					return e
				}
				if info.Mode().IsRegular() {
					row.AppBytes += info.Size()
					if info.Size() > runtime.MaxPrivateWorkspaceStreamFileBytes {
						row.Reasons = append(row.Reasons, "app contains a file exceeding the private archive limit")
					}
					if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
						row.HardlinkedFiles++
					}
				}
				if info.Mode()&os.ModeSymlink != 0 {
					target, e := os.Readlink(path)
					if e != nil {
						return e
					}
					name, e := filepath.Rel(app, path)
					if e != nil || !runtime.ValidPrivateWorkspaceLink(filepath.ToSlash(name), target) {
						row.Reasons = append(row.Reasons, "app contains an absolute or escaping symlink requiring a compatibility adapter")
					}
				}
				if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() && !info.Mode().IsRegular() {
					row.Reasons = append(row.Reasons, "app contains unsupported special files")
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			// Runtime socket/lock/PID are transient and deliberately never transported.
			if rel == ".runtimed/sock" || rel == ".runtimed/supervisor.lock" || rel == ".runtimed/runtimed.pid" {
				return nil
			}
			if strings.HasPrefix(rel, ".runtimed/tasks/") || rel == ".runtimed/web.log" || rel == ".runtimed/gateway.log" {
				return nil
			}
			row.UnhandledHomeEntries++
			categories[strings.SplitN(rel, string(os.PathSeparator), 2)[0]] = true
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			row.Reasons = append(row.Reasons, "workspace cannot be fully inventoried")
		}
		for category := range categories {
			row.UnhandledHomePaths = append(row.UnhandledHomePaths, category)
		}
		sort.Strings(row.UnhandledHomePaths)
		if plan, ok := homes[row.SandboxID]; ok {
			report, e := runtime.ValidateHomeManifest(ctx, home, plan)
			row.HomeReport = &report
			if e != nil {
				row.Reasons = append(row.Reasons, "owner home manifest validation failed")
			} else if !report.Eligible {
				row.Reasons = append(row.Reasons, report.Reasons...)
			}
		} else if len(row.UnhandledHomePaths) > 0 {
			row.Reasons = append(row.Reasons, "owner data outside workspace/app requires a reviewed home/history transfer adapter")
		}
		if row.AppBytes > runtime.MaxPrivateWorkspaceStreamExpandedBytes || row.AppEntries > runtime.MaxPrivateWorkspaceEntries {
			row.Reasons = append(row.Reasons, "workspace exceeds private archive limits")
		}
		row.Eligible = len(row.Reasons) == 0
		out = append(out, row)
	}
	return out, nil
}
