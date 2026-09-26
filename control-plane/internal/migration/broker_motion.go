package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

func migrationChannelGeneration(m *store.RuntimeMigration) string {
	raw, _ := json.Marshal([]any{m.SandboxID, m.Phase, m.Source.AppID, m.Binding.RuntimeID, m.Binding.TemplateID, m.Binding.Domain, m.Binding.TokenCiphertext, m.Binding.TokenNonce})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (b *MigrationBroker) motionJournalMatches(ctx context.Context, id, generation, appID string) bool {
	if ctx.Err() != nil || b.ctx.Err() != nil || b.journal == nil {
		return false
	}
	m, err := b.journal.GetRuntimeMigration(ctx, id)
	if err != nil || m == nil || m.SandboxID != id || m.Source.ID != id || m.Source.RuntimeProvider != "docker" || !m.Source.AppID.Valid || m.Source.AppID.String != appID || m.Binding.RuntimeID == "" || m.Binding.TemplateID == "" || m.Binding.Domain == "" || len(m.Binding.TokenCiphertext) == 0 || len(m.Binding.TokenNonce) == 0 || migrationChannelGeneration(m) != generation {
		return false
	}
	// The channel is only for this journaled target. Never grant through an
	// uncertain-create, aborted, rollback/recovery or already committed journal.
	return motionMigrationPhase(m.Phase)
}
func (b *MigrationBroker) authorizeMotion(ctx context.Context, id egress.Identity, appID string) bool {
	if appID != b.motionAppID || !b.motionJournalMatches(ctx, id.SandboxID, id.Generation, appID) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.sessions[id.SandboxID]
	return !b.closed && entry != nil && entry.ctx.Err() == nil && entry.generation == id.Generation && entry.appID == appID
}
func (b *MigrationBroker) motionEnvironment(ctx context.Context, m *store.RuntimeMigration, status *runtime.Status, env map[string]string) (map[string]string, error) {
	id := egress.Identity{SandboxID: m.SandboxID, Generation: migrationChannelGeneration(m)}
	if b.motion == nil || !b.authorizeMotion(ctx, id, b.motionAppID) {
		return nil, errors.New("Motion Studio target does not have an active journal-bound worker capability")
	}
	return runtime.MotionStudioEnvironment(env, status)
}

func motionMigrationPhase(phase string) bool {
	return phase == "staged" || phase == "imported" || phase == "verified"
}
