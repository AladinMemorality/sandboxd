package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type migrationChannel struct {
	ctx        context.Context
	generation string
	cancel     context.CancelFunc
	done       chan struct{}
	ready      chan struct{}
	mu         sync.Mutex
	connected  bool
}

// MigrationBroker owns only authenticated channels to journaled target guests.
// No model/bridge service is exposed: migration excludes active agent tasks.
// Public requests use the same reviewed policy as the online control plane.
type MigrationBroker struct {
	ctx      context.Context
	cancel   context.CancelFunc
	policy   egress.Policy
	mu       sync.Mutex
	sessions map[string]*migrationChannel
	closed   bool
	wg       sync.WaitGroup
}

func NewMigrationBroker(ctx context.Context, policy egress.Policy) (*MigrationBroker, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy.ProtectedPrefixes = append(policy.ProtectedPrefixes[:0:0], policy.ProtectedPrefixes...)
	policy.ProtectedDomains = append([]string(nil), policy.ProtectedDomains...)
	policy.Ports = append([]uint16(nil), policy.Ports...)
	ctx, cancel := context.WithCancel(ctx)
	return &MigrationBroker{ctx: ctx, cancel: cancel, policy: policy, sessions: map[string]*migrationChannel{}}, nil
}
func (b *MigrationBroker) Attach(ctx context.Context, m *store.RuntimeMigration, client *runtime.Client) error {
	if m == nil || m.SandboxID == "" || m.Binding.RuntimeID == "" || client == nil {
		return errors.New("migration target identity required")
	}
	raw, _ := json.Marshal([]any{m.Binding.RuntimeID, m.Binding.TemplateID, m.Binding.Domain, m.Binding.TokenCiphertext, m.Binding.TokenNonce})
	digest := sha256.Sum256(raw)
	generation := hex.EncodeToString(digest[:])
	b.mu.Lock()
	if b.closed || b.ctx.Err() != nil || ctx.Err() != nil {
		b.mu.Unlock()
		return errors.New("migration broker closed")
	}
	old := b.sessions[m.SandboxID]
	if old != nil && old.generation != generation {
		old.cancel()
		delete(b.sessions, m.SandboxID)
		b.mu.Unlock()
		select {
		case <-old.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		return b.Attach(ctx, m, client)
	}
	entry := old
	if entry == nil {
		life, cancel := context.WithCancel(b.ctx)
		entry = &migrationChannel{ctx: life, generation: generation, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
		b.sessions[m.SandboxID] = entry
		b.wg.Add(1)
		go b.run(life, m.SandboxID, client, entry)
	}
	b.mu.Unlock()
	for {
		entry.mu.Lock()
		ready, connected := entry.ready, entry.connected
		entry.mu.Unlock()
		if connected {
			if entry.ctx.Err() != nil {
				return entry.ctx.Err()
			}
			if b.ctx.Err() != nil {
				return b.ctx.Err()
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		select {
		case <-ready:
		case <-entry.done:
			return errors.New("migration broker stopped")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (b *MigrationBroker) run(ctx context.Context, id string, client *runtime.Client, entry *migrationChannel) {
	defer b.wg.Done()
	defer close(entry.done)
	for ctx.Err() == nil {
		conn, err := client.OpenEgressChannel(ctx)
		if err == nil {
			entry.mu.Lock()
			entry.connected = true
			close(entry.ready)
			entry.mu.Unlock()
			_ = egress.RunHost(ctx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: id, Generation: entry.generation}, Policy: b.policy})
			entry.mu.Lock()
			entry.connected = false
			entry.ready = make(chan struct{})
			entry.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (b *MigrationBroker) Detach(id string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	entry := b.sessions[id]
	delete(b.sessions, id)
	b.mu.Unlock()
	if entry != nil {
		entry.cancel()
		<-entry.done
	}
}
func (b *MigrationBroker) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		b.wg.Wait()
		return
	}
	b.closed = true
	b.cancel()
	entries := b.sessions
	b.sessions = map[string]*migrationChannel{}
	b.mu.Unlock()
	for _, e := range entries {
		e.cancel()
	}
	b.wg.Wait()
}
