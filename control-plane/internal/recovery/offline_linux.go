// Package recovery is an offline operator-only coordinator. It has no tenant
// route and never infers that a dead worker, archive or provider request drained
// safely merely from elapsed time. External evidence remains operator-reviewed.
package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/cube"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/maintenance"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/secrets"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type Session struct {
	mu     sync.Mutex
	lock   *os.File
	db     *store.Store
	client *cube.Client
	cipher *secrets.Cipher
	path   string
	closed bool
}
type credentials struct {
	Supervisor string `json:"supervisor_token"`
	Traffic    string `json:"traffic_access_token"`
}

// Open requires the native host root namespace for the /proc writer scan. The
// operator must separately disable legacy daemon restarts; flock only fences
// cooperating binaries. Opening this session does not create a recovery journal.
func Open(ctx context.Context, database, migrations string, key *secrets.Cipher, provider cube.Config, policy cube.AdmissionConfig) (*Session, error) {
	return open(ctx, database, migrations, key, provider, policy, "", "vps")
}

// OpenPinned retains the same offline maintenance fence while using the
// authoritative placement verifier required by an existing pinned worker policy.
// It does not select a worker or weaken the durable admission contract.
func OpenPinned(ctx context.Context, database, migrations string, key *secrets.Cipher, provider cube.Config, policy cube.AdmissionConfig, masterURL string) (*Session, error) {
	if policy.NodeID == "" || masterURL == "" {
		return nil, errors.New("pinned worker and trusted master URL required")
	}
	return open(ctx, database, migrations, key, provider, policy, masterURL, "vps")
}

// OpenPinnedWorker recovers onto the source worker's existing durable partition.
// Cross-worker movement is not authorized by this operation.
func OpenPinnedWorker(ctx context.Context, database, migrations string, key *secrets.Cipher, provider cube.Config, policy cube.AdmissionConfig, masterURL, worker string) (*Session, error) {
	if policy.NodeID == "" || masterURL == "" || worker == "" {
		return nil, errors.New("pinned worker and trusted master URL required")
	}
	return open(ctx, database, migrations, key, provider, policy, masterURL, worker)
}

func open(ctx context.Context, database, migrations string, key *secrets.Cipher, provider cube.Config, policy cube.AdmissionConfig, masterURL, worker string) (*Session, error) {
	if os.Geteuid() != 0 || key == nil {
		return nil, errors.New("native root and existing controller encryption key required")
	}
	path, e := filepath.Abs(database)
	if e != nil {
		return nil, e
	}
	path, e = filepath.EvalSymlinks(path)
	if e != nil {
		return nil, e
	}
	lock, e := maintenance.Acquire(path, true)
	if e != nil {
		return nil, e
	}
	failed := true
	defer func() {
		if failed {
			lock.Close()
		}
	}()
	if e = maintenance.CheckDatabaseUsers(path); e != nil {
		return nil, e
	}
	db, e := store.OpenRecoveryWorker(ctx, "file:"+path+"?_journal=WAL&_busy_timeout=5000&_fk=1", migrations, worker)
	if e != nil {
		return nil, e
	}
	defer func() {
		if failed {
			db.Close()
		}
	}()
	client, e := cube.New(provider)
	if e != nil {
		return nil, e
	}
	if masterURL != "" {
		if e = client.ConfigurePlacement(masterURL, "cubebox"); e != nil {
			return nil, e
		}
	}
	if e = client.ConfigureAdmission(ctx, db, policy); e != nil {
		return nil, e
	}
	failed = false
	return &Session{lock: lock, db: db, client: client, cipher: key, path: path}, nil
}
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	e := s.db.Close()
	other := s.lock.Close()
	return errors.Join(e, other)
}
func (s *Session) check() error {
	if s.closed || s.lock == nil {
		return errors.New("offline recovery session closed")
	}
	return maintenance.CheckDatabaseUsers(s.path)
}
func (s *Session) Journal(ctx context.Context, id string) (*store.CubeRecoveryJournal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return nil, e
	}
	return s.db.GetCubeRecovery(ctx, id)
}
func (s *Session) Begin(ctx context.Context, p store.CubeRecoveryPlan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return e
	}
	old, e := s.db.GetRuntimeBinding(ctx, p.SandboxID)
	if e != nil {
		return e
	}
	plain, e := s.cipher.Open(old.TokenCiphertext, old.TokenNonce)
	if e != nil {
		return errors.New("controller key cannot open retained source binding")
	}
	var previous credentials
	if json.Unmarshal(plain, &previous) != nil || previous.Supervisor == "" || previous.Traffic == "" {
		return errors.New("retained source credential format invalid")
	}
	var token [32]byte
	if _, e = rand.Read(token[:]); e != nil {
		return e
	}
	c := credentials{Supervisor: hex.EncodeToString(token[:])}
	raw, _ := json.Marshal(c)
	p.PlannedCiphertext, p.PlannedNonce, e = s.cipher.Seal(raw)
	if e != nil {
		return e
	}
	p.SupervisorSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(c.Supervisor)))
	return s.db.BeginCubeRecovery(ctx, p)
}
func (s *Session) Fence(ctx context.Context, f store.CubeRecoveryFence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return e
	}
	return s.db.RecordCubeRecoveryFence(ctx, f)
}
func (s *Session) planned(j *store.CubeRecoveryJournal) (credentials, error) {
	var c credentials
	raw, e := s.cipher.Open(j.PlannedCiphertext, j.PlannedNonce)
	if e != nil {
		return c, errors.New("cannot decrypt planned recovery credential")
	}
	if json.Unmarshal(raw, &c) != nil || fmt.Sprintf("%x", sha256.Sum256([]byte(c.Supervisor))) != j.SupervisorSHA256 {
		return c, errors.New("planned credential mismatch")
	}
	return c, nil
}
func (s *Session) recordCredential(ctx context.Context, j *store.CubeRecoveryJournal, c credentials, remote *cube.Sandbox) error {
	if remote.TrafficAccessToken == "" {
		return errors.New("recovery target private ingress credential unavailable; retain journal and inspect provider")
	}
	// If the acknowledgment survived but this write did not, Adopt can repeat
	// exact-target verification and seal the same supervisor/ingress credentials.
	existing, e := s.db.GetCubeRecovery(ctx, j.ID)
	if e != nil {
		return e
	}
	if len(existing.Target.TokenCiphertext) > 0 {
		raw, e := s.cipher.Open(existing.Target.TokenCiphertext, existing.Target.TokenNonce)
		var previous credentials
		if e != nil || json.Unmarshal(raw, &previous) != nil || previous.Supervisor != c.Supervisor || previous.Traffic != remote.TrafficAccessToken {
			return errors.New("retained replacement credential mismatch")
		}
		return nil
	}
	c.Traffic = remote.TrafficAccessToken
	raw, _ := json.Marshal(c)
	cipher, nonce, e := s.cipher.Seal(raw)
	if e != nil {
		return e
	}
	return s.db.RecordCubeRecoveryCredential(ctx, j.ID, remote.SandboxID, cipher, nonce)
}
func (s *Session) Create(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return e
	}
	j, e := s.db.GetCubeRecovery(ctx, id)
	if e != nil {
		return e
	}
	c, e := s.planned(j)
	if e != nil {
		return e
	}
	input := cube.CreateRequest{TemplateID: j.Target.TemplateID, TimeoutSeconds: 3600, Lifecycle: &cube.Lifecycle{OnTimeout: "pause", AutoResume: false}, Network: &cube.NetworkPolicy{DenyOut: []string{"0.0.0.0/0"}}, EnvVars: map[string]string{"RUNTIMED_HTTP_ADDR": ":3031", "RUNTIMED_HTTP_TOKEN": c.Supervisor}, Metadata: map[string]string{"sandboxd_id": j.SandboxID, "sandboxd_app_id": j.AppID}}
	remote, e := s.client.CreateRecovery(ctx, s.db, id, input)
	if e != nil {
		return e
	}
	durable, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stop()
	return s.recordCredential(durable, j, c, remote)
}
func (s *Session) Adopt(ctx context.Context, id, runtimeID string) error {
	return s.adopt(ctx, id, runtimeID, "", "")
}

// AdoptWithIngress uses the private credential from a retained create response
// when the provider detail API intentionally omits it. Authenticate it against
// the journal's independently generated supervisor before sealing the binding.
func (s *Session) AdoptWithIngress(ctx context.Context, id, runtimeID, traffic, proxy string) error {
	if traffic == "" || proxy == "" {
		return errors.New("retained private ingress and trusted proxy required")
	}
	return s.adopt(ctx, id, runtimeID, traffic, proxy)
}
func authenticateIngress(ctx context.Context, remote *cube.Sandbox, supervisor, traffic, proxy string) error {
	if remote.TrafficAccessToken != "" && remote.TrafficAccessToken != traffic {
		return errors.New("provider ingress credential differs from retained acknowledgment")
	}
	client, e := runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: proxy, Host: "3031-" + remote.SandboxID + ".cube.app", Token: supervisor, TrafficAccessToken: traffic})
	if e != nil {
		return e
	}
	if _, e = client.Status(ctx); e != nil {
		return errors.New("retained ingress could not authenticate the planned supervisor")
	}
	remote.TrafficAccessToken = traffic
	return nil
}
func (s *Session) adopt(ctx context.Context, id, runtimeID, traffic, proxy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return e
	}
	j, e := s.db.GetCubeRecovery(ctx, id)
	if e != nil {
		return e
	}
	c, e := s.planned(j)
	if e != nil {
		return e
	}
	remote, e := s.client.AdoptRecovery(ctx, s.db, id, runtimeID)
	if e != nil {
		return e
	}
	if traffic != "" {
		if e = authenticateIngress(ctx, remote, c.Supervisor, traffic, proxy); e != nil {
			return e
		}
	}
	return s.recordCredential(ctx, j, c, remote)
}

// ResumeTarget can only touch the recorded replacement, never the quarantined
// old runtime. A fresh verification receipt is required after any lease change.
func (s *Session) ResumeTarget(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return e
	}
	j, e := s.db.GetCubeRecovery(ctx, id)
	if e != nil {
		return e
	}
	if (j.Phase != "created" && j.Phase != "verified") || j.Target.RuntimeID == "" {
		return errors.New("recorded recovery target required")
	}
	_, e = s.client.Connect(ctx, j.Target.RuntimeID, cube.ConnectRequest{TimeoutSeconds: 3600})
	return e
}
func (s *Session) Verify(ctx context.Context, v store.CubeRecoveryVerification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return e
	}
	return s.db.VerifyCubeRecovery(ctx, v)
}
func (s *Session) Commit(ctx context.Context, id, old string, revision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return e
	}
	return s.db.CommitCubeRecovery(ctx, id, old, revision)
}

// TargetClient returns credentials only inside a scoped runtime client. Its
// trusted proxy origin comes from the operator, never guest input. The caller
// must retain this session's maintenance lock while importing/verifying.
func (s *Session) TargetClient(ctx context.Context, id, proxyOrigin string) (*runtime.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return nil, e
	}
	j, e := s.db.GetCubeRecovery(ctx, id)
	if e != nil {
		return nil, e
	}
	if (j.Phase != "created" && j.Phase != "verified") || j.Target.RuntimeID == "" {
		return nil, errors.New("recorded replacement required")
	}
	raw, e := s.cipher.Open(j.Target.TokenCiphertext, j.Target.TokenNonce)
	if e != nil {
		return nil, errors.New("replacement credentials unavailable")
	}
	var c credentials
	if json.Unmarshal(raw, &c) != nil || c.Traffic == "" {
		return nil, errors.New("invalid replacement credentials")
	}
	host := "3031-" + j.Target.RuntimeID + "." + j.Target.Domain
	return runtime.NewRemoteClient(runtime.RemoteConfig{BaseURL: proxyOrigin, Host: host, Token: c.Supervisor, TrafficAccessToken: c.Traffic})
}

// AdmissionToken binds a final verification to the current replacement lease.
// Every Connect/pause/resume after verification requires a new receipt.
func (s *Session) AdmissionToken(ctx context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return "", e
	}
	j, e := s.db.GetCubeRecovery(ctx, id)
	if e != nil {
		return "", e
	}
	a, e := s.db.AdmissionLookup(ctx, j.Target.RuntimeID)
	if e != nil {
		return "", e
	}
	if a.State != "active" || a.Charged != 1 {
		return "", cube.ErrAdmissionPending
	}
	return a.Token, nil
}
