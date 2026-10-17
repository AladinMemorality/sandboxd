package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

type piAgent struct{ log *slog.Logger }

func (*piAgent) name() string { return "pi" }

type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Model        string          `json:"model"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Usage        struct {
		Input, Output, CacheRead, CacheWrite int
		Cost                                 struct{ Total float64 }
	} `json:"usage"`
}

func piText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

type piResult struct {
	final   string
	usage   runtime.TokenUsage
	settled bool
	err     error
}

func parsePiStream(r io.Reader, emit eventSink, input *piInput) piResult {
	var out piResult
	started := time.Now()
	seen := map[string]bool{}
	paths := map[string]string{}
	mark := func(stage string) {
		if !seen[stage] {
			seen[stage] = true
			emit("timing", map[string]any{"stage": stage, "elapsed_ms": time.Since(started).Milliseconds(), "origin": "stream_observation"})
		}
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var ev struct {
			Type, ID, Command, ToolCallID, ToolName string
			Success                                 bool
			Error                                   string
			Aborted, IsError                        bool
			DurationMs                              *float64
			Args                                    json.RawMessage
			Message                                 piMessage
			Data                                    struct{ Disposition string }
			AssistantMessageEvent                   struct{ Type, Delta string }
		}
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			out.err = errors.New("invalid Pi RPC record")
			break
		}
		fatal := false
		switch ev.Type {
		case "response":
			if !ev.Success {
				out.err = fmt.Errorf("Pi %s command failed: %s", ev.Command, ev.Error)
				fatal = true
				break
			}
			if ev.Command == "prompt" {
				if ev.Data.Disposition != "started" && ev.Data.Disposition != "queued" {
					out.err = errors.New("Pi did not start or queue the requested task")
					fatal = true
					break
				}
				input.response(ev.ID)
			}
		case "agent_start":
			out.settled = false
			input.start()
			mark("cli_initialized")
		case "message_start":
			if ev.Message.Role == "user" {
				if id := input.consume(piText(ev.Message.Content)); id != "" {
					emit("input", map[string]any{"message_id": id, "status": "received"})
				}
			}
		case "message_update":
			if ev.AssistantMessageEvent.Type == "text_delta" || ev.AssistantMessageEvent.Type == "toolcall_delta" {
				mark("first_model_delta")
			}
		case "message_end":
			if ev.Message.Role != "assistant" {
				continue
			}
			m := ev.Message
			out.usage.Input += m.Usage.Input
			out.usage.Output += m.Usage.Output
			out.usage.CacheRead += m.Usage.CacheRead
			out.usage.CacheWrite += m.Usage.CacheWrite
			out.usage.Cost += m.Usage.Cost.Total
			if m.StopReason == "error" || m.StopReason == "aborted" || m.StopReason == "length" {
				// A later successful retry replaces this error. agent_end alone
				// is not completion and must not end the stream.
				out.err = fmt.Errorf("Pi model response %s: %s", m.StopReason, m.ErrorMessage)
				continue
			}
			out.err = nil
			if m.Model != "" && !seen["model"] {
				seen["model"] = true
				emit(runtime.EventStatus, map[string]any{"phase": "model", "model": m.Model})
			}
			out.final = piText(m.Content)
			if out.final != "" {
				mark("first_visible_message")
				emit(runtime.EventMessage, map[string]any{"role": "agent", "text": out.final})
			}
		case "tool_execution_start":
			if seen["tool:"+ev.ToolCallID] {
				continue
			}
			seen["tool:"+ev.ToolCallID] = true
			mark("first_tool")
			if ev.ToolName == "edit" || ev.ToolName == "write" {
				mark("first_edit_invocation")
			}
			paths[ev.ToolCallID] = toolTarget(ev.Args)
			emit(runtime.EventTool, map[string]any{"call_id": ev.ToolCallID, "name": ev.ToolName, "path": paths[ev.ToolCallID], "status": "running"})
		case "tool_execution_end":
			path, exists := paths[ev.ToolCallID]
			if !exists {
				continue
			}
			delete(paths, ev.ToolCallID)
			status := "completed"
			if ev.IsError {
				status = "error"
			}
			data := map[string]any{"call_id": ev.ToolCallID, "name": ev.ToolName, "path": path, "status": status, "is_error": ev.IsError}
			if ev.DurationMs != nil {
				data["duration_ms"] = *ev.DurationMs
				data["duration_basis"] = "agent_reported"
			}
			// Tool output and private reasoning stay out of user-facing events.
			emit(runtime.EventTool, data)
		case "agent_settled":
			out.settled = true
			if ev.Aborted {
				out.err = errors.New("Pi run aborted")
			}
			input.settle()
		case "extension_error":
			out.err = errors.New("Pi extension failed")
			fatal = true
		}
		if fatal {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		out.err = fmt.Errorf("read Pi RPC stream: %w", err)
	}
	if !out.settled && out.err == nil {
		out.err = errors.New("Pi exited before agent_settled")
	}
	if !input.complete() && out.err == nil {
		out.err = errors.New("Pi exited with unconsumed task input")
	}
	if out.final == "" && out.err == nil {
		out.err = errors.New("Pi produced no final response")
	}
	out.usage.Total = out.usage.Input + out.usage.Output + out.usage.CacheRead + out.usage.CacheWrite
	return out
}

// The custom provider uses the existing Anthropic-compatible, task-scoped relay.
// Config stores env references, never a provider key or a bridge token.
func piConfig(env []string, model string) ([]byte, error) {
	values := map[string]string{}
	for _, value := range env {
		k, v, _ := strings.Cut(value, "=")
		values[k] = v
	}
	u, err := url.Parse(values["ANTHROPIC_BASE_URL"])
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || values["ANTHROPIC_API_KEY"] == "" {
		return nil, errors.New("Pi requires the configured model relay")
	}
	if model == "" {
		return nil, errors.New("Pi requires an explicit model ID")
	}
	provider := map[string]any{"baseUrl": strings.TrimRight(u.String(), "/"), "api": "anthropic-messages", "apiKey": "${ANTHROPIC_API_KEY}",
		"models": []any{map[string]any{"id": model, "name": model, "reasoning": false, "input": []string{"text", "image"}, "contextWindow": 200000, "maxTokens": 8192}}}
	if values["BRIDGE_TOKEN"] != "" {
		provider["headers"] = map[string]string{"x-baarcha-bridge": "${BRIDGE_TOKEN}"}
	}
	return json.Marshal(map[string]any{"providers": map[string]any{"baarcha": provider}})
}

func (p *piAgent) run(ctx context.Context, spec agentSpec, emit eventSink) (string, runtime.TokenUsage, error) {
	var empty runtime.TokenUsage
	input, _ := spec.input.(*piInput)
	if input == nil {
		input = newPiInput()
	}
	defer input.close()
	env := agentEnv("pi", spec.env)
	config, err := piConfig(env, spec.model)
	if err != nil {
		return "", empty, err
	}
	root, err := os.MkdirTemp("", "baarcha-pi-")
	if err != nil {
		return "", empty, err
	}
	defer os.RemoveAll(root)
	if err = os.WriteFile(filepath.Join(root, "models.json"), config, 0600); err != nil {
		return "", empty, err
	}
	// Disable automatic discovery/updates: the platform explicitly supplies
	// resources and instructions. User project files remain readable by tools.
	settings := []byte(`{"compaction":{"enabled":true},"retry":{"enabled":true,"maxRetries":2},"cacheWarming":{"enabled":false}}`)
	if err = os.WriteFile(filepath.Join(root, "settings.json"), settings, 0600); err != nil {
		return "", empty, err
	}
	env = replaceEnv(env, "PI_CODING_AGENT_DIR", root)
	env = replaceEnv(env, "PI_TELEMETRY", "0")
	args := []string{"--mode", "rpc", "--offline", "--provider", "baarcha", "--model", spec.model, "--thinking", "off", "--no-approve", "--no-extensions", "--no-mcp", "--no-skills", "--no-prompt-templates", "--no-themes"}
	if spec.sessionDir != "" {
		if err = os.MkdirAll(spec.sessionDir, 0700); err != nil {
			return "", empty, err
		}
		args = append(args, "--session-dir", spec.sessionDir)
		if spec.cont {
			args = append(args, "--continue")
		}
	} else {
		args = append(args, "--no-session")
	}
	if spec.systemPrompt != "" {
		args = append(args, "--append-system-prompt", spec.systemPrompt)
	}
	cmd := exec.Command("pi", args...)
	cmd.Dir = spec.workDir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", empty, err
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", empty, err
	}
	if spec.rawLog == nil {
		spec.rawLog = io.Discard
	}
	cmd.Stderr = spec.rawLog
	started := time.Now()
	if err = cmd.Start(); err != nil {
		return "", empty, fmt.Errorf("start Pi: %w", err)
	}
	emit("timing", map[string]any{"stage": "process_started", "elapsed_ms": time.Since(started).Milliseconds(), "origin": "process_spawn"})
	finished := make(chan struct{})
	defer close(finished)
	inputDone := input.done
	go func() {
		select {
		case <-finished:
			return
		case <-ctx.Done():
		case <-inputDone:
			select {
			case <-finished:
				return
			case <-time.After(5 * time.Second):
			}
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()
	if err = input.attach(stdin, spec.prompt); err != nil {
		input.close()
		_ = cmd.Wait()
		return "", empty, err
	}
	out := parsePiStream(teeStream(stdout, spec.streamLog), emit, input)
	input.close()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return out.final, out.usage, nil
	}
	if out.err != nil {
		return out.final, out.usage, out.err
	}
	if waitErr != nil {
		return out.final, out.usage, fmt.Errorf("Pi exited: %w", waitErr)
	}
	return out.final, out.usage, nil
}

func replaceEnv(env []string, name, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, name+"=") {
			out = append(out, item)
		}
	}
	return append(out, name+"="+value)
}
