package main

import (
	"context"
	"errors"
	"io"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

var errAgentInputClosed = errors.New("the agent has finished accepting messages")

// agentInput is the supervisor's live-message contract. Each adapter owns its
// wire protocol and acknowledgements; the task scheduler is provider-neutral.
type agentInput interface {
	send(runtime.TaskMessage) (bool, error)
	close()
	reopen()
}

type agentSpec struct {
	input                  agentInput
	workDir, prompt, model string
	env                    map[string]string
	rawLog, streamLog      io.Writer
	systemPrompt           string
	sessionDir             string
	cont                   bool
}

type agent interface {
	name() string
	run(context.Context, agentSpec, eventSink) (string, runtime.TokenUsage, error)
}
