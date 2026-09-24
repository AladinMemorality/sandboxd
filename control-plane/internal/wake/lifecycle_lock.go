package wake

import "context"

type lifecycleLockKey struct{}

// WithLifecycleLockHeld is only for in-process callers already holding the
// shared sandbox mutex. No HTTP field can enable this lock handoff.
func WithLifecycleLockHeld(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, lifecycleLockKey{}, id)
}

func lifecycleLockHeld(ctx context.Context, id string) bool {
	return ctx.Value(lifecycleLockKey{}) == id
}
