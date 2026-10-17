package main

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

type piPending struct {
	id, prompt          string
	responded, consumed bool
}

// RPC acceptance is not model consumption. Keep input pending until both the
// command response and the corresponding user message appear in Pi's stream.
type piInput struct {
	mu              sync.Mutex
	w               io.WriteCloser
	closed, settled bool
	accepted        map[string]string
	pending         []*piPending
	done            chan struct{}
}

func newPiInput() *piInput { return &piInput{accepted: map[string]string{}, done: make(chan struct{})} }

func (in *piInput) write(p *piPending) error {
	if w, ok := in.w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = w.SetWriteDeadline(time.Now().Add(3 * time.Second))
		defer w.SetWriteDeadline(time.Time{})
	}
	return json.NewEncoder(in.w).Encode(map[string]any{"id": p.id, "type": "prompt", "message": p.prompt, "streamingBehavior": "steer"})
}

func (in *piInput) attach(w io.WriteCloser, prompt string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return errAgentInputClosed
	}
	in.w = w
	in.pending = append([]*piPending{{id: "initial", prompt: prompt}}, in.pending...)
	for _, p := range in.pending {
		if err := in.write(p); err != nil {
			return err
		}
	}
	return nil
}

func (in *piInput) send(req runtime.TaskMessage) (bool, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if old, ok := in.accepted[req.MessageID]; ok {
		if old != req.Prompt {
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
	p := &piPending{id: req.MessageID, prompt: req.Prompt}
	if in.w != nil {
		if err := in.write(p); err != nil {
			return false, err
		}
	}
	in.accepted[p.id] = p.prompt
	in.pending = append(in.pending, p)
	in.settled = false
	return true, nil
}

func (in *piInput) response(id string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, p := range in.pending {
		if p.id == id {
			p.responded = true
			break
		}
	}
	in.finishLocked()
}

func (in *piInput) consume(text string) string {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, p := range in.pending {
		if !p.consumed && p.prompt == text {
			p.consumed = true
			if p.id != "initial" {
				return p.id
			}
			break
		}
	}
	return ""
}

func (in *piInput) settle() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.settled = true
	in.finishLocked()
}

func (in *piInput) finishLocked() {
	if !in.settled {
		return
	}
	for _, p := range in.pending {
		if !p.responded || !p.consumed {
			return
		}
	}
	in.closeLocked()
}

func (in *piInput) closeLocked() {
	if in.closed {
		return
	}
	in.closed = true
	if in.w != nil {
		_ = in.w.Close()
	}
	close(in.done)
}
func (in *piInput) close() { in.mu.Lock(); defer in.mu.Unlock(); in.closeLocked() }
func (in *piInput) reopen() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.w = nil
	in.closed = false
	in.settled = false
	in.pending = nil
	in.done = make(chan struct{})
}

func (in *piInput) start() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.settled = false
}
func (in *piInput) complete() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.settled {
		return false
	}
	for _, p := range in.pending {
		if !p.responded || !p.consumed {
			return false
		}
	}
	return true
}
