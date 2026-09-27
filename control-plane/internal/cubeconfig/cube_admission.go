package cubeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func ConfigureAdmission(ctx context.Context, cfg Config, st *store.Store) error {
	if cfg.Client == nil {
		return nil
	}
	admission, err := cube.ParseAdmissionConfig(os.Getenv("SANDBOXD_CUBE_ADMISSION"))
	if err != nil {
		return err
	}
	if err = admission.RequireStorageGuard(); err != nil {
		return err
	}
	for _, id := range cfg.Templates {
		if _, ok := admission.Templates[id]; !ok {
			return errors.New("Cube preset template missing from reviewed admission contract")
		}
	}
	if raw := os.Getenv("SANDBOXD_CUBE_FLEET"); raw != "" {
		var fleet struct {
			Version   int                      `json:"version"`
			MasterURL string                   `json:"master_url"`
			Workers   []cube.FleetWorkerConfig `json:"workers"`
		}
		if len(raw) > 128<<10 {
			return errors.New("Cube fleet configuration too large")
		}
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		var extra any
		if d.Decode(&fleet) != nil || d.Decode(&extra) != io.EOF || fleet.Version != 1 {
			return errors.New("invalid Cube fleet configuration")
		}
		for _, w := range fleet.Workers {
			for _, id := range cfg.Templates {
				if _, ok := w.Admission.Templates[id]; !ok {
					return errors.New("fleet worker lacks reviewed preset template")
				}
			}
			if w.ID == "vps" && (w.Admission.MaxActive != admission.MaxActive || w.Admission.CPUCount != admission.CPUCount || w.Admission.MemoryMB != admission.MemoryMB || w.Admission.StorageGuard == nil || *w.Admission.StorageGuard != *admission.StorageGuard) {
				return errors.New("fleet must preserve the VPS capacity and storage contract")
			}
		}
		if err := cfg.Client.ConfigurePlacement(fleet.MasterURL, "cubebox"); err != nil {
			return err
		}
		return cfg.Client.ConfigureFleet(ctx, st, fleet.Workers)
	}
	if admission.NodeID != "" {
		if err := cfg.Client.ConfigurePlacement(os.Getenv("SANDBOXD_CUBE_MASTER_URL"), "cubebox"); err != nil {
			return err
		}
	}
	return cfg.Client.ConfigureAdmission(ctx, st, admission)
}
