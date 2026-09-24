package migration

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func retainedDockerName(id string) string { return "sandboxd-migration-source-" + id }

// ValidateRollback performs read-only checks before stopping the current guest.
// A changed config is supported only through the normal Docker recreation path.
func (b *OfflineBackend) ValidateRollback(ctx context.Context, m *store.RuntimeMigration) error {
	fingerprint, err := b.Store.RuntimeConfigFingerprint(ctx, m.Source.AppID.String)
	if err != nil {
		return err
	}
	if fingerprint == m.ConfigFingerprint {
		return nil
	}
	if err = b.sourceStopped(ctx, m); err != nil {
		return err
	}
	rows, err := b.Store.ListAppConfig(ctx, m.Source.AppID.String)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if (row.AccessPolicy == "runtime_access" || row.AccessPolicy == "both") && !runtime.ValidAppConfigKey(row.Key) {
			return errors.New("runtime configuration contains an unsupported environment key")
		}
	}
	entries, err := appenv.For(ctx, b.Store, b.Secrets, m.Source.AppID.String)
	if err != nil {
		return err
	}
	env := map[string]string{}
	for _, entry := range entries {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			env[k] = v
		}
	}
	if err = runtime.ValidateAppConfig(runtime.AppConfigRequest{Revision: "rollback-preflight", Env: env}); err != nil {
		return errors.New("runtime configuration cannot be delivered safely")
	}
	_, err = b.rollbackContainer(ctx, m)
	return err
}

func (b *OfflineBackend) rollbackContainer(ctx context.Context, m *store.RuntimeMigration) (*docker.ContainerJSON, error) {
	original, err := b.Docker.Inspect(ctx, m.Source.ContainerID.String)
	if err != nil {
		return nil, err
	}
	if original.State.Running {
		return nil, errors.New("retained source is running")
	}
	if err = b.verifyPinnedSourceIdentity(m, original); err != nil {
		return nil, err
	}
	if err = b.validateSourceContainer(m, original); err != nil {
		return nil, err
	}
	canonical, retained := "s-"+m.SandboxID, retainedDockerName(m.SandboxID)
	name := strings.TrimPrefix(original.Name, "/")
	if name != canonical && name != retained {
		return nil, errors.New("Docker source name is incompatible with guarded recreation")
	}
	for _, candidate := range []string{canonical, retained} {
		other, e := b.Docker.Inspect(ctx, candidate)
		if errors.Is(e, docker.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if other.ID != original.ID {
			return nil, fmt.Errorf("Docker recovery name %s is already occupied", candidate)
		}
	}
	return original, nil
}

// PrepareRollback never deletes the original container or constructs an ad-hoc
// security spec. Removing its canonical name makes the established wake path
// create a new container from current sandboxspec/appenv settings. The row stays
// stopped and explicitly pending recreation until that path verifies readiness.
func (b *OfflineBackend) PrepareRollback(ctx context.Context, m *store.RuntimeMigration) error {
	fingerprint, err := b.Store.RuntimeConfigFingerprint(ctx, m.Source.AppID.String)
	if err != nil {
		return err
	}
	if fingerprint != m.RollbackConfigFingerprint {
		return errors.New("rollback configuration changed after preparation")
	}
	if !m.RollbackRecreate {
		return nil
	}
	original, err := b.rollbackContainer(ctx, m)
	if err != nil {
		return err
	}
	name := retainedDockerName(m.SandboxID)
	if strings.TrimPrefix(original.Name, "/") != name {
		if err = b.Docker.Rename(ctx, original.ID, name); err != nil {
			return err
		}
	}
	// An acknowledgement is not evidence: inspect both names again. Retrying a
	// lost rename response finds the retained original and never creates a copy.
	original, err = b.rollbackContainer(ctx, m)
	if err != nil {
		return err
	}
	if strings.TrimPrefix(original.Name, "/") != name {
		return errors.New("original Docker rename was not applied")
	}
	if _, err = b.Docker.Inspect(ctx, "s-"+m.SandboxID); !errors.Is(err, docker.ErrNotFound) {
		if err != nil {
			return err
		}
		return errors.New("stale canonical Docker container still exists")
	}
	return b.Store.RecordRetainedDocker(ctx, m.SandboxID, name)
}

// RetireSource removes only the immutable original container identity, after
// ordinary wake has acknowledged a different ready Docker runtime. The source
// workspace and private recovery archives are retained. This explicit operation
// prevents a renamed original becoming an undiscoverable orphan on later purge.
func (b *OfflineBackend) RetireSource(ctx context.Context, id string) error {
	m, err := b.Store.GetRuntimeMigration(ctx, id)
	if err != nil {
		return err
	}
	if m.Phase != "rolled_back" || !m.RollbackRecreate || m.RetainedDockerName != retainedDockerName(id) {
		return errors.New("source retirement requires a completed recreation rollback")
	}
	if m.RetainedDockerRetired {
		return nil
	}
	current, err := b.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.RuntimeProvider != "docker" || current.Status != "running" || !current.ContainerID.Valid || current.ContainerID.String == m.Source.ContainerID.String {
		return errors.New("source retirement requires a newly running Docker identity")
	}
	active, err := b.Store.SandboxHasRunningTask(ctx, id)
	if err != nil {
		return err
	}
	if active {
		return errors.New("active task prevents source retirement")
	}
	replacement, err := b.Docker.Inspect(ctx, "s-"+id)
	if err != nil {
		return err
	}
	if replacement.ID != current.ContainerID.String || !replacement.State.Running {
		return errors.New("replacement container identity or running state mismatch")
	}
	if err = b.validateSourceContainer(m, replacement); err != nil {
		return err
	}
	entries, err := appenv.For(ctx, b.Store, b.Secrets, current.AppID.String)
	if err != nil {
		return err
	}
	actual := map[string]string{}
	for _, entry := range replacement.Config.Env {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			actual[k] = v
		}
	}
	for _, entry := range entries {
		k, v, ok := strings.Cut(entry, "=")
		if !ok || actual[k] != v {
			return errors.New("replacement has not applied current runtime configuration")
		}
	}
	client := runtime.NewClient(filepath.Join(b.WorkspaceRoot, id, ".runtimed", "sock"))
	probe := migrationReadiness{Window: 2 * time.Second}
	if err = wait(ctx, 30*time.Second, func() bool {
		status, statusErr := client.Status(ctx)
		if statusErr != nil {
			return probe.Observe(nil, time.Now())
		}
		return probe.Observe(status, time.Now())
	}); err != nil {
		return fmt.Errorf("replacement application is not stably ready: %w", err)
	}
	original, err := b.Docker.Inspect(ctx, m.Source.ContainerID.String)
	if errors.Is(err, docker.ErrNotFound) {
		return b.Store.RetireMigrationDocker(ctx, id)
	}
	if err != nil {
		return err
	}
	if original.State.Running || strings.TrimPrefix(original.Name, "/") != m.RetainedDockerName {
		return errors.New("retained source identity or stopped state changed")
	}
	if err = b.verifyPinnedSourceIdentity(m, original); err != nil {
		return err
	}
	if err = b.validateSourceContainer(m, original); err != nil {
		return err
	}
	if err = b.Docker.Remove(ctx, original.ID); err != nil && !errors.Is(err, docker.ErrNotFound) {
		return err
	}
	return b.Store.RetireMigrationDocker(ctx, id)
}
