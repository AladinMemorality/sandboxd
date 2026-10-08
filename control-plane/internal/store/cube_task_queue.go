package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrTaskQueueFull = errors.New("worker coding queue is full")

type CubeQueuedTask struct {
	TaskID, SandboxID, WorkerID, Token string
	Ciphertext, Nonce                  []byte
	Attempts                           int
}

func (s *Store) CubeTaskQueueCapacity(ctx context.Context, sid string) (int, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT c.contract FROM runtime_binding b JOIN cube_admission a ON a.runtime_id=b.runtime_id JOIN cube_resource_budget c ON c.worker_id=a.worker_id WHERE b.sandbox_id=? AND a.state<>'deleted'`, sid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var c resourceContract
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return 0, err
	}
	return c.Budget.RuntimeSlots, nil
}
func (s *Store) EnqueueCubeTask(ctx context.Context, t *Task, ciphertext, nonce []byte) error {
	if len(ciphertext) == 0 || len(nonce) == 0 {
		return errors.New("encrypted dispatch request required")
	}
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		var worker string
		if err := tx.QueryRowContext(ctx, `SELECT a.worker_id FROM runtime_binding b JOIN cube_admission a ON a.runtime_id=b.runtime_id WHERE b.sandbox_id=? AND a.state<>'deleted' AND NOT EXISTS(SELECT 1 FROM cube_runtime_quarantine q WHERE q.runtime_id=b.runtime_id)`, t.SandboxID).Scan(&worker); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task WHERE sandbox_id=? AND status IN ('running','queued')`, t.SandboxID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrConflict
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM cube_task_queue WHERE worker_id=?`, worker).Scan(&n); err != nil {
			return err
		}
		if n >= 100 {
			return ErrTaskQueueFull
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task(task_id,sandbox_id,external_user_id,external_project_id,agent,prompt,status,timeout_s,created_at) VALUES(?,?,?,?,?,?,'queued',?,?)`, t.TaskID, t.SandboxID, t.ExternalUserID, t.ExternalProjectID, t.Agent, t.Prompt, t.TimeoutS, time.Now().Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_task_queue_history(task_id) VALUES(?)`, t.TaskID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO cube_task_queue(task_id,worker_id,ciphertext,nonce,state) VALUES(?,?,?,?,'queued')`, t.TaskID, worker, ciphertext, nonce)
		return err
	})
}

// Only pre-dispatch claims are restartable. A lost response after the dispatch
// checkpoint stays charged until authenticated guest reconciliation completes.
func (s *Store) ResetPreparingCubeTasks(ctx context.Context) error {
	return s.submit(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, `UPDATE cube_task_queue SET state='queued',claim_token='' WHERE state='preparing'`)
		return err
	})
}
func (s *Store) ClaimCubeTask(ctx context.Context, token string, limit int) (*CubeQueuedTask, error) {
	if token == "" || limit < 1 || limit > 1000 {
		return nil, errors.New("claim token and valid coding concurrency required")
	}
	var out *CubeQueuedTask
	err := s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT q.task_id,t.sandbox_id,q.worker_id,q.ciphertext,q.nonce,q.attempts,c.contract FROM cube_task_queue q JOIN task t ON t.task_id=q.task_id JOIN cube_resource_budget c ON c.worker_id=q.worker_id WHERE q.state='queued' AND t.status='queued' AND q.next_try<=? ORDER BY q.task_id`, time.Now().Unix())
		if err != nil {
			return err
		}
		type candidate struct {
			task   CubeQueuedTask
			budget resourceContract
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			var raw string
			if err = rows.Scan(&c.task.TaskID, &c.task.SandboxID, &c.task.WorkerID, &c.task.Ciphertext, &c.task.Nonce, &c.task.Attempts, &raw); err != nil {
				rows.Close()
				return err
			}
			if err = json.Unmarshal([]byte(raw), &c.budget); err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, c := range candidates {
			var used int
			if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM task t JOIN runtime_binding b ON b.sandbox_id=t.sandbox_id JOIN cube_admission a ON a.runtime_id=b.runtime_id WHERE t.status='running' AND a.worker_id=? AND a.state<>'deleted')+(SELECT count(*) FROM cube_task_queue q JOIN task t ON t.task_id=q.task_id WHERE q.worker_id=? AND q.state='preparing' AND t.status='queued')`, c.task.WorkerID, c.task.WorkerID).Scan(&used); err != nil {
				return err
			}
			workerLimit := limit
			if c.budget.Budget.RuntimeSlots < workerLimit {
				workerLimit = c.budget.Budget.RuntimeSlots
			}
			if workerLimit < 1 || used >= workerLimit {
				continue
			}
			if _, err = tx.ExecContext(ctx, `UPDATE cube_task_queue SET state='preparing',claim_token=?,attempts=attempts+1 WHERE task_id=? AND state='queued'`, token, c.task.TaskID); err != nil {
				return err
			}
			c.task.Token = token
			c.task.Attempts++
			out = &c.task
			return nil
		}
		return nil
	})
	return out, err
}
func (s *Store) BeginCubeTaskDispatch(ctx context.Context, t CubeQueuedTask) error {
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE cube_task_queue SET state='dispatched',dispatched_at=? WHERE task_id=? AND state='preparing' AND claim_token=? AND EXISTS(SELECT 1 FROM task WHERE task_id=? AND status='queued')`, time.Now().Unix(), t.TaskID, t.Token, t.TaskID)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE cube_task_queue_history SET dispatched_at=? WHERE task_id=?`, time.Now().Unix(), t.TaskID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE task SET status='running' WHERE task_id=? AND status='queued'`, t.TaskID)
		return err
	})
}
func (s *Store) RetryCubeTaskPreparation(ctx context.Context, t CubeQueuedTask) error {
	return s.submit(ctx, func(db *sql.DB) error {
		r, err := db.ExecContext(ctx, `UPDATE cube_task_queue SET state='queued',claim_token='',next_try=? WHERE task_id=? AND state='preparing' AND claim_token=?`, time.Now().Add(10*time.Second).Unix(), t.TaskID, t.Token)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (s *Store) CancelQueuedCubeTask(ctx context.Context, sid, id, result string) (bool, error) {
	var waiting int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM task WHERE task_id=? AND sandbox_id=? AND status='queued'`, id, sid).Scan(&waiting); err != nil {
		return false, err
	}
	if waiting == 0 {
		return false, nil
	}

	changed := false
	err := s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE task SET status='cancelled',result_json=?,finished_at=? WHERE task_id=? AND sandbox_id=? AND status='queued'`, result, time.Now().Unix(), id, sid)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		changed = n == 1
		if changed {
			if _, err = tx.ExecContext(ctx, `UPDATE cube_task_queue_history SET terminal_only=1 WHERE task_id=?`, id); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `DELETE FROM cube_task_queue WHERE task_id=?`, id)
		}
		return err
	})
	return changed, err
}
func (s *Store) CubeTaskDispatchAt(ctx context.Context, id string) (time.Time, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT dispatched_at FROM cube_task_queue_history WHERE task_id=?`, id).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return time.Unix(at, 0), err
}

// FinishUndispatchedCubeTask fences retries with the claim token. There is no
// remote task to reconcile or cancel, so only its canonical terminal event exists.
func (s *Store) FinishUndispatchedCubeTask(ctx context.Context, t CubeQueuedTask, result string) error {
	return s.recoveryWrite(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE task SET status='failed',result_json=?,finished_at=? WHERE task_id=? AND status='queued' AND EXISTS(SELECT 1 FROM cube_task_queue q WHERE q.task_id=task.task_id AND q.state='preparing' AND q.claim_token=?)`, result, time.Now().Unix(), t.TaskID, t.Token)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE cube_task_queue_history SET terminal_only=1 WHERE task_id=?`, t.TaskID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM cube_task_queue WHERE task_id=?`, t.TaskID)
		return err
	})
}

func (s *Store) CubeTaskTerminalOnly(ctx context.Context, id string) (bool, error) {
	var value int
	err := s.db.QueryRowContext(ctx, `SELECT terminal_only FROM cube_task_queue_history WHERE task_id=?`, id).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return value == 1, err
}
