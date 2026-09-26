// cube-controller owns Baarcha projects and tasks using Cube exclusively.
// Durable schemas and existing environment keys are retained across cutover;
// no sandboxd process, Docker client, socket, reaper or workspace provisioner
// participates in serving this controller.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/activity"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/agentauth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/api"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/audit"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/auth"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/authproxy"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/cubeconfig"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/events"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/idlock"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/instancecfg"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/logging"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/metrics"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

var buildVersion = "dev"
var buildCommit = "unknown"

func main() {
	if len(os.Args) > 1 {
		if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "--version") {
			fmt.Printf("cube-controller %s (%s)\n", buildVersion, buildCommit)
			return
		}
		fmt.Fprintln(os.Stderr, "usage: cube-controller [version]")
		os.Exit(2)
	}
	if err := run(); err != nil {
		logging.NewLogger().Error("Cube controller stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := cubeconfig.Load()
	if err != nil {
		return err
	}
	if cfg.Client == nil || !cfg.AllApps || cfg.ReverseEgress == nil {
		return errors.New("Cube controller requires global rollout and reviewed reverse egress")
	}
	authCfg, err := strictAuth(os.Getenv)
	if err != nil {
		return err
	}
	dataDir := env("SANDBOXD_DATA_DIR", "/var/lib/sandboxd")
	dbPath := env("SANDBOXD_DB", filepath.Join(dataDir, "state", "sandboxd.db"))
	keyPath := filepath.Join(dataDir, "secrets.key")
	// A mistyped mount must never silently start an empty installation or
	// generate a new key over customer ciphertext.
	if err := requireFile(dbPath); err != nil {
		return err
	}
	if os.Getenv("SANDBOXD_SECRETS_KEY") == "" {
		if err := requireFile(keyPath); err != nil {
			return err
		}
	}
	// Exclusive for the process lifetime: this also excludes the old daemon's
	// shared lock, preventing two controllers from accepting the same task.
	lock, err := maintenance.Acquire(dbPath, true)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := maintenance.CheckWorkerStop(dbPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dsn := (&url.URL{Scheme: "file", Path: dbPath}).String() + "?mode=rw&_journal=WAL&_busy_timeout=5000&_fk=1"
	st, err := store.Open(ctx, dsn, env("SANDBOXD_MIGRATIONS", "/usr/local/share/cube-controller/migrations"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.CheckCubeOnly(ctx); err != nil {
		return err
	}
	if pending, e := st.HasIncompleteRuntimeMigrations(ctx); e != nil || pending {
		return errors.New("incomplete runtime migration requires offline reconciliation")
	}
	if pending, e := st.HasIncompleteCubeRecoveries(ctx); e != nil || pending {
		return errors.New("incomplete Cube recovery requires offline reconciliation")
	}
	if err := cubeconfig.ConfigureAdmission(ctx, cfg, st); err != nil {
		return err
	}
	admission, err := cube.ParseAdmissionConfig(os.Getenv("SANDBOXD_CUBE_ADMISSION"))
	if err != nil {
		return err
	}
	if err := admission.RequireStorageGuard(); err != nil {
		return err
	}
	cipher, err := retainedCipher(os.Getenv("SANDBOXD_SECRETS_KEY"), keyPath)
	if err != nil {
		return err
	}
	// Verify the actual retained key before any reconciliation or listener.
	rows, err := st.List(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		binding, e := st.GetRuntimeBinding(ctx, row.ID)
		if e != nil {
			return errors.New("runtime binding unavailable")
		}
		if _, e = cipher.Open(binding.TokenCiphertext, binding.TokenNonce); e != nil {
			return errors.New("retained encryption key cannot open runtime credentials")
		}
	}
	log := logging.NewLogger()
	auditLog := audit.New(st, log.With("component", "audit"))
	authMW := auth.NewMiddleware(authCfg, api.NewStoreResolver(st), auditLog, log.With("component", "auth"))
	idleSeconds, err := nonnegativeEnv("SANDBOXD_IDLE_THRESHOLD_SECONDS", 2100)
	if err != nil {
		return err
	}
	keepaliveSeconds, err := nonnegativeEnv("SANDBOXD_KEEPALIVE_MAX_SECONDS", 86400)
	if err != nil {
		return err
	}
	live := instancecfg.New(instancecfg.Snapshot{IdleEnabled: true, IdleThresholdSeconds: idleSeconds, KeepaliveMaxSeconds: keepaliveSeconds})
	if persisted, e := st.GetInstanceSettings(ctx); e == nil {
		live.Set(instancecfg.Snapshot{IdleEnabled: persisted.IdleReapEnabled, IdleThresholdSeconds: persisted.IdleThresholdSeconds,
			KeepaliveMaxSeconds: persisted.KeepaliveMaxSeconds, DefaultModels: persisted.AgentDefaultModels})
	} else if !errors.Is(e, store.ErrNotFound) {
		return e
	}
	scheme := env("PREVIEW_URL_SCHEME", "https")
	if scheme != "http" && scheme != "https" {
		return errors.New("invalid preview URL scheme")
	}
	agentAuth := agentauth.NewStore(dataDir)
	// Credential injection stays inside this process's loopback namespace.
	// Only the task-scoped Cube relay can reach it from a guest; no raw model
	// proxy listener is published on the service network.
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer proxyListener.Close()
	proxyServer := &http.Server{Handler: authproxy.New(agentAuth, log.With("component", "model-auth")),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	defer proxyServer.Close()
	server := &api.Server{
		Store: st, Secrets: cipher, Log: log.With("component", "api"),
		Cube: cfg.Client, CubeAllApps: true, CubeTemplates: cfg.Templates, CubeProxyURL: cfg.ProxyURL,
		CubeDomain: cfg.Domain, CubeAgentRelayOrigin: cfg.RelayOrigin,
		CubeReadiness: func(ctx context.Context) error {
			now, err := cube.ReadStorageClock()
			if err != nil {
				return err
			}
			if _, err := cube.ReadStorageObservation(*admission.StorageGuard, now); err != nil {
				return err
			}
			_, err = cfg.Client.Inventory(ctx)
			return err
		},
		AgentAuth: agentAuth, AgentOAuth: agentauth.NewOAuth(agentAuth),
		AgentProxyURL: "http://" + proxyListener.Addr().String(),
		DefaultAgent:  env("SANDBOXD_DEFAULT_AGENT", "claude-code"),
		OpencodeModel: os.Getenv("SANDBOXD_OPENCODE_MODEL"), OpencodeZenPath: os.Getenv("SANDBOXD_OPENCODE_ZEN_PATH"),
		PreviewDomain: env("PREVIEW_DOMAIN", "localhost"), PreviewURLScheme: scheme,
		PublicHTTPPort: env("SANDBOXD_PUBLIC_HTTP_PORT", "443"), PreviewTLS: scheme == "https",
		Inflight: activity.NewInflightExec(), Locks: idlock.New(), Live: live,
		KeepaliveMax: time.Duration(keepaliveSeconds) * time.Second,
		Auth:         authMW, Audit: auditLog, Events: events.New(st, log.With("component", "events")),
		ForwardAuthDenyMode: env("SANDBOXD_FORWARD_AUTH_DENY_MODE", "redirect"),
		LibraryRoot:         env("SANDBOXD_LIBRARY_DIR", filepath.Join(dataDir, "library")),
		RetainedHistoryRoot: env("CUBE_RETAINED_HISTORY_ROOT", filepath.Join(dataDir, "workspaces")),
		LogDir:              env("SANDBOXD_LOG_DIR", "/var/log/sandboxd"),
		Instance: api.InstanceInfo{Version: buildVersion, GitCommit: buildCommit, AuthEnabled: true,
			StorageMode: "cube", EgressMode: "reverse-proxy", AgentProviders: []string{"claude-code"},
			IdleReapEnabled: true, IdleThresholdSeconds: idleSeconds},
	}
	if err := server.ConfigureCubeEgress(ctx, *cfg.ReverseEgress); err != nil {
		return err
	}
	handler, err := server.CubeHandler(ctx)
	if err != nil {
		return err
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	err = server.CubeReadiness(readyCtx)
	readyCancel()
	if err != nil {
		return errors.New("Cube worker/storage readiness failed")
	}
	// Bind before reconciliation so an occupied listener fails without
	// changing tenant runtime state. There is no Docker boot reconciler.
	listener, err := net.Listen("tcp", env("CUBE_CONTROLLER_ADDR", "127.0.0.1:9090"))
	if err != nil {
		return err
	}
	defer listener.Close()
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- proxyServer.Serve(proxyListener) }()
	server.ReconcileCube(ctx)
	server.ReconcileTasks(ctx)
	maintenanceDone := make(chan struct{})
	go func() { defer close(maintenanceDone); server.RunCubeMaintenance(ctx) }()
	defer func() { cancel(); <-maintenanceDone }()
	authDone := make(chan struct{})
	go func() {
		defer close(authDone)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			if server.AgentOAuth != nil {
				if err := server.AgentOAuth.Refresh(); err != nil {
					log.Warn("model credential refresh failed; retained credential preserved")
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { cancel(); <-authDone }()
	metrics.BuildInfo.WithLabelValues(buildVersion, buildCommit).Set(1)
	_ = metrics.RefreshSandboxGauge(ctx, st)
	httpServer := &http.Server{Handler: logging.Middleware(log, server.CubePreviewHandler(authMW.Wrap(handler))),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { errorsCh <- httpServer.Serve(listener) }()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	log.Info("Cube controller ready", "addr", listener.Addr().String(), "projects", len(rows))
	for {
		select {
		case sig := <-signals:
			if sig == syscall.SIGHUP {
				envMap, e := auth.LoadEnvFile(env("SANDBOXD_ENV_FILE", "/etc/baarcha-cube/controller.env"))
				if e == nil {
					var next *auth.Config
					next, e = strictAuth(auth.MapGetter(envMap))
					if e == nil {
						authMW.Reload(next)
					}
				}
				if e != nil {
					log.Error("auth reload refused; existing configuration retained")
				}
				continue
			}
		case err = <-errorsCh:
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
		}
		break
	}
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	if e := httpServer.Shutdown(shutdownCtx); e != nil {
		_ = httpServer.Close()
		if err == nil {
			err = e
		}
	}
	if e := proxyServer.Shutdown(shutdownCtx); e != nil && err == nil {
		err = e
	}
	return err
}

func strictAuth(get func(string) string) (*auth.Config, error) {
	cfg := auth.ParseConfig(get)
	if cfg.Disabled || len(cfg.APITokens) == 0 {
		return nil, errors.New("Cube controller requires service-token authentication")
	}
	for _, token := range cfg.APITokens {
		if strings.TrimSpace(token.Token) == "" {
			return nil, errors.New("empty service token")
		}
	}
	if err := auth.ValidatePreviewSecrets(get("SANDBOXD_PREVIEW_TOKEN_SECRETS")); err != nil {
		return nil, err
	}
	return cfg, nil
}

func retainedCipher(envKey, keyPath string) (*secrets.Cipher, error) {
	key := envKey
	if key == "" {
		b, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, errors.New("retained encryption key cannot be read")
		}
		key = string(b)
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("retained encryption key is empty")
	}
	// Supply the existing bytes as an explicit key. secrets.Load's automatic
	// key generation path is never reachable, including on read errors.
	return secrets.Load(key, "")
}

func requireFile(path string) error {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return errors.New("Cube controller requires existing regular database and encryption key files")
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func nonnegativeEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || n > 86400*365 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}
