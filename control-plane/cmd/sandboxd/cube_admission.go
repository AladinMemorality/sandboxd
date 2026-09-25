package main

import (
	"context"
	"errors"
	"os"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func configureCubeAdmission(ctx context.Context, cfg cubeConfig, st *store.Store) error {
	if cfg.client == nil {
		return nil
	}
	admission, err := cube.ParseAdmissionConfig(os.Getenv("SANDBOXD_CUBE_ADMISSION"))
	if err != nil {
		return err
	}
	if err = admission.RequireStorageGuard(); err != nil {
		return err
	}
	for _, id := range cfg.templates {
		if _, ok := admission.Templates[id]; !ok {
			return errors.New("Cube preset template missing from reviewed admission contract")
		}
	}
	return cfg.client.ConfigureAdmission(ctx, st, admission)
}
