package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/appenv"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/docker"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type OfflineBackend struct {
	TemplateResources map[string]ResourceLimits
	Broker            *MigrationBroker
	Store             *store.Store
	Docker            *docker.Client
	Cube              *cube.Client
	Secrets           *secrets.Cipher
	ProxyURL          string
	ArchiveDir        string
	// WorkspaceRoot is the configured host bind-mount root, not a request path.
	WorkspaceRoot string
}

type credentials struct {
	SupervisorToken    string `json:"supervisor_token"`
	TrafficAccessToken string `json:"traffic_access_token"`
}

func validateHistoryScope(data []byte, expected []string) error {
	actual, err := runtime.PrivateTaskHistoryIDs(data)
	if err != nil {
		return err
	}
	want := append([]string(nil), expected...)
	sort.Strings(want)
	sort.Strings(actual)
	if len(actual) != len(want) {
		return errors.New("guest task archive does not match canonical owner task IDs")
	}
	for i := range want {
		if actual[i] != want[i] {
			return errors.New("guest task archive contains an unrequested task")
		}
	}
	return nil
}

func (b *OfflineBackend) sourceRoot(m *store.RuntimeMigration) (string, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(m.SandboxID) {
		return "", errors.New("invalid sandbox identity")
	}
	root := filepath.Join(b.WorkspaceRoot, m.SandboxID)
	// Historical loopback installations may use a different path. Never guess
	// or read an arbitrary DB path: those require a reviewed storage adapter.
	if m.Source.WorkspaceMnt != "" && filepath.Clean(m.Source.WorkspaceMnt) != root {
		return "", errors.New("workspace storage layout is not eligible for directory migration")
	}
	for _, directory := range []string{root, filepath.Join(root, "workspace"), filepath.Join(root, "workspace", "app")} {
		info, err := os.Lstat(directory)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("workspace ancestry contains a link or non-directory")
		}
	}
	return filepath.Join(root, "workspace", "app"), nil
}

func (b *OfflineBackend) sourceStopped(ctx context.Context, m *store.RuntimeMigration) error {
	if !m.Source.ContainerID.Valid {
		return errors.New("source container identity is missing")
	}
	inspected, err := b.Docker.Inspect(ctx, m.Source.ContainerID.String)
	if err != nil {
		return err
	}
	if inspected.State.Running {
		return errors.New("source Docker container is still running")
	}
	if err = b.verifyPinnedSourceIdentity(m, inspected); err != nil {
		return err
	}
	return b.validateSourceContainer(m, inspected)
}

func (b *OfflineBackend) validateSourceContainer(m *store.RuntimeMigration, inspected *docker.ContainerJSON) error {
	if inspected.Config.Labels["sandboxd.managed"] != "true" {
		return errors.New("source container is not sandboxd-managed")
	}
	expected := filepath.Join(b.WorkspaceRoot, m.SandboxID)
	for _, mount := range inspected.Mounts {
		if mount.Destination == "/home/sandbox" && filepath.Clean(mount.Source) == expected {
			return nil
		}
	}
	return errors.New("source container home mount does not match the owner workspace")
}

func (b *OfflineBackend) StopSource(ctx context.Context, m *store.RuntimeMigration) error {
	if m.HomeManifestJSON != "" {
		plan, e := migrationHome(m)
		if e != nil {
			return e
		}
		report, e := runtime.ValidateHomeManifest(ctx, filepath.Join(b.WorkspaceRoot, m.SandboxID), plan)
		if e != nil {
			return e
		}
		if !report.Eligible {
			return errors.New("source owner-home manifest no longer passes preflight")
		}
	}
	if _, err := b.sourceRoot(m); err != nil {
		return err
	}
	active, err := b.Store.SandboxHasRunningTask(ctx, m.SandboxID)
	if err != nil {
		return err
	}
	if active {
		return errors.New("active task prevents migration")
	}
	if !m.Source.ContainerID.Valid {
		return errors.New("source container identity is missing")
	}
	inspected, err := b.Docker.Inspect(ctx, m.Source.ContainerID.String)
	if err != nil {
		return err
	}
	if err = b.validateSourceContainer(m, inspected); err != nil {
		return err
	}
	if err = b.preserveSourceResources(m, inspected); err != nil {
		return err
	}
	if err = b.Docker.Stop(ctx, inspected.ID, 30); err != nil {
		return err
	}
	return b.sourceStopped(ctx, m)
}

func (b *OfflineBackend) artifact(m *store.RuntimeMigration, name string) (string, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(m.SandboxID) {
		return "", errors.New("invalid sandbox identity")
	}
	root := filepath.Join(b.ArchiveDir, m.SandboxID)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return "", errors.New("migration archive directory must be a private real directory with mode 0700")
	}
	return filepath.Join(root, name+".zip"), nil
}

func (b *OfflineBackend) saveArchive(m *store.RuntimeMigration, name string, data []byte) (string, error) {
	digest, err := runtime.PrivateWorkspaceDigest(data)
	if err != nil {
		return "", err
	}
	path, err := b.artifact(m, name)
	if err != nil {
		return "", err
	}
	if old, e := os.ReadFile(path); e == nil {
		previous, e := runtime.PrivateWorkspaceDigest(old)
		if e != nil || previous != digest {
			return "", errors.New("retained archive differs; refusing to overwrite recovery data")
		}
		return digest, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".archive-")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	// Linking is an atomic no-replace commit; an unexpected existing file can
	// never be overwritten, including after an interrupted previous attempt.
	if err = os.Link(temporary, path); err != nil {
		return "", err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return "", err
	}
	return digest, nil
}

func (b *OfflineBackend) readArchive(m *store.RuntimeMigration, name, digest string) ([]byte, error) {
	path, err := b.artifact(m, name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	got, err := runtime.PrivateWorkspaceDigest(data)
	if err != nil {
		return nil, err
	}
	if got != digest {
		return nil, errors.New("recovery archive checksum mismatch")
	}
	return data, nil
}

func (b *OfflineBackend) ArchiveSource(ctx context.Context, m *store.RuntimeMigration) (string, error) {
	if err := b.sourceStopped(ctx, m); err != nil {
		return "", err
	}
	root, err := b.sourceRoot(m)
	if err != nil {
		return "", err
	}
	if err = b.archiveSourceHome(ctx, m); err != nil {
		return "", err
	}
	digest, err := b.saveWorkspaceArchive(ctx, m, "source", func(w io.Writer) error {
		return runtime.ExportPrivateWorkspaceFile(ctx, root, filepath.Join(b.WorkspaceRoot, m.SandboxID), w)
	})
	if err != nil {
		return "", err
	}
	ids, err := b.Store.MigrationTaskIDs(ctx, m.SandboxID)
	if err != nil {
		return "", err
	}
	history, err := runtime.ExportPrivateTaskHistory(ctx, filepath.Join(b.WorkspaceRoot, m.SandboxID, ".runtimed", "tasks"), ids)
	if err != nil {
		return "", err
	}
	historyDigest, err := b.saveArchive(m, "source-history", history)
	if err != nil {
		return "", err
	}
	if err = b.Store.RecordMigrationHistory(ctx, m.SandboxID, m.Phase, historyDigest, false); err != nil {
		return "", err
	}
	return digest, nil
}

func (b *OfflineBackend) StageTarget(ctx context.Context, m *store.RuntimeMigration) error {
	if _, err := b.readResourceContract(m); err != nil {
		return err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	c := credentials{SupervisorToken: hex.EncodeToString(token)}
	raw, _ := json.Marshal(c)
	ciphertext, nonce, err := b.Secrets.Seal(raw)
	if err != nil {
		return err
	}
	// The token and uncertainty marker precede network creation. A lost response
	// cannot accidentally create a second VM on restart.
	if err = b.Store.PrepareMigrationTargetCredential(ctx, m.SandboxID, ciphertext, nonce); err != nil {
		return err
	}
	network, err := cube.OperatorEgressPolicy("")
	if err != nil {
		return err
	}
	remote, err := b.Cube.Create(ctx, cube.CreateRequest{TemplateID: m.Binding.TemplateID, TimeoutSeconds: 3600, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": c.SupervisorToken}, Metadata: map[string]string{"sandboxd_id": m.SandboxID, "sandboxd_app_id": m.Source.AppID.String, "sandboxd_migration": "offline-v1"}, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: network})
	if err != nil {
		return err
	}
	if remote.TrafficAccessToken == "" {
		return errors.New("Cube creation returned no private traffic credential; recover tagged VM before continuing")
	}
	c.TrafficAccessToken = remote.TrafficAccessToken
	raw, _ = json.Marshal(c)
	ciphertext, nonce, err = b.Secrets.Seal(raw)
	if err != nil {
		return err
	}
	return b.Store.SaveMigrationTarget(ctx, m.SandboxID, &store.RuntimeBinding{RuntimeID: remote.SandboxID, TokenCiphertext: ciphertext, TokenNonce: nonce})
}

func (b *OfflineBackend) remote(m *store.RuntimeMigration) (*runtime.Client, error) {
	plain, err := b.Secrets.Open(m.Binding.TokenCiphertext, m.Binding.TokenNonce)
	if err != nil {
		return nil, err
	}
	var c credentials
	if err = json.Unmarshal(plain, &c); err != nil {
		return nil, err
	}
	return runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: b.ProxyURL, Token: c.SupervisorToken, TrafficAccessToken: c.TrafficAccessToken, Host: fmt.Sprintf("3031-%s.%s", m.Binding.RuntimeID, m.Binding.Domain)})
}

func (b *OfflineBackend) connect(ctx context.Context, m *store.RuntimeMigration) (*runtime.Client, error) {
	if _, err := b.Cube.Connect(ctx, m.Binding.RuntimeID, cube.ConnectRequest{TimeoutSeconds: 3600}); err != nil {
		return nil, err
	}
	client, err := b.remote(m)
	if err != nil {
		return nil, err
	}
	err = wait(ctx, 30*time.Second, func() bool { status, e := client.Status(ctx); return e == nil && status.ActiveTask == nil })
	if err == nil && b.Broker != nil {
		attach, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		err = b.Broker.Attach(attach, m, client)
	}
	return client, err
}

func wait(ctx context.Context, timeout time.Duration, condition func() bool) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("authenticated guest readiness timed out")
		case <-ticker.C:
		}
	}
}

func (b *OfflineBackend) ImportTarget(ctx context.Context, m *store.RuntimeMigration) error {
	if err := b.verifyTargetResources(ctx, m); err != nil {
		return err
	}
	data, size, err := b.readWorkspaceArchive(ctx, m, "source", m.ArchiveSHA256)
	if err != nil {
		return err
	}
	defer data.Close()
	client, err := b.connect(ctx, m)
	if err != nil {
		return err
	}
	if err = client.QuiesceWorkspace(ctx); err != nil {
		return err
	}
	before, err := client.Status(ctx)
	if err != nil {
		return err
	}
	if err = client.ImportPrivateWorkspaceFile(ctx, data, size); err != nil {
		return err
	}
	err = wait(ctx, 30*time.Second, func() bool {
		status, e := client.Status(ctx)
		return e == nil && !status.Runtimed.BootedAt.Equal(before.Runtimed.BootedAt)
	})
	if err != nil {
		return err
	}
	history, err := b.readArchive(m, "source-history", m.HistorySHA256)
	if err != nil {
		return err
	}
	if err = client.ImportPrivateTaskHistory(ctx, history); err != nil {
		return err
	}
	return b.importTargetHome(ctx, m, client)
}

func (b *OfflineBackend) VerifyTarget(ctx context.Context, m *store.RuntimeMigration) error {
	if err := b.verifyTargetResources(ctx, m); err != nil {
		return err
	}
	client, err := b.connect(ctx, m)
	if err != nil {
		return err
	}
	if err = client.QuiesceWorkspace(ctx); err != nil {
		return fmt.Errorf("quiesce target before verification: %w", err)
	}
	if err = b.verifyTargetHome(ctx, m, client); err != nil {
		return err
	}
	digest, err := b.saveWorkspaceArchive(ctx, m, "verified", func(w io.Writer) error { return client.ExportPrivateWorkspaceFile(ctx, w) })
	if err != nil {
		return fmt.Errorf("export target workspace for verification: %w", err)
	}
	if digest != m.ArchiveSHA256 {
		return errors.New("target workspace manifest differs from source")
	}
	ids, err := b.Store.MigrationTaskIDs(ctx, m.SandboxID)
	if err != nil {
		return err
	}
	history, err := client.ExportPrivateTaskHistory(ctx, ids)
	if err != nil {
		return fmt.Errorf("export target task history for verification: %w", err)
	}
	if err = validateHistoryScope(history, ids); err != nil {
		return err
	}
	historyDigest, err := runtime.PrivateWorkspaceDigest(history)
	if err != nil {
		return err
	}
	if historyDigest != m.HistorySHA256 {
		return errors.New("target task history differs from source")
	}
	entries, err := appenv.For(ctx, b.Store, b.Secrets, m.Source.AppID.String)
	if err != nil {
		return err
	}
	env := map[string]string{}
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	motionScoped := b.Broker != nil && b.Broker.motionAppID != "" && m.Source.AppID.Valid && b.Broker.motionAppID == m.Source.AppID.String
	if err := runtime.ValidateMotionStudioScope(env, motionScoped); err != nil {
		return err
	}
	if motionScoped {
		status, e := client.Status(ctx)
		if e != nil {
			return e
		}
		env, e = b.Broker.motionEnvironment(ctx, m, status, env)
		if e != nil {
			return e
		}
	}
	revision := m.SandboxID + ":0"
	if len(env) > 0 {
		revision = m.SandboxID + ":1"
	}
	if motionScoped {
		revision += ":" + runtime.MotionWorkerCapability
	}
	if err = client.ApplyAppConfig(ctx, runtime.AppConfigRequest{Env: env, Revision: revision}); err != nil {
		return fmt.Errorf("apply target app config: %w", err)
	}
	return wait(ctx, 30*time.Second, func() bool { status, e := client.Status(ctx); return e == nil && status.AppConfigRevision == revision })
}

func (b *OfflineBackend) ReadyTarget(ctx context.Context, m *store.RuntimeMigration) error {
	if err := b.verifyTargetResources(ctx, m); err != nil {
		return err
	}
	client, err := b.connect(ctx, m)
	if err != nil {
		return err
	}
	if err = client.ResumeWorkspace(ctx); err != nil {
		return err
	}
	probe := migrationReadiness{Window: 2 * time.Second}
	return wait(ctx, 90*time.Second, func() bool {
		status, e := client.Status(ctx)
		if e != nil {
			probe.Observe(nil, time.Now())
			return false
		}
		return probe.Observe(status, time.Now())
	})
}

func (b *OfflineBackend) ArchiveTarget(ctx context.Context, m *store.RuntimeMigration) (string, error) {
	client, err := b.connect(ctx, m)
	if err != nil {
		return "", err
	}
	if err = client.QuiesceWorkspace(ctx); err != nil {
		return "", err
	}
	if err = b.archiveTargetHome(ctx, m, client); err != nil {
		return "", err
	}
	export := func(w io.Writer) error { return client.ExportPrivateWorkspaceFile(ctx, w) }
	digest, err := b.saveWorkspaceArchive(ctx, m, "rollback", export)
	if err != nil {
		return "", err
	}
	if _, err = b.saveWorkspaceArchive(ctx, m, "rollback", export); err != nil {
		return "", err
	}
	ids, err := b.Store.MigrationTaskIDs(ctx, m.SandboxID)
	if err != nil {
		return "", err
	}
	history, err := client.ExportPrivateTaskHistory(ctx, ids)
	if err != nil {
		return "", err
	}
	if err = validateHistoryScope(history, ids); err != nil {
		return "", err
	}
	historyDigest, err := b.saveArchive(m, "rollback-history", history)
	if err != nil {
		return "", err
	}
	if err = b.Store.RecordMigrationHistory(ctx, m.SandboxID, m.Phase, historyDigest, true); err != nil {
		return "", err
	}
	return digest, nil
}

func (b *OfflineBackend) RestoreSource(ctx context.Context, m *store.RuntimeMigration) error {
	if err := b.sourceStopped(ctx, m); err != nil {
		return err
	}
	original, _, err := b.readWorkspaceArchive(ctx, m, "source", m.ArchiveSHA256)
	if err != nil {
		return err
	}
	original.Close()
	if _, err := b.readArchive(m, "source-history", m.HistorySHA256); err != nil {
		return err
	}
	data, size, err := b.readWorkspaceArchive(ctx, m, "rollback", m.RollbackSHA256)
	if err != nil {
		return err
	}
	defer data.Close()
	history, err := b.readArchive(m, "rollback-history", m.RollbackHistorySHA256)
	if err != nil {
		return err
	}
	root, err := b.sourceRoot(m)
	if err != nil {
		return err
	}
	if err = b.restoreSourceHome(ctx, m); err != nil {
		return err
	}
	if err = runtime.InstallPrivateWorkspaceFile(ctx, root, data, size); err != nil {
		return err
	}
	// The host importer runs as root; preserve the guest's unprivileged owner.
	if err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, 1000, 1000)
	}); err != nil {
		return err
	}
	digest, err := b.saveWorkspaceArchive(ctx, m, "restored", func(w io.Writer) error {
		return runtime.ExportPrivateWorkspaceFile(ctx, root, filepath.Join(b.WorkspaceRoot, m.SandboxID), w)
	})
	if err != nil {
		return err
	}
	if digest != m.RollbackSHA256 {
		return errors.New("restored Docker workspace checksum mismatch")
	}
	tasksRoot := filepath.Join(b.WorkspaceRoot, m.SandboxID, ".runtimed", "tasks")
	if err = os.MkdirAll(filepath.Dir(tasksRoot), 0755); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(filepath.Dir(tasksRoot))
	if err != nil {
		return err
	}
	if !parentInfo.IsDir() {
		return errors.New("retained runtime directory is not a real directory")
	}
	if err = os.Lchown(filepath.Dir(tasksRoot), 1000, 1000); err != nil {
		return err
	}
	if err = os.Mkdir(tasksRoot, 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	rootInfo, err := os.Lstat(tasksRoot)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() {
		return errors.New("retained task directory is not a real directory")
	}
	if err = runtime.InstallPrivateTaskHistory(tasksRoot, history); err != nil {
		return err
	}
	if err = filepath.Walk(tasksRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, 1000, 1000)
	}); err != nil {
		return err
	}
	ids, err := b.Store.MigrationTaskIDs(ctx, m.SandboxID)
	if err != nil {
		return err
	}
	verified, err := runtime.ExportPrivateTaskHistory(ctx, tasksRoot, ids)
	if err != nil {
		return err
	}
	historyDigest, err := runtime.PrivateWorkspaceDigest(verified)
	if err != nil {
		return err
	}
	if historyDigest != m.RollbackHistorySHA256 {
		return errors.New("restored Docker task history checksum mismatch")
	}
	return nil
}

func (b *OfflineBackend) PauseTarget(ctx context.Context, m *store.RuntimeMigration) error {
	err := b.Cube.Pause(ctx, m.Binding.RuntimeID)
	if err == nil {
		b.Broker.Detach(m.SandboxID)
	}
	return err
}

// AdoptTarget recovers a lost create response without minting a new token or
// trusting a caller-selected guest: metadata AND supervisor authentication match.
func (b *OfflineBackend) AdoptTarget(ctx context.Context, m *store.RuntimeMigration, id, trafficToken string) error {
	if m.Phase != "staging" {
		return store.ErrConflict
	}
	remote, err := b.Cube.Get(ctx, id)
	if err != nil {
		return err
	}
	if remote.TemplateID != m.Binding.TemplateID || remote.Metadata["sandboxd_id"] != m.SandboxID || remote.Metadata["sandboxd_app_id"] != m.Source.AppID.String || remote.Metadata["sandboxd_migration"] != "offline-v1" {
		return errors.New("remote identity does not match migration")
	}
	plain, err := b.Secrets.Open(m.Binding.TokenCiphertext, m.Binding.TokenNonce)
	if err != nil {
		return err
	}
	var c credentials
	if err = json.Unmarshal(plain, &c); err != nil {
		return err
	}
	if trafficToken == "" {
		trafficToken = remote.TrafficAccessToken
	}
	c.TrafficAccessToken = trafficToken
	plain, _ = json.Marshal(c)
	ciphertext, nonce, err := b.Secrets.Seal(plain)
	if err != nil {
		return err
	}
	if err = b.Cube.AdoptAdmission(ctx, id); err != nil {
		return err
	}
	m.Binding.RuntimeID = id
	m.Binding.TokenCiphertext = ciphertext
	m.Binding.TokenNonce = nonce
	client, err := b.connect(ctx, m)
	if err != nil {
		return err
	}
	if _, err = client.Status(ctx); err != nil {
		return err
	}
	return b.Store.SaveMigrationTarget(ctx, m.SandboxID, &m.Binding)
}

// Abort is safe only before cutover. An uncertain remote creation must first be
// adopted so cleanup cannot strand an unknown VM holding owner data.
func (b *OfflineBackend) Abort(ctx context.Context, id string) error {
	m, err := b.Store.GetRuntimeMigration(ctx, id)
	if err != nil {
		return err
	}
	switch m.Phase {
	case "aborted":
		return nil
	case "planned", "quiesced", "archived", "staged", "imported", "verified":
	default:
		return errors.New("cannot abort this phase; recover uncertain create or use data-preserving rollback")
	}
	if m.Binding.RuntimeID != "" {
		err = b.DeleteTarget(ctx, m)
		var upstream *cube.APIError
		if err != nil && !(errors.As(err, &upstream) && upstream.StatusCode == 404) {
			return err
		}
	}
	return b.Store.AbortRuntimeMigration(ctx, id)
}

// DeleteTarget revokes its owned channel even when deletion needs operator recovery.
func (b *OfflineBackend) DeleteTarget(ctx context.Context, m *store.RuntimeMigration) error {
	b.Broker.Detach(m.SandboxID)
	err := b.Cube.Delete(ctx, m.Binding.RuntimeID)
	var upstream *cube.APIError
	if errors.As(err, &upstream) && upstream.StatusCode == 404 {
		return nil
	}
	return err
}
