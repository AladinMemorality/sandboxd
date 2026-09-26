package main

import (
	"context"
	"errors"
	rt "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"testing"
	"time"
)

func TestWaitStatusIgnoresOldGenerationAndTransientRestartFailure(t *testing.T) {
	old := time.Now()
	attempts := 0
	waitStatus(context.Background(), func(context.Context) (*rt.Status, error) {
		attempts++
		if attempts == 2 {
			return nil, errors.New("supervisor reexec")
		}
		s := &rt.Status{}
		s.Runtimed.BootedAt = old
		if attempts == 3 {
			s.Runtimed.BootedAt = old.Add(time.Second)
		}
		return s, nil
	}, func(s *rt.Status) bool { return !s.Runtimed.BootedAt.Equal(old) })
	if attempts != 3 {
		t.Fatal("old supervisor acknowledged imported workspace")
	}
}

func TestWaitStatusCancellationCannotAcknowledgeConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer func() {
		if recover() == nil {
			t.Fatal("canceled wait accepted old config")
		}
	}()
	waitStatus(ctx, func(context.Context) (*rt.Status, error) { t.Fatal("request after cancellation"); return nil, nil }, func(*rt.Status) bool { return true })
}
