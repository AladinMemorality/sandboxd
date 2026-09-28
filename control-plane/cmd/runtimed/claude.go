package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

// claudeCodeAgent drives the official Claude Code CLI:
//
//	claude -p <prompt> --output-format stream-json --verbose --dangerously-skip-permissions
//
// It runs in the sandbox with HOME pointed at the mounted agent-auth dir
// (/run/agent-home), so the CLI authenticates with the owner's imported Claude
// credentials. This is the claude-code provider's runner — it is NOT used for
// opencode, and claude credentials are only meaningful here.
type claudeCodeAgent struct{ log *slog.Logger }

func (c *claudeCodeAgent) name() string { return "claude-code" }

// claudeEvent is one line of `claude … --output-format stream-json`. Only the
// fields runtimed maps are declared; the rest is treated as opaque.
type claudeEvent struct {
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	} `json:"event"`
	UUID          string `json:"uuid"`
	Type          string `json:"type"`    // system | assistant | user | result
	Subtype       string `json:"subtype"` // on result: success | error_* …
	Model         string `json:"model"`   // on system/init: the RESOLVED model id
	Result        string `json:"result"`  // on result: the final assistant text
	IsError       bool   `json:"is_error"`
	ToolUseResult struct {
		ExitCode *int `json:"exitCode"`
	} `json:"tool_use_result"`
	Cost  float64 `json:"total_cost_usd"`
	Usage struct {
		Input       int `json:"input_tokens"`
		Output      int `json:"output_tokens"`
		CacheRead   int `json:"cache_read_input_tokens"`
		CacheCreate int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Message struct {
		Content []struct {
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Type      string          `json:"type"` // text | tool_use | tool_result
			Text      string          `json:"text"`
			Name      string          `json:"name"`  // tool name on tool_use
			Input     json.RawMessage `json:"input"` // tool args
		} `json:"content"`
	} `json:"message"`
	// Error is tolerated as string | object | null: real claude puts a bare
	// string here on the assistant turn (e.g. "authentication_failed"), so a
	// strongly-typed struct would fail the whole line's unmarshal.
	Error json.RawMessage `json:"error"`
}

// hasError reports whether a raw `error` field is present and non-null.
func hasError(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != `""`
}

// errString renders a raw `error` (string or {message:…} object) as a message.
func errString(raw json.RawMessage) string {
	if !hasError(raw) {
		return "claude error"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return "claude error"
}

type claudeParseResult struct {
	FinalMessage string
	Usage        runtime.TokenUsage
	SawText      bool
	SawTool      bool
	APIErr       string
}

// parseClaudeStream consumes NDJSON from r, dispatches canonical events, and
// returns a structured summary. Pure — unit-testable without spawning claude.
func parseClaudeStream(r io.Reader, emit eventSink) claudeParseResult {
	return parseClaudeStreamInput(r, emit, nil)
}

func parseClaudeStreamInput(r io.Reader, emit eventSink, input *claudeInput) claudeParseResult {
	var pr claudeParseResult
	var acc strings.Builder
	started := time.Now()
	type invocation struct {
		name, path string
		at         time.Time
	}
	pending := map[string]invocation{}
	seen := map[string]bool{}
	mark := func(name string) {
		if !seen[name] {
			seen[name] = true
			emit("timing", map[string]any{"stage": name, "elapsed_ms": time.Since(started).Milliseconds(), "origin": "stream_observation"})
		}
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		raw := sc.Bytes()
		// Replayed user messages can carry string content, whereas assistant
		// messages carry block arrays. Decode the acknowledgement envelope
		// independently so a valid replay cannot be dropped by block decoding.
		var envelope struct {
			Type string `json:"type"`
			UUID string `json:"uuid"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.Type == "user" {
			if input != nil && input.acknowledge(envelope.UUID) {
				emit("input", map[string]any{"message_id": envelope.UUID, "status": "received"})
			}
			// User envelopes also carry tool_result blocks. Only string-content
			// replay messages should fail block decoding below; do not drop results.
		}
		var ev claudeEvent
		if json.Unmarshal(raw, &ev) != nil {
			// Non-JSON line. claude prints "Not logged in · Please run /login"
			// (and exits 0) when unauthenticated — capture it as an error so
			// the task is classified as failed, not silently empty.
			s := strings.TrimSpace(string(raw))
			if pr.APIErr == "" && s != "" && (strings.Contains(s, "Not logged in") || strings.Contains(s, "Invalid API key")) {
				pr.APIErr = s
			}
			continue
		}
		switch ev.Type {
		case "stream_event":
			// Timestamp the first generated text/tool-argument delta without
			// emitting duplicate text, partial tools or private reasoning content.
			if ev.Event.Type == "content_block_delta" && ((ev.Event.Delta.Type == "text_delta" && ev.Event.Delta.Text != "") || (ev.Event.Delta.Type == "input_json_delta" && ev.Event.Delta.PartialJSON != "")) {
				mark("first_model_delta")
			}
		case "user":
			for _, blk := range ev.Message.Content {
				if blk.Type != "tool_result" || blk.ToolUseID == "" {
					continue
				}
				call, ok := pending[blk.ToolUseID]
				if !ok {
					continue
				} // Duplicate/replayed/unmatched results are not new work.
				delete(pending, blk.ToolUseID)
				status := "completed"
				if blk.IsError {
					status = "error"
				}
				data := map[string]any{"call_id": blk.ToolUseID, "name": call.name, "path": call.path,
					"status": status, "is_error": blk.IsError, "duration_ms": time.Since(call.at).Milliseconds(),
					"duration_basis": "stream_observation"}
				// Never forward tool stdout: it may contain credentials or tenant data.
				// An absent exit code remains unknown, rather than inventing zero.
				if ev.ToolUseResult.ExitCode != nil {
					data["exit_code"] = *ev.ToolUseResult.ExitCode
				}
				emit(runtime.EventTool, data)
			}
		case "system":
			// The init event reports the RESOLVED model (an alias like "sonnet"
			// becomes e.g. "claude-sonnet-5"). Surface it so the user sees which
			// model actually ran — the model's own "what model are you" answer is
			// unreliable (it reports its system-prompt identity).
			if ev.Subtype == "init" && ev.Model != "" {
				mark("cli_initialized")
				emit(runtime.EventStatus, map[string]any{"phase": "model", "model": ev.Model})
			}
		case "assistant":
			// An assistant turn carrying a top-level error (e.g. auth failure)
			// is NOT real output: capture its text as the failure reason and do
			// not emit it as a normal agent message.
			errTurn := hasError(ev.Error)
			for _, blk := range ev.Message.Content {
				switch blk.Type {
				case "text":
					if blk.Text == "" {
						continue
					}
					if errTurn {
						if pr.APIErr == "" {
							pr.APIErr = blk.Text
						}
						continue
					}
					pr.SawText = true
					mark("first_visible_message")
					acc.WriteString(blk.Text)
					emit(runtime.EventMessage, map[string]any{"role": "agent", "text": blk.Text})
				case "tool_use":
					if blk.Name != "" && !errTurn {
						pr.SawTool = true
						mark("first_tool")
						if blk.Name == "Edit" || blk.Name == "Write" || blk.Name == "MultiEdit" {
							mark("first_edit_invocation")
						}
						if blk.ID != "" {
							if seen["tool:"+blk.ID] {
								continue
							}
							seen["tool:"+blk.ID] = true
							pending[blk.ID] = invocation{blk.Name, toolTarget(blk.Input), time.Now()}
						}
						data := map[string]any{
							"name":   blk.Name,
							"status": "running",
							"path":   toolTarget(blk.Input),
						}
						if blk.ID != "" {
							data["call_id"] = blk.ID
						}
						emit(runtime.EventTool, data)
					}
				}
			}
		case "result":
			if input != nil {
				input.result(ev.IsError || (ev.Subtype != "" && ev.Subtype != "success"))
			}
			if ev.Result != "" {
				pr.FinalMessage = ev.Result
			}
			pr.Usage.Input += ev.Usage.Input
			pr.Usage.Output += ev.Usage.Output
			pr.Usage.CacheRead += ev.Usage.CacheRead
			pr.Usage.CacheWrite += ev.Usage.CacheCreate
			pr.Usage.Cost += ev.Cost
			if ev.IsError || (ev.Subtype != "" && ev.Subtype != "success") {
				if pr.APIErr == "" {
					switch {
					case ev.Result != "":
						pr.APIErr = ev.Result
					case ev.Subtype != "":
						pr.APIErr = ev.Subtype
					default:
						pr.APIErr = "claude reported an error"
					}
				}
			}
		case "error":
			if pr.APIErr == "" {
				pr.APIErr = errString(ev.Error)
			}
		}
	}
	if pr.FinalMessage == "" {
		pr.FinalMessage = acc.String()
	}
	pr.Usage.Total = pr.Usage.Input + pr.Usage.Output + pr.Usage.Reasoning +
		pr.Usage.CacheRead + pr.Usage.CacheWrite
	return pr
}

func (c *claudeCodeAgent) run(ctx context.Context, spec agentSpec, emit eventSink) (string, runtime.TokenUsage, error) {
	var usage runtime.TokenUsage
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--dangerously-skip-permissions"}
	if spec.input != nil {
		args = append(args, "--input-format", "stream-json", "--replay-user-messages")
		defer spec.input.close()
	}
	// Per-task model (claude accepts an alias like "sonnet"/"opus" or a full id).
	if spec.model != "" {
		args = append(args, "--model", spec.model)
	}
	if spec.cont {
		args = append(args, "--continue") // continue the most recent conversation in this workspace
	}
	// Platform briefing → appended to claude's default system prompt (out of the
	// workspace, so it can't be committed or edited by the agent). It's a system
	// prompt append (not conversation), so it's safe to include on --continue too.
	if spec.systemPrompt != "" {
		args = append(args, "--append-system-prompt", spec.systemPrompt)
	}
	if spec.input == nil {
		args = append(args, spec.prompt)
	}
	cmd := exec.Command("claude", args...)
	cmd.Dir = spec.workDir
	// Scrub secret-shaped vars and point HOME at THIS agent's mounted auth dir
	// (/run/agent-auth/claude-code = the imported Claude creds), keyed on the
	// agent name so it works even when the sandbox default is opencode.
	cmd.Env = agentEnv(c.name(), spec.env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdin io.WriteCloser
	if spec.input != nil {
		var err error
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return "", usage, err
		}
		defer stdin.Close()
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", usage, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", usage, err
	}
	spawnStarted := time.Now()
	if err := cmd.Start(); err != nil {
		return "", usage, fmt.Errorf("start claude: %w", err)
	}
	emit("timing", map[string]any{"stage": "process_started", "elapsed_ms": time.Since(spawnStarted).Milliseconds(), "origin": "process_spawn"})
	pgid := cmd.Process.Pid

	finished := make(chan struct{})
	go func() {
		select {
		case <-finished:
		case <-ctx.Done():
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			t := time.NewTimer(5 * time.Second)
			defer t.Stop()
			select {
			case <-finished:
			case <-t.C:
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}
	}()
	defer close(finished)
	if spec.input != nil {
		if err := spec.input.attach(stdin, spec.prompt); err != nil {
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			_ = cmd.Wait()
			return "", usage, fmt.Errorf("start live input: %w", err)
		}
	}

	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(spec.rawLog, stderr)
		close(stderrDone)
	}()

	pr := parseClaudeStreamInput(teeStream(stdout, spec.streamLog), emit, spec.input)
	waitErr := cmd.Wait()
	<-stderrDone

	switch {
	case ctx.Err() != nil:
		return pr.FinalMessage, pr.Usage, nil
	case pr.APIErr != "":
		return pr.FinalMessage, pr.Usage, fmt.Errorf("agent error: %s", pr.APIErr)
	case waitErr != nil:
		return pr.FinalMessage, pr.Usage, fmt.Errorf("claude exited: %w", waitErr)
	case !pr.SawText && !pr.SawTool && pr.FinalMessage == "":
		return pr.FinalMessage, pr.Usage,
			fmt.Errorf("agent produced no output (claude exited with zero events)")
	}
	return pr.FinalMessage, pr.Usage, nil
}
