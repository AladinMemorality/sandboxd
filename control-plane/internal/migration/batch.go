package migration

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
)

type BatchRun struct {
	SandboxID string
	Engine    Engine
}

// RunBatch shares one maintenance owner and Store writer across independent
// projects. It waits for every started operation, even after a sibling fails;
// cancelling an acknowledged provider request could make recovery ambiguous.
// Native admission remains authoritative. Creation alone is serialized because
// its durable admission contract permits only one pending create at a time.
func RunBatch(ctx context.Context, runs []BatchRun) error {
	if len(runs) == 0 || len(runs) > 4 {
		return errors.New("batch requires one to four projects")
	}
	seen := map[string]bool{}
	for _, run := range runs {
		if run.SandboxID == "" || seen[run.SandboxID] || run.Engine.Store == nil || run.Engine.Backend == nil || run.Engine.Store != runs[0].Engine.Store {
			return errors.New("batch requires distinct projects and one shared store")
		}
		seen[run.SandboxID] = true
	}
	creation := &batchCreation{}
	var fence sync.Mutex
	failures := make([]error, len(runs))
	var wg sync.WaitGroup
	for i, run := range runs {
		i, run := i, run
		wg.Add(1)
		go func() {
			defer wg.Done()
			engine := run.Engine
			engine.Backend = &batchBackend{Backend: engine.Backend, creation: creation}
			if before := engine.BeforePhase; before != nil {
				engine.BeforePhase = func() error { fence.Lock(); defer fence.Unlock(); return before() }
			}
			if err := engine.Run(ctx, run.SandboxID); err != nil {
				failures[i] = fmt.Errorf("%s: %w", run.SandboxID, err)
			}
		}()
	}
	wg.Wait()
	return errors.Join(failures...)
}

type batchCreation struct {
	sync.Mutex
	failed error
}
type batchBackend struct {
	Backend
	creation *batchCreation
}

func (b *batchBackend) StageTarget(ctx context.Context, m *store.RuntimeMigration) error {
	b.creation.Lock()
	defer b.creation.Unlock()
	if b.creation.failed != nil {
		return fmt.Errorf("previous batch creation needs review: %w", b.creation.failed)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.creation.failed = b.Backend.StageTarget(ctx, m)
	return b.creation.failed
}
