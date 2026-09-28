package api

import (
	"context"
	"sync"
	"time"
)

type taskTimingKey struct{}
type taskTimings struct {
	mu     sync.Mutex
	stages map[string]int64
}

// Only task-submit contexts enable these spans. They contain no prompts, env,
// credentials or outputs. Nested spans overlap and must not be added together.
func taskStage(ctx context.Context, name string) func() {
	t, ok := ctx.Value(taskTimingKey{}).(*taskTimings)
	if !ok {
		return func() {}
	}
	start := time.Now()
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.stages[name] += time.Since(start).Milliseconds()
	}
}
