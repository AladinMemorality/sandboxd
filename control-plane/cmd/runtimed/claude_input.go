package main

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

var errAgentInputClosed = errors.New("the agent has finished accepting messages")

// claudeInput feeds the CLI's native streaming input. The CLI folds messages
// received mid-turn into the next tool result, preserving its own transcript.
// Its replay acknowledgement distinguishes accepted input from consumed input.
type claudeInput struct {
	mu          sync.Mutex
	w           io.WriteCloser
	closed      bool
	accepted    map[string]string
	pending     map[string]bool
	beforeStart []runtime.TaskMessage
}

func newClaudeInput() *claudeInput {
	return &claudeInput{accepted: map[string]string{}, pending: map[string]bool{}}
}

func (in *claudeInput) write(req runtime.TaskMessage) error {
	if w, ok := in.w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = w.SetWriteDeadline(time.Now().Add(3 * time.Second))
		defer w.SetWriteDeadline(time.Time{})
	}
	return json.NewEncoder(in.w).Encode(map[string]any{
		"type": "user", "uuid": req.MessageID, "session_id": "", "parent_tool_use_id": nil,
		"message": map[string]string{"role": "user", "content": req.Prompt},
	})
}

func (in *claudeInput) attach(w io.WriteCloser, prompt string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return errAgentInputClosed
	}
	in.w = w
	// The initial prompt has no client message id to acknowledge.
	if err := json.NewEncoder(w).Encode(map[string]any{
		"type": "user", "session_id": "", "parent_tool_use_id": nil,
		"message": map[string]string{"role": "user", "content": prompt},
	}); err != nil {
		return err
	}
	for _, req := range in.beforeStart {
		if err := in.write(req); err != nil {
			return err
		}
	}
	in.beforeStart = nil
	return nil
}

func (in *claudeInput) send(req runtime.TaskMessage) (bool, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if previous, ok := in.accepted[req.MessageID]; ok {
		if previous != req.Prompt {
			return false, errors.New("message_id already used for different input")
		}
		return false, nil
	}
	if in.closed {
		return false, errAgentInputClosed
	}
	if len(in.accepted) >= 100 {
		return false, errors.New("too many messages in this task")
	}
	if in.w != nil {
		if err := in.write(req); err != nil {
			return false, err
		}
	} else {
		in.beforeStart = append(in.beforeStart, req)
	}
	in.accepted[req.MessageID] = req.Prompt
	in.pending[req.MessageID] = true
	return true, nil
}

func (in *claudeInput) acknowledge(id string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.pending[id] {
		return false
	}
	delete(in.pending, id)
	return true
}

// Serialize completion with send: either input is accepted before the final
// result and drained by the CLI, or the caller gets an explicit conflict.
func (in *claudeInput) result(failed bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if failed || len(in.pending) == 0 {
		in.closed = true
		if in.w != nil {
			_ = in.w.Close()
		}
	}
}

func (in *claudeInput) close() { in.result(true) }

func (in *claudeInput) reopen() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.w = nil
	in.closed = false
}
