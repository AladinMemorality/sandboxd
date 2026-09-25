package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oklog/ulid/v2"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/manifest"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

// FleetOptions contains only reviewed operator assignments. Empty legacy
// presets are never guessed from file names or silently mapped to React.
type FleetOptions struct {
	TemplateResources map[string]ResourceLimits
	InspectSource     func(context.Context, string) (*docker.ContainerJSON, error)
	Templates         map[string]string
	AppPresets        map[string]string
	LibraryRoot       string
	HomeManifests     map[string]runtime.HomeManifest
}
type FleetProject struct {
	SourceResources *ResourceLimits `json:"source_resources,omitempty"`
	TargetResources *ResourceLimits `json:"target_resources,omitempty"`
	AppID           string          `json:"app_id"`
	SandboxID       string          `json:"sandbox_id,omitempty"`
	SourcePreset    string          `json:"source_preset"`
	TargetPreset    string          `json:"target_preset"`
	TemplateID      string          `json:"template_id,omitempty"`
	State           string          `json:"state"`
	Reasons         []string        `json:"reasons,omitempty"`
	Inventory       *InventoryRow   `json:"inventory,omitempty"`
}
type FleetSnapshot struct {
	ID      string   `json:"snapshot_id"`
	AppID   string   `json:"app_id"`
	Format  string   `json:"format"`
	Preset  string   `json:"preset"`
	State   string   `json:"state"`
	Reasons []string `json:"reasons,omitempty"`
}
type FleetReport struct {
	Version                    int             `json:"version"`
	Scope                      string          `json:"scope"`
	IdentitySHA256             string          `json:"identity_sha256"`
	SandboxCount               int             `json:"sandbox_count"`
	SnapshotCount              int             `json:"snapshot_count"`
	RawSnapshotCount           int             `json:"raw_snapshot_count"`
	MissingSnapshotPresetCount int             `json:"missing_snapshot_preset_count"`
	Projects                   []FleetProject  `json:"projects"`
	UnassignedSandboxes        []InventoryRow  `json:"unassigned_sandboxes"`
	Snapshots                  []FleetSnapshot `json:"snapshots"`
	BlockedProjects            int             `json:"blocked_projects"`
	BlockedSnapshots           int             `json:"blocked_snapshots"`
	Reasons                    []string        `json:"reasons,omitempty"`
	PendingChecks              []string        `json:"pending_checks"`
}

func targetPreset(source, app string, options FleetOptions) (string, string, []string) {
	target := source
	if override, ok := options.AppPresets[app]; ok {
		target = override
	}
	reasons := []string{}
	if !preset.Valid(target) {
		reasons = append(reasons, "a valid reviewed runtime preset assignment is required")
	}
	template := options.Templates[target]
	if template == "" {
		reasons = append(reasons, "reviewed Cube template is not configured for the target preset")
	}
	return target, template, reasons
}

// FleetPreflight is read-only and accounts for every app, sandbox and published
// snapshot. Passing metadata checks is explicitly not a production readiness
// certificate: definitive archive/config/guest verification is still required.
func FleetPreflight(ctx context.Context, db *sql.DB, workspaces string, options FleetOptions) (*FleetReport, error) {
	inventory, err := InventoryWithHome(ctx, db, workspaces, "", options.HomeManifests)
	if err != nil {
		return nil, err
	}
	bySandbox := map[string]*InventoryRow{}
	for i := range inventory {
		bySandbox[inventory[i].SandboxID] = &inventory[i]
	}
	out := &FleetReport{Version: 1, Scope: "read-only preliminary fleet plan; no sandboxes stopped or migrated", Projects: []FleetProject{}, UnassignedSandboxes: []InventoryRow{}, Snapshots: []FleetSnapshot{}, PendingChecks: []string{"freeze admission and task submissions, drain tasks, stop the control plane, then regenerate this plan offline", "reject new, missing, or reassigned IDs by comparing identity_sha256 before each migration", "offline task drain and exclusive maintenance fence", "Docker source identity, owner mount and stopped-state verification", "complete owner manifest and private archive checksums", "trusted template capability and authenticated guest readiness", "source archive filesystem and guest disk capacity must cover compressed spool plus expanded staging plus retained original app/home; transport limits do not guarantee free space", "stable UI/preview routes and post-cutover application checks"}}
	out.SandboxCount = len(inventory)
	out.IdentitySHA256, err = FleetIdentityDigest(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT a.id,COALESCE(a.runtime_preset,''),COALESCE((SELECT s.id FROM sandbox s WHERE s.app_id=a.id ORDER BY s.created_at DESC,s.id DESC LIMIT 1),''),COALESCE((SELECT s.container_id FROM sandbox s WHERE s.app_id=a.id ORDER BY s.created_at DESC,s.id DESC LIMIT 1),'') FROM app a ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	selected := map[string]bool{}
	for rows.Next() {
		var project FleetProject
		var containerID string
		if err = rows.Scan(&project.AppID, &project.SourcePreset, &project.SandboxID, &containerID); err != nil {
			rows.Close()
			return nil, err
		}
		known[project.AppID] = true
		selected[project.SandboxID] = true
		project.TargetPreset, project.TemplateID, project.Reasons = targetPreset(project.SourcePreset, project.AppID, options)
		project.Inventory = bySandbox[project.SandboxID]
		if project.Inventory == nil || project.Inventory.Provider != "cube" {
			expected, ok := options.TemplateResources[project.TemplateID]
			if !ok || !expected.valid() {
				project.Reasons = append(project.Reasons, "reviewed target CPU/RAM resource contract is required")
			} else {
				project.TargetResources = &expected
			}
			if options.InspectSource == nil {
				project.Reasons = append(project.Reasons, "actual Docker source CPU/RAM inspection is required")
			} else if project.SandboxID != "" {
				inspected, inspectErr := options.InspectSource(ctx, containerID)
				if inspectErr != nil || inspected == nil || !sourceContainerIdentityMatches(containerID, inspected.ID) {
					project.Reasons = append(project.Reasons, "source resource inspection failed or identity differs")
				} else {
					source, e := sourceResources(inspected)
					if e != nil {
						project.Reasons = append(project.Reasons, "source CPU/RAM is unlimited, missing or unsupported")
					} else {
						project.SourceResources = &source
						if project.TargetResources != nil && !expected.covers(source) {
							project.Reasons = append(project.Reasons, "target template would reduce source CPU or memory limits")
						}
					}
				}
			}
		}
		if project.Inventory == nil {
			project.Reasons = append(project.Reasons, "project has no current sandbox; its creation preset still requires review")
		} else {
			if project.Inventory.Provider == "cube" {
				project.State = "already_cube"
				project.Reasons = nil
			} else {
				project.Reasons = append(project.Reasons, project.Inventory.Reasons...)
			}
		}
		if project.State == "" {
			project.State = "preflight_passed"
			if len(project.Reasons) > 0 {
				project.State = "blocked"
				out.BlockedProjects++
			}
		}
		out.Projects = append(out.Projects, project)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for id := range options.HomeManifests {
		if bySandbox[id] == nil {
			out.Reasons = append(out.Reasons, "home manifest references an unknown sandbox: "+id)
		}
	}
	for id := range options.AppPresets {
		if !known[id] {
			out.Reasons = append(out.Reasons, "preset assignment references an unknown app: "+id)
		}
	}
	for _, row := range inventory {
		if !selected[row.SandboxID] {
			out.UnassignedSandboxes = append(out.UnassignedSandboxes, row)
		}
	}
	// Check port compatibility outside the app cursor for single-connection DBs.
	for i := range out.Projects {
		project := &out.Projects[i]
		if project.Inventory == nil || project.State == "already_cube" || !preset.Valid(project.TargetPreset) {
			continue
		}
		definition, _ := preset.Get(project.TargetPreset)
		parsed, e := manifest.Parse([]byte(definition.Manifest))
		if e != nil {
			return nil, e
		}
		port := parsed.WebPort()
		if port <= 0 {
			port = 3000
		}
		var ports string
		// Ports are normalized in sandbox_port; no stored JSON is assumed.
		portRows, e := db.QueryContext(ctx, `SELECT port FROM sandbox_port WHERE sandbox_id=? ORDER BY port`, project.SandboxID)
		if e != nil {
			return nil, e
		}
		actual := []int{}
		for portRows.Next() {
			var p int
			if e = portRows.Scan(&p); e != nil {
				portRows.Close()
				return nil, e
			}
			actual = append(actual, p)
		}
		e = portRows.Err()
		portRows.Close()
		if e != nil {
			return nil, e
		}
		encoded, _ := json.Marshal(actual)
		ports = string(encoded)
		if len(actual) != 1 || actual[0] != port {
			if project.State != "blocked" {
				out.BlockedProjects++
			}
			project.State = "blocked"
			project.Reasons = append(project.Reasons, "source preview ports "+ports+" do not match the reviewed target preset")
		}
	}
	snapshots, err := db.QueryContext(ctx, `SELECT s.id,COALESCE(s.source_app_id,''),s.format,s.status,s.image_path,COALESCE(a.runtime_preset,''),s.base_image,COALESCE(a.owner_token,''),s.owner_token FROM snapshot s LEFT JOIN app a ON a.id=s.source_app_id ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	for snapshots.Next() {
		var row FleetSnapshot
		var status, path, source, baseImage, appOwner, snapshotOwner string
		if err = snapshots.Scan(&row.ID, &row.AppID, &row.Format, &status, &path, &source, &baseImage, &appOwner, &snapshotOwner); err != nil {
			snapshots.Close()
			return nil, err
		}
		if row.Format == "cube-source-v1" {
			source = strings.TrimPrefix(baseImage, "cube-preset:")
			if !strings.HasPrefix(baseImage, "cube-preset:") {
				source = ""
			}
			row.Preset, _, row.Reasons = targetPreset(source, "", FleetOptions{Templates: options.Templates})
		} else {
			row.Preset, _, row.Reasons = targetPreset(source, row.AppID, options)
		}
		if row.Preset == "" {
			out.MissingSnapshotPresetCount++
		}
		if parsed, e := ulid.ParseStrict(row.ID); e != nil || parsed.String() != row.ID {
			row.Reasons = append(row.Reasons, "snapshot identity is not a canonical ULID")
		}
		if status != "ready" {
			row.Reasons = append(row.Reasons, "snapshot is not ready")
		}
		if row.Format == "raw" {
			out.RawSnapshotCount++
			if appOwner == "" || appOwner != snapshotOwner {
				row.Reasons = append(row.Reasons, "raw snapshot source owner is unavailable or mismatched")
			}
			if options.LibraryRoot == "" || !filepath.IsAbs(options.LibraryRoot) {
				row.Reasons = append(row.Reasons, "absolute snapshot library root is required")
			} else if path != filepath.Join(options.LibraryRoot, row.ID) {
				row.Reasons = append(row.Reasons, "raw snapshot path does not match its library identity")
			} else {
				for directory := filepath.Join(path, "workspace", "app"); ; directory = filepath.Dir(directory) {
					info, e := os.Lstat(directory)
					if e != nil || !info.IsDir() {
						row.Reasons = append(row.Reasons, "raw snapshot ancestry is missing, linked, or not a directory")
						break
					}
					if directory == string(os.PathSeparator) {
						break
					}
				}
			}
			row.State = "conversion_required"
		} else if row.Format == "cube-source-v1" {
			if options.LibraryRoot == "" || !filepath.IsAbs(options.LibraryRoot) || path != filepath.Join(options.LibraryRoot, row.ID+".cube.zip") {
				row.Reasons = append(row.Reasons, "Cube snapshot path does not match its library identity")
			} else {
				info, e := os.Lstat(path)
				if e != nil || !info.Mode().IsRegular() {
					row.Reasons = append(row.Reasons, "Cube snapshot archive is missing or unsafe")
				}
				for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
					info, e := os.Lstat(directory)
					if e != nil || !info.IsDir() {
						row.Reasons = append(row.Reasons, "Cube snapshot ancestry is missing, linked, or not a directory")
						break
					}
					if directory == string(os.PathSeparator) {
						break
					}
				}
			}
		} else {
			row.Reasons = append(row.Reasons, "snapshot format requires a compatibility adapter")
		}
		if len(row.Reasons) > 0 {
			row.State = "blocked"
			out.BlockedSnapshots++
		} else if row.State == "" {
			row.State = "preflight_passed"
		}
		out.SnapshotCount++
		out.Snapshots = append(out.Snapshots, row)
	}
	err = snapshots.Err()
	snapshots.Close()
	if err != nil {
		return nil, err
	}
	sort.Strings(out.Reasons)
	return out, nil
}

func ReadFleetPresets(path string) (map[string]string, error) {
	if path == "" {
		return map[string]string{}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("fleet preset assignments must be a bounded regular JSON file")
	}
	values := map[string]string{}
	decoder := json.NewDecoder(file)
	if err = decoder.Decode(&values); err != nil {
		return nil, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, errors.New("unexpected trailing fleet preset data")
	}
	if values == nil {
		return nil, errors.New("fleet presets must be a JSON object")
	}
	return values, nil
}

// Identity digest deliberately excludes provider/preset transitions so a
// partially completed fleet can resume. Ownership and every ID remain fixed.
func FleetIdentityDigest(ctx context.Context, db *sql.DB) (string, error) {
	h := sha256.New()
	for _, query := range []string{
		`SELECT id,owner_token,COALESCE(external_user_id,'') FROM app ORDER BY id`,
		`SELECT id,COALESCE(app_id,''),COALESCE(external_user_id,'') FROM sandbox ORDER BY id`,
		`SELECT id,COALESCE(source_app_id,''),owner_token FROM snapshot ORDER BY id`,
	} {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var a, b, c string
			if err = rows.Scan(&a, &b, &c); err != nil {
				rows.Close()
				return "", err
			}
			raw, _ := json.Marshal([]string{query, a, b, c})
			h.Write(raw)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
func VerifyFleetIdentity(ctx context.Context, db *sql.DB, expected string) error {
	if len(expected) != 64 {
		return errors.New("fleet plan is missing its identity digest")
	}
	actual, err := FleetIdentityDigest(ctx, db)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("fleet membership or ownership changed; freeze admission and regenerate the offline plan")
	}
	return nil
}
