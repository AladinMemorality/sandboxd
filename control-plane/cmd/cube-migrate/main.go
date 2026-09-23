// cube-migrate is a local root/operator tool, never an authenticated tenant API.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/manifest"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/migration"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cube-migrate:", err)
		os.Exit(1)
	}
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func run(args []string) error {
	flags := flag.NewFlagSet("cube-migrate", flag.ContinueOnError)
	data := env("SANDBOXD_DATA_DIR", "/var/lib/sandboxd")
	database := flags.String("database", env("SANDBOXD_DB", filepath.Join(data, "state", "sandboxd.db")), "same absolute SQLite file as sandboxd")
	workspaces := flags.String("workspaces", filepath.Join(data, "workspaces"), "host workspace root")
	migrations := flags.String("migrations", "/usr/local/share/sandboxd/migrations", "schema migration directory")
	archives := flags.String("archives", filepath.Join(data, "migration-archives"), "private retained recovery archives")
	keyfile := flags.String("keyfile", filepath.Join(data, "secrets.key"), "existing sandboxd secrets keyfile")
	id := flags.String("sandbox", "", "stable sandbox ID")
	targetPreset := flags.String("preset", "", "reviewed target runtime preset")
	remote := flags.String("adopt-runtime", "", "recover a known Cube VM after uncertain creation")
	trafficFile := flags.String("traffic-token-file", "", "0600 file containing adoption ingress credential; never a command argument")
	if err := flags.Parse(args); err != nil {
		return err
	}
	action := "inventory"
	if flags.NArg() > 0 {
		action = flags.Arg(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if !filepath.IsAbs(*database) || !filepath.IsAbs(*workspaces) || !filepath.IsAbs(*archives) {
		return errors.New("database, workspaces and archives must be absolute paths")
	}
	uri := url.URL{Scheme: "file", Path: *database}
	if action == "inventory" || action == "status" || action == "rollback-check" {
		db, err := sql.Open("sqlite3", uri.String()+"?mode=ro&_busy_timeout=5000")
		if err != nil {
			return err
		}
		defer db.Close()
		if action == "rollback-check" {
			if *id == "" {
				return errors.New("--sandbox is required")
			}
			if err = migration.RollbackCheck(ctx, db, *id); err != nil {
				return err
			}
			fmt.Println("preliminary rollback config/task eligibility passed; rechecked under maintenance before quiescence")
			return nil
		}
		if action == "inventory" {
			rows, err := migration.Inventory(ctx, db, *workspaces, *id)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(rows)
		}
		rows, err := db.QueryContext(ctx, `SELECT sandbox_id,phase,template_id,runtime_id,archive_sha256,rollback_sha256 FROM runtime_migration WHERE (?='' OR sandbox_id=?)`, *id, *id)
		if err != nil {
			return err
		}
		defer rows.Close()
		values := []map[string]string{}
		for rows.Next() {
			var sid, phase, template, runtimeID, source, rollback string
			if err = rows.Scan(&sid, &phase, &template, &runtimeID, &source, &rollback); err != nil {
				return err
			}
			values = append(values, map[string]string{"sandbox_id": sid, "phase": phase, "template_id": template, "runtime_id": runtimeID, "source_sha256": source, "rollback_sha256": rollback})
		}
		if err = rows.Err(); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(values)
	}
	if action != "migrate" && action != "resume" && action != "rollback" && action != "rollback-check" && action != "adopt" && action != "abort" {
		return errors.New("action must be inventory, status, migrate, resume, rollback-check, rollback, adopt or abort (flags precede action)")
	}
	if *id == "" {
		return errors.New("--sandbox is required")
	}
	if os.Geteuid() != 0 {
		return errors.New("offline mutations require native host root")
	}
	if _, e := os.Stat("/.dockerenv"); e == nil {
		return errors.New("run offline migration natively on the host so legacy database users are visible")
	}
	lock, err := maintenance.Acquire(*database, true)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = maintenance.CheckDatabaseUsers(*database); err != nil {
		return err
	}
	st, err := store.Open(ctx, uri.String()+"?_journal=WAL&_busy_timeout=5000&_fk=1", *migrations)
	if err != nil {
		return err
	}
	defer st.Close()
	if os.Getenv("SANDBOXD_SECRETS_KEY") == "" {
		if _, err = os.Stat(*keyfile); err != nil {
			return errors.New("existing secrets key is required; migration must not generate a replacement")
		}
	}
	cipher, err := secrets.Load(os.Getenv("SANDBOXD_SECRETS_KEY"), *keyfile)
	if err != nil {
		return err
	}
	client, err := cube.New(cube.Config{APIURL: os.Getenv("SANDBOXD_CUBE_API_URL"), APIKey: os.Getenv("SANDBOXD_CUBE_API_KEY")})
	if err != nil {
		return err
	}
	backend := &migration.OfflineBackend{Store: st, Docker: docker.NewClient(), Cube: client, Secrets: cipher, ProxyURL: os.Getenv("SANDBOXD_CUBE_PROXY_URL"), ArchiveDir: *archives, WorkspaceRoot: *workspaces}
	engine := migration.Engine{Store: st, Backend: backend, BeforePhase: func() error { return maintenance.CheckDatabaseUsers(*database) }}
	if action == "migrate" {
		rows, err := migration.Inventory(ctx, st.DB(), *workspaces, *id)
		if err != nil {
			return err
		}
		if len(rows) != 1 || !rows[0].Eligible {
			if len(rows) == 1 {
				return fmt.Errorf("not eligible: %s", strings.Join(rows[0].Reasons, "; "))
			}
			return store.ErrNotFound
		}
		templates := map[string]string{}
		if err = json.Unmarshal([]byte(os.Getenv("SANDBOXD_CUBE_TEMPLATES")), &templates); err != nil {
			return err
		}
		if !preset.Valid(*targetPreset) || templates[*targetPreset] == "" {
			return errors.New("--preset must select a reviewed SANDBOXD_CUBE_TEMPLATES entry")
		}
		definition, _ := preset.Get(*targetPreset)
		parsed, e := manifest.Parse([]byte(definition.Manifest))
		if e != nil {
			return e
		}
		port := parsed.WebPort()
		if port <= 0 {
			port = 3000
		}
		source, e := st.Get(ctx, *id)
		if e != nil {
			return e
		}
		if len(source.Ports) != 1 || source.Ports[0] != port || (source.WebPort.Valid && source.WebPort.Int64 > 0 && int(source.WebPort.Int64) != port) {
			return errors.New("source preview ports differ from the reviewed target preset; preserving URLs requires a compatible template")
		}
		domain := os.Getenv("SANDBOXD_CUBE_DOMAIN")
		if domain == "" || strings.ContainsAny(domain, "/:\\ \r\n") {
			return errors.New("invalid Cube domain")
		}
		if err = st.BeginRuntimeMigration(ctx, *id, *targetPreset, templates[*targetPreset], domain); err != nil {
			return err
		}
	}
	switch action {
	case "rollback":
		err = engine.Rollback(ctx, *id)
	case "abort":
		err = backend.Abort(ctx, *id)
	case "adopt":
		m, e := st.GetRuntimeMigration(ctx, *id)
		if e != nil {
			return e
		}
		token := ""
		if *trafficFile != "" {
			info, e := os.Lstat(*trafficFile)
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 8192 {
				return errors.New("traffic token must be in a regular 0600 file of at most 8192 bytes")
			}
			raw, e := os.ReadFile(*trafficFile)
			if e != nil {
				return e
			}
			token = strings.TrimSpace(string(raw))
		}
		err = backend.AdoptTarget(ctx, m, *remote, token)
	default:
		err = engine.Run(ctx, *id)
	}
	if err != nil {
		return err
	}
	m, err := st.GetRuntimeMigration(ctx, *id)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"sandbox_id": *id, "phase": m.Phase})
}
