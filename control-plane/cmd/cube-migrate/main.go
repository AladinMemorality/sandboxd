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
	"github.com/tastyeffectco/sandboxd/control-plane/internal/migration"
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
	batchPath := flags.String("batch", "", "private reviewed batch of up to four independent projects; migrate stops after import")
	admissionKey := flags.String("admission-key", "", "durable app:APP_ID admission identity for fenced recovery")
	providerDrained := flags.Bool("provider-requests-drained", false, "operator verified all previous provider mutations terminated; required for pending known-runtime recovery")
	homeManifests := flags.String("home-manifests", "", "reviewed JSON map of sandbox IDs to private owner-home manifests")
	expectedFleet := flags.String("expected-fleet", "", "identity_sha256 from the reviewed fleet plan; checked before every phase")
	templateResources := flags.String("template-resources", "", "reviewed JSON map of template IDs to CPU milli and memory bytes; forward migration requires it")
	fleetPresets := flags.String("fleet-presets", "", "reviewed JSON map of app IDs to target presets")
	library := flags.String("library", filepath.Join(data, "library"), "snapshot library root for fleet preflight")
	targetPreset := flags.String("preset", "", "reviewed target runtime preset")
	stopAfterImport := flags.Bool("stop-after-import", false, "leave the journaled target quiesced after import; explicit resume is required for verification and provider commit")
	remote := flags.String("adopt-runtime", "", "recover a known Cube VM after uncertain creation")
	trafficFile := flags.String("traffic-token-file", "", "0600 file containing adoption ingress credential; never a command argument")
	if err := flags.Parse(args); err != nil {
		return err
	}
	action := "inventory"
	if flags.NArg() > 0 {
		action = flags.Arg(0)
	}
	if *stopAfterImport && action != "migrate" && action != "resume" {
		return errors.New("--stop-after-import requires migrate or resume")
	}
	batch, err := readBatch(*batchPath, action, *id, *targetPreset, *expectedFleet, *stopAfterImport)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if !filepath.IsAbs(*database) || !filepath.IsAbs(*workspaces) || !filepath.IsAbs(*archives) {
		return errors.New("database, workspaces and archives must be absolute paths")
	}
	homes, err := migration.ReadHomeManifests(*homeManifests)
	if err != nil {
		return err
	}
	resources, err := migration.ReadTemplateResources(*templateResources)
	if err != nil {
		return err
	}
	uri := url.URL{Scheme: "file", Path: *database}
	if action == "inventory" || action == "fleet-preflight" || action == "status" || action == "rollback-check" || action == "admission-status" {
		db, err := sql.Open("sqlite3", uri.String()+"?mode=ro&_busy_timeout=5000")
		if err != nil {
			return err
		}
		defer db.Close()
		if action == "admission-status" {
			return printAdmissionStatus(ctx, db)
		}
		if action == "fleet-preflight" {
			assignments, e := migration.ReadFleetPresets(*fleetPresets)
			if e != nil {
				return e
			}
			templates := map[string]string{}
			if raw := os.Getenv("SANDBOXD_CUBE_TEMPLATES"); raw != "" {
				if e = json.Unmarshal([]byte(raw), &templates); e != nil {
					return e
				}
			}
			report, e := migration.FleetPreflight(ctx, db, *workspaces, migration.FleetOptions{TemplateResources: resources, InspectSource: docker.NewClient().Inspect, Templates: templates, AppPresets: assignments, LibraryRoot: *library, HomeManifests: homes})
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(report)
		}
		if action == "rollback-check" {
			if *id == "" {
				return errors.New("--sandbox is required")
			}
			if err = migration.RollbackCheck(ctx, db, *id); err != nil {
				return err
			}
			var original, appID string
			if err = db.QueryRowContext(ctx, `SELECT m.config_fingerprint,s.app_id FROM runtime_migration m JOIN sandbox s ON s.id=m.sandbox_id WHERE m.sandbox_id=?`, *id).Scan(&original, &appID); err != nil {
				return err
			}
			current, e := store.RuntimeConfigFingerprintDB(ctx, db, appID)
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"sandbox_id": *id, "preliminary_eligible": true, "requires_docker_recreation": current != original, "pending_checks": []string{"offline source identity and config validation", "current workspace/history reverse-copy", "normal Docker wake and application readiness"}})
		}
		if action == "inventory" {
			rows, err := migration.InventoryWithHome(ctx, db, *workspaces, *id, homes)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(rows)
		}
		// Status must remain readable before applying the new journal columns.
		optional := map[string]string{"retained_docker_name": "''", "retained_docker_retired": "0", "home_sha256": "''", "rollback_home_sha256": "''"}
		columns, e := db.QueryContext(ctx, `PRAGMA table_info(runtime_migration)`)
		if e != nil {
			return e
		}
		for columns.Next() {
			var index, required, primary int
			var name, kind string
			var fallback any
			if e = columns.Scan(&index, &name, &kind, &required, &fallback, &primary); e != nil {
				columns.Close()
				return e
			}
			if _, ok := optional[name]; ok {
				optional[name] = name
			}
		}
		e = columns.Err()
		columns.Close()
		if e != nil {
			return e
		}
		query := `SELECT sandbox_id,phase,template_id,runtime_id,archive_sha256,rollback_sha256,` + optional["retained_docker_name"] + `,` + optional["retained_docker_retired"] + `,` + optional["home_sha256"] + `,` + optional["rollback_home_sha256"] + ` FROM runtime_migration WHERE (?='' OR sandbox_id=?)`
		rows, err := db.QueryContext(ctx, query, *id, *id)
		if err != nil {
			return err
		}
		defer rows.Close()
		values := []map[string]string{}
		for rows.Next() {
			var sid, phase, template, runtimeID, source, rollback, retained, home, rollbackHome string
			var retired bool
			if err = rows.Scan(&sid, &phase, &template, &runtimeID, &source, &rollback, &retained, &retired, &home, &rollbackHome); err != nil {
				return err
			}
			values = append(values, map[string]string{"sandbox_id": sid, "phase": phase, "template_id": template, "runtime_id": runtimeID, "source_sha256": source, "rollback_sha256": rollback, "retained_docker_name": retained, "retained_docker_retired": fmt.Sprint(retired), "home_sha256": home, "rollback_home_sha256": rollbackHome})
		}
		if err = rows.Err(); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(values)
	}
	if action != "migrate" && action != "resume" && action != "rollback" && action != "rollback-check" && action != "adopt" && action != "abort" && action != "retire-source" && action != "admission-reconcile" && action != "admission-adopt" {
		return errors.New("action must be inventory, fleet-preflight, status, migrate, resume, rollback-check, rollback, adopt, abort, retire-source, admission-status, admission-adopt or admission-reconcile (flags precede action)")
	}
	if *id == "" && len(batch) == 0 && action != "admission-reconcile" && action != "admission-adopt" {
		return errors.New("--sandbox is required")
	}
	if action == "admission-reconcile" && (!*providerDrained || *admissionKey == "") {
		return errors.New("--admission-key and verified --provider-requests-drained are required")
	}
	if action == "admission-adopt" && *remote == "" {
		return errors.New("--adopt-runtime is required")
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
	admission, err := cube.ParseAdmissionConfig(os.Getenv("SANDBOXD_CUBE_ADMISSION"))
	if err != nil {
		return err
	}
	if err = admission.RequireStorageGuard(); err != nil {
		return err
	}
	if err = client.ConfigureAdmission(ctx, st, admission); err != nil {
		return err
	}
	if action == "admission-reconcile" || action == "admission-adopt" {
		if err = maintenance.CheckDatabaseUsers(*database); err != nil {
			return err
		}
		if action == "admission-reconcile" {
			err = client.ReconcileAdmission(ctx, *admissionKey, *providerDrained)
		} else {
			err = client.AdoptAdmission(ctx, *remote)
		}
		if err != nil {
			return err
		}
		return printAdmissionStatus(ctx, st.DB())
	}
	var broker *migration.MigrationBroker
	if migrationActionNeedsBroker(action) {
		policy, e := migrationBrokerPolicy()
		if e != nil {
			return e
		}
		broker, e = migration.NewMigrationBrokerWithOptions(ctx, policy, migration.MigrationBrokerOptions{HTTPServices: os.Getenv("SANDBOXD_CUBE_APP_HTTP_SERVICES"), MotionStudioAppID: os.Getenv("SANDBOXD_CUBE_MOTION_STUDIO_APP_ID"), Journal: st})
		if e != nil {
			return e
		}
		defer broker.Close()
	}
	backend := &migration.OfflineBackend{TemplateResources: resources, Broker: broker, Store: st, Docker: docker.NewClient(), Cube: client, Secrets: cipher, ProxyURL: os.Getenv("SANDBOXD_CUBE_PROXY_URL"), ArchiveDir: *archives, WorkspaceRoot: *workspaces}
	fence := func() error {
		if e := maintenance.CheckDatabaseUsers(*database); e != nil {
			return e
		}
		if *expectedFleet != "" {
			return migration.VerifyFleetIdentity(ctx, st.DB(), *expectedFleet)
		}
		return nil
	}
	if err = fence(); err != nil {
		return err
	}
	engine := migration.Engine{Store: st, Backend: backend, BeforePhase: fence, StopAfterImport: *stopAfterImport}
	if len(batch) > 0 {
		if action == "migrate" {
			for _, row := range batch {
				if err = beginMigration(ctx, st, backend, *workspaces, row.SandboxID, row.Preset, homes, resources); err != nil {
					return err
				}
			}
		}
		runs := make([]migration.BatchRun, 0, len(batch))
		for _, row := range batch {
			runs = append(runs, migration.BatchRun{SandboxID: row.SandboxID, Engine: engine})
		}
		batchErr := migration.RunBatch(ctx, runs)
		results := make([]map[string]any, 0, len(batch))
		for _, row := range batch {
			journal, e := st.GetRuntimeMigration(ctx, row.SandboxID)
			if e != nil {
				return errors.Join(batchErr, e)
			}
			results = append(results, map[string]any{"sandbox_id": row.SandboxID, "phase": journal.Phase})
		}
		if err = json.NewEncoder(os.Stdout).Encode(map[string]any{"projects": results, "success": batchErr == nil}); err != nil {
			return errors.Join(batchErr, err)
		}
		return batchErr
	}
	if action == "migrate" {
		if err = beginMigration(ctx, st, backend, *workspaces, *id, *targetPreset, homes, resources); err != nil {
			return err
		}
	}

	switch action {
	case "rollback":
		err = engine.Rollback(ctx, *id)
	case "retire-source":
		err = backend.RetireSource(ctx, *id)
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
	current, e := st.Get(ctx, *id)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"sandbox_id": *id, "phase": m.Phase, "pending_recreation": m.Phase == "rolled_back" && m.RollbackRecreate && !current.ContainerID.Valid, "retained_docker_name": m.RetainedDockerName, "retained_docker_retired": m.RetainedDockerRetired})
}
