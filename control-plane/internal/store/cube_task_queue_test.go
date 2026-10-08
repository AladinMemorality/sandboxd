package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func queueFixture(t *testing.T) *Store {
	s, _ := recoveryFixture(t, 4)
	cfg := resourceTestConfig(12000)
	raw, _ := json.Marshal(resourceContract{Budget: *cfg.ResourceBudget, Templates: cfg.Templates})
	if _, err := s.db.Exec(`INSERT INTO cube_resource_budget(worker_id,contract) VALUES('vps',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestCodingQueueConcurrentClaimsAndUnknownDispatchStayCharged(t *testing.T) {
	s := queueFixture(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := s.EnqueueCubeTask(ctx, &Task{TaskID: fmt.Sprintf("task-%d", i), SandboxID: fmt.Sprintf("stable-%d", i), Agent: "claude-code", Prompt: "test"}, []byte("ciphertext"), []byte("nonce")); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	claims := make(chan *CubeQueuedTask, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q, err := s.ClaimCubeTask(ctx, fmt.Sprintf("claim-%d", i), 2)
			if err != nil {
				t.Error(err)
			}
			if q != nil {
				claims <- q
			}
		}(i)
	}
	wg.Wait()
	close(claims)
	var accepted []*CubeQueuedTask
	for q := range claims {
		accepted = append(accepted, q)
	}
	if len(accepted) != 2 {
		t.Fatalf("concurrent claims=%d", len(accepted))
	}
	if err := s.BeginCubeTaskDispatch(ctx, *accepted[0]); err != nil {
		t.Fatal(err)
	}
	if q, err := s.ClaimCubeTask(ctx, "third", 2); err != nil || q != nil {
		t.Fatalf("ambiguous dispatch released capacity: %+v %v", q, err)
	}
	if err := s.FinishTask(ctx, accepted[0].TaskID, "succeeded", `{}`); err != nil {
		t.Fatal(err)
	}
	if q, err := s.ClaimCubeTask(ctx, "third", 2); err != nil || q == nil {
		t.Fatalf("confirmed completion did not release slot: %+v %v", q, err)
	}
}
func TestCodingQueueRestartCancellationAndDuplicateSandboxFence(t *testing.T) {
	s := queueFixture(t)
	ctx := context.Background()
	task := &Task{TaskID: "one", SandboxID: "stable-0", Agent: "claude-code", Prompt: "private"}
	if err := s.EnqueueCubeTask(ctx, task, []byte("encrypted"), []byte("nonce")); err != nil {
		t.Fatal(err)
	}
	task.TaskID = "duplicate"
	if err := s.EnqueueCubeTask(ctx, task, []byte("encrypted"), []byte("nonce")); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate project queued", err)
	}
	old, err := s.ClaimCubeTask(ctx, "old-process", 2)
	if err != nil || old == nil {
		t.Fatal(err)
	}
	if err = s.ResetPreparingCubeTasks(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimCubeTask(ctx, "new-process", 2)
	if err != nil || next == nil {
		t.Fatal(err)
	}
	if err = s.BeginCubeTaskDispatch(ctx, *old); !errors.Is(err, ErrConflict) {
		t.Fatal("old process can dispatch after restart", err)
	}
	if err = s.FinishUndispatchedCubeTask(ctx, *old, `{"status":"failed"}`); !errors.Is(err, ErrConflict) {
		t.Fatal("old claim can fail the new claim", err)
	}
	if ok, err := s.CancelQueuedCubeTask(ctx, "other-project", next.TaskID, `{}`); err != nil || ok {
		t.Fatal("cross-project cancellation")
	}
	if ok, err := s.CancelQueuedCubeTask(ctx, next.SandboxID, next.TaskID, `{"status":"cancelled"}`); err != nil || !ok {
		t.Fatal("queued cancellation", err)
	}
	if err = s.BeginCubeTaskDispatch(ctx, *next); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled task can dispatch", err)
	}
	got, err := s.GetTask(ctx, next.TaskID)
	if err != nil || got.Status != "cancelled" {
		t.Fatal("cancellation not durable", err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM cube_task_queue`).Scan(&n)
	if n != 0 {
		t.Fatal("cancelled encrypted payload retained")
	}
}

func TestCodingQueueCompletionAndSecretDeletionAreAtomic(t *testing.T) {
	s := queueFixture(t)
	ctx := context.Background()
	if err := s.EnqueueCubeTask(ctx, &Task{TaskID: "atomic", SandboxID: "stable-0", Agent: "opencode"}, []byte("encrypted"), []byte("nonce")); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCubeTask(ctx, "claim", 2)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	if err = s.BeginCubeTaskDispatch(ctx, *claim); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER block_queue_delete BEFORE DELETE ON cube_task_queue BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, "atomic", "succeeded", `{}`); err == nil {
		t.Fatal("injected failure not surfaced")
	}
	task, err := s.GetTask(ctx, "atomic")
	if err != nil || task.Status != "running" || task.ResultJSON.Valid {
		t.Fatal("partial terminal transition", task, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER block_queue_delete`); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTask(ctx, "atomic", "succeeded", `{}`); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM cube_task_queue WHERE task_id='atomic'`).Scan(&n)
	if n != 0 {
		t.Fatal("encrypted payload retained")
	}
	at, err := s.CubeTaskDispatchAt(ctx, "atomic")
	if err != nil || at.IsZero() {
		t.Fatal("dispatch metadata lost", err)
	}
}
func TestCancelOrdinaryTaskDoesNotRequireCubePolicy(t *testing.T) {
	s := openTestStore(t)
	if changed, err := s.CancelQueuedCubeTask(context.Background(), "missing", "missing", `{}`); err != nil || changed {
		t.Fatal(changed, err)
	}
}

// Builder VM reservations do not cap agents operating in existing runtime VMs.
func TestCodingConcurrencyIndependentOfTwoBuilderVMs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	cfg := resourceTestConfig(40000)
	cfg.ResourceBudget.RuntimeSlots = 50
	cfg.MaxActive = 52
	cfg.HostMemoryMB = 45056
	cfg.HostCPUMillis = 72000
	enrollBudget(t, s, cfg)
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("stable-%d", i)
		seedPausedBudgetRuntime(t, s, id, "small")
		if err := s.Create(ctx, &Sandbox{ID: id, Status: "stopped", RuntimeProvider: "cube", RuntimeBinding: &RuntimeBinding{Provider: "cube", RuntimeID: id, TemplateID: "small", Domain: "cube.test", TokenCiphertext: []byte("encrypted"), TokenNonce: []byte("nonce")}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 50; i++ {
		if err := s.EnqueueCubeTask(ctx, &Task{TaskID: fmt.Sprintf("task-%02d", i), SandboxID: fmt.Sprintf("stable-%d", i), Agent: "claude-code"}, []byte("ciphertext"), []byte("nonce")); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	claims := make(chan *CubeQueuedTask, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q, err := s.ClaimCubeTask(ctx, fmt.Sprintf("claim-%d", i), 50)
			if err != nil {
				t.Error(err)
			}
			if q != nil {
				claims <- q
			}
		}(i)
	}
	wg.Wait()
	close(claims)
	if len(claims) != 50 {
		t.Fatalf("accepted %d jobs; wanted 50 despite two builder VM slots", len(claims))
	}
}
