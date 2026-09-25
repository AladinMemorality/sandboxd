package migration

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type migrationChannel struct {
	ctx        context.Context
	generation string
	phase      string
	appID      string
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
	ctx          context.Context
	cancel       context.CancelFunc
	policy       egress.Policy
	httpServices map[string]map[string]http.Handler
	mu           sync.Mutex
	sessions     map[string]*migrationChannel
	motion       http.Handler
	motionAppID  string
	journal      MigrationJournal
	closed       bool
	wg           sync.WaitGroup
}

func NewMigrationBroker(ctx context.Context, policy egress.Policy, scopedServices ...string) (*MigrationBroker, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy.ProtectedPrefixes = append(policy.ProtectedPrefixes[:0:0], policy.ProtectedPrefixes...)
	policy.ProtectedDomains = append([]string(nil), policy.ProtectedDomains...)
	policy.Ports = append([]uint16(nil), policy.Ports...)
	if len(scopedServices) > 1 {
		return nil, errors.New("one scoped HTTP service configuration required")
	}
	raw := ""
	if len(scopedServices) == 1 {
		raw = scopedServices[0]
	}
	return NewMigrationBrokerWithOptions(ctx, policy, MigrationBrokerOptions{HTTPServices: raw})
}

type MigrationJournal interface {
	GetRuntimeMigration(context.Context, string) (*store.RuntimeMigration, error)
}
type MigrationBrokerOptions struct {
	HTTPServices      string
	MotionStudioAppID string
	Journal           MigrationJournal
}

func NewMigrationBrokerWithOptions(ctx context.Context, policy egress.Policy, options MigrationBrokerOptions) (*MigrationBroker, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	policy.ProtectedPrefixes = append(policy.ProtectedPrefixes[:0:0], policy.ProtectedPrefixes...)
	policy.ProtectedDomains = append([]string(nil), policy.ProtectedDomains...)
	policy.Ports = append([]uint16(nil), policy.Ports...)
	raw := options.HTTPServices
	services, err := egress.ParseHTTPServices(raw, policy)
	if err != nil {
		return nil, err
	}
	handlers := make(map[string]map[string]http.Handler, len(services))
	for appID, selected := range services {
		handlers[appID] = make(map[string]http.Handler, len(selected))
		for _, service := range selected {
			handlers[appID][service.Address()] = service.Handler()
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	b := &MigrationBroker{ctx: ctx, cancel: cancel, policy: policy, httpServices: handlers, sessions: map[string]*migrationChannel{}, motionAppID: options.MotionStudioAppID, journal: options.Journal}
	if options.MotionStudioAppID != "" {
		if options.Journal == nil {
			cancel()
			return nil, errors.New("Motion Studio migration requires authoritative journal")
		}
		b.motion, err = egress.NewMotionStudio(options.MotionStudioAppID, b.authorizeMotion)
		if err != nil {
			cancel()
			return nil, err
		}
	}
	return b, nil
}
func (b *MigrationBroker) Attach(ctx context.Context, m *store.RuntimeMigration, client *runtime.Client) error {
	if m == nil || m.SandboxID == "" || m.Binding.RuntimeID == "" || client == nil {
		return errors.New("migration target identity required")
	}
	generation := migrationChannelGeneration(m)
	if b.motion != nil && m.Source.AppID.Valid && m.Source.AppID.String == b.motionAppID && motionMigrationPhase(m.Phase) {
		if !b.motionJournalMatches(ctx, m.SandboxID, generation, b.motionAppID) {
			return errors.New("Motion Studio migration journal is not an eligible owned target")
		}
	}
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
		entry = &migrationChannel{ctx: life, generation: generation, phase: m.Phase, appID: m.Source.AppID.String, cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
		b.sessions[m.SandboxID] = entry
		b.wg.Add(1)
		services := map[string]http.Handler{}
		if m.Source.AppID.Valid {
			for address, handler := range b.httpServices[m.Source.AppID.String] {
				services[address] = handler
			}
		}
		named := map[string]http.Handler{}
		if b.motion != nil && m.Source.AppID.Valid && m.Source.AppID.String == b.motionAppID && motionMigrationPhase(m.Phase) {
			named["motion"] = b.motion
		}
		go b.run(life, m.SandboxID, client, entry, services, named)
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
func (b *MigrationBroker) run(ctx context.Context, id string, client *runtime.Client, entry *migrationChannel, services map[string]http.Handler, named map[string]http.Handler) {
	defer b.wg.Done()
	defer close(entry.done)
	for ctx.Err() == nil {
		conn, err := client.OpenEgressChannel(ctx)
		if err == nil {
			entry.mu.Lock()
			entry.connected = true
			close(entry.ready)
			entry.mu.Unlock()
			_ = egress.RunHost(ctx, conn, egress.HostOptions{Identity: egress.Identity{SandboxID: id, Generation: entry.generation}, Policy: b.policy, HTTPServices: services, Services: named})
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
