package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/manifest"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/migration"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/preset"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"os"
	"strings"
)

func beginMigration(ctx context.Context, st *store.Store, backend *migration.OfflineBackend, workspaces, id, targetPreset string, homes map[string]runtime.HomeManifest, resources map[string]migration.ResourceLimits) error {
	return planMigration(ctx, st, backend, workspaces, id, targetPreset, homes, resources, false)
}

func planMigration(ctx context.Context, st *store.Store, backend *migration.OfflineBackend, workspaces, id, targetPreset string, homes map[string]runtime.HomeManifest, resources map[string]migration.ResourceLimits, replan bool) error {
	rows, err := migration.InventoryWithHome(ctx, st.DB(), workspaces, id, homes)
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
	if !preset.Valid(targetPreset) || templates[targetPreset] == "" {
		return errors.New("--preset must select a reviewed SANDBOXD_CUBE_TEMPLATES entry")
	}
	definition, _ := preset.Get(targetPreset)
	parsed, e := manifest.Parse([]byte(definition.Manifest))
	if e != nil {
		return e
	}
	port := parsed.WebPort()
	if port <= 0 {
		port = 3000
	}
	source, e := st.Get(ctx, id)
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
	homeJSON := ""
	if home, ok := homes[id]; ok {
		raw, e := runtime.CanonicalHomeManifest(home)
		if e != nil {
			return e
		}
		homeJSON = string(raw)
	}
	inspected, e := backend.Docker.Inspect(ctx, source.ContainerID.String)
	if e != nil {
		return e
	}
	if e = migration.ValidateResourceSelection(templates[targetPreset], source.ContainerID.String, resources, inspected); e != nil {
		return e
	}
	if replan {
		previous, e := st.GetRuntimeMigration(ctx, id)
		if e != nil {
			return e
		}
		if previous.Phase != "aborted" {
			return errors.New("replan requires an aborted journal")
		}
		if e = confirmReplanTargetGone(ctx, backend.Cube, previous.Binding.RuntimeID); e != nil {
			return e
		}
		return st.ReplanAbortedRuntimeMigrationWithHome(ctx, id, targetPreset, templates[targetPreset], domain, homeJSON)
	}
	if err = st.BeginRuntimeMigrationWithHome(ctx, id, targetPreset, templates[targetPreset], domain, homeJSON); err != nil {
		return err
	}
	return nil
}

func confirmReplanTargetGone(ctx context.Context, client *cube.Client, runtimeID string) error {
	if runtimeID == "" {
		return nil
	}
	_, err := client.Get(ctx, runtimeID)
	var upstream *cube.APIError
	if !errors.As(err, &upstream) || upstream.StatusCode != 404 {
		return errors.New("former Cube target must be confirmed deleted before replan")
	}
	return nil
}
