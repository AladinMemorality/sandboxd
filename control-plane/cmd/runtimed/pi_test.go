package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

func piTestInput(t *testing.T) *piInput {
	t.Helper()
	in := newPiInput()
	if err := in.attach(&inputBuffer{}, "Build it"); err != nil {
		t.Fatal(err)
	}
	return in
}

const piAccepted = `{"type":"response","id":"initial","command":"prompt","success":true,"data":{"disposition":"started"}}`
const piConsumed = `{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"Build it"}]}}`
const piFinal = `{"type":"message_end","message":{"role":"assistant","model":"test-model","stopReason":"stop","content":[{"type":"text","text":"Done."}],"usage":{"input":10,"output":4,"cacheRead":3,"cacheWrite":2,"cost":{"total":0.02}}}}`

func TestPiStreamRetryToolsAndUsage(t *testing.T) {
	in := piTestInput(t)
	sink, events := collectSink()
	stream := []string{
		piAccepted, `{"type":"agent_start"}`, piConsumed,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"temporary"}}`,
		`{"type":"agent_end"}`,
		// A successful RPC acknowledgement must not promote a retryable model error
		// to a fatal parser error.
		`{"type":"response","id":"state","command":"get_state","success":true}`,
		`{"type":"agent_start"}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"private-reasoning"}}`,
		`{"type":"tool_execution_start","toolCallId":"write-1","toolName":"write","args":{"path":"app/page.tsx","content":"private-source"}}`,
		`{"type":"tool_execution_end","toolCallId":"write-1","toolName":"write","durationMs":12,"isError":false,"result":{"content":[{"type":"text","text":"private-output"}]}}`,
		piFinal, `{"type":"agent_end"}`, `{"type":"agent_settled","aborted":false}`,
	}
	got := parsePiStream(strings.NewReader(strings.Join(stream, "\n")), sink, in)
	if got.err != nil || got.final != "Done." || !in.complete() {
		t.Fatalf("result: %+v", got)
	}
	if got.usage != (runtime.TokenUsage{Input: 10, Output: 4, CacheRead: 3, CacheWrite: 2, Total: 19, Cost: 0.02}) {
		t.Fatalf("usage: %+v", got.usage)
	}
	b, _ := json.Marshal(events)
	for _, private := range []string{"private-reasoning", "private-source", "private-output"} {
		if strings.Contains(string(b), private) {
			t.Fatalf("public events leaked %s", private)
		}
	}
	var tools int
	for _, ev := range *events {
		if ev.typ == runtime.EventTool {
			tools++
			d := ev.data.(map[string]any)
			if d["path"] != "app/page.tsx" {
				t.Fatalf("tool path lost: %v", d)
			}
		}
	}
	if tools != 2 {
		t.Fatalf("tool lifecycle events: %d", tools)
	}
}

func TestPiRejectsIncompleteAndFailedStreams(t *testing.T) {
	for name, tail := range map[string][]string{
		"agent_end is not settled": {piFinal, `{"type":"agent_end"}`},
		"aborted":                  {piFinal, `{"type":"agent_settled","aborted":true}`},
		"output limit":             {`{"type":"message_end","message":{"role":"assistant","stopReason":"length","content":[{"type":"text","text":"Partial answer"}]}}`, `{"type":"agent_settled"}`},
		"model failure":            {`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"upstream failed"}}`, `{"type":"agent_settled"}`},
		"RPC rejection":            {`{"type":"response","command":"prompt","success":false,"error":"rejected"}`},
		"handled without run":      {`{"type":"response","command":"prompt","success":true,"data":{"disposition":"handled"}}`},
		"malformed":                {`not-json`},
		"another run started":      {piFinal, `{"type":"agent_settled"}`, `{"type":"agent_start"}`},
	} {
		t.Run(name, func(t *testing.T) {
			in := piTestInput(t)
			sink, _ := collectSink()
			out := parsePiStream(strings.NewReader(strings.Join(append([]string{piAccepted, piConsumed}, tail...), "\n")), sink, in)
			if out.err == nil {
				t.Fatal("false success")
			}
		})
	}
}

func TestPiFollowupMustBeConsumedBeforeCompletion(t *testing.T) {
	in := piTestInput(t)
	req := inputMessage()
	if added, err := in.send(req); !added || err != nil {
		t.Fatalf("send: %v %v", added, err)
	}
	if added, err := in.send(req); added || err != nil {
		t.Fatalf("duplicate: %v %v", added, err)
	}
	changed := req
	changed.Prompt = "different"
	if _, err := in.send(changed); err == nil {
		t.Fatal("conflicting message ID accepted")
	}
	in.response("initial")
	in.consume("Build it")
	in.settle()
	if in.closed {
		t.Fatal("closed while follow-up pending")
	}
	in.response(req.MessageID)
	if in.closed {
		t.Fatal("RPC acceptance treated as consumption")
	}
	in.start()
	if id := in.consume(req.Prompt); id != req.MessageID {
		t.Fatalf("received id: %s", id)
	}
	if in.closed {
		t.Fatal("closed before follow-up settled")
	}
	in.settle()
	if !in.closed || !in.complete() {
		t.Fatal("completed input did not close")
	}
	if _, err := in.send(runtime.TaskMessage{MessageID: "new", Prompt: "late"}); !errors.Is(err, errAgentInputClosed) {
		t.Fatalf("late input: %v", err)
	}
	in.reopen()
	if in.closed || in.complete() {
		t.Fatal("continuation did not reset completion")
	}
	if added, err := in.send(req); added || err != nil {
		t.Fatal("continuation replayed consumed message")
	}
}

func TestPiEOFWithUnconsumedFollowupFails(t *testing.T) {
	in := piTestInput(t)
	_, _ = in.send(inputMessage())
	sink, _ := collectSink()
	out := parsePiStream(strings.NewReader(strings.Join([]string{piAccepted, piConsumed, piFinal, `{"type":"agent_settled"}`}, "\n")), sink, in)
	if out.err == nil {
		t.Fatal("unconsumed follow-up reported as success")
	}
}

func TestPiFollowupCompletionRace(t *testing.T) {
	for i := 0; i < 100; i++ {
		in := piTestInput(t)
		in.response("initial")
		in.consume("Build it")
		var wg sync.WaitGroup
		var added bool
		var err error
		wg.Add(2)
		go func() { defer wg.Done(); in.settle() }()
		go func() { defer wg.Done(); added, err = in.send(inputMessage()) }()
		wg.Wait()
		if added {
			if err != nil || in.closed || in.complete() {
				t.Fatal("accepted input was lost during completion")
			}
		} else if !errors.Is(err, errAgentInputClosed) || !in.complete() {
			t.Fatalf("completion must explicitly reject late input: %v", err)
		}
		in.close()
	}
}

func TestPiRelayConfigAndCredentialIsolation(t *testing.T) {
	t.Setenv("RUNTIMED_ANTHROPIC_PROXY", "http://global-proxy")
	t.Setenv("ANTHROPIC_API_KEY", "real-provider-secret")
	env := agentEnv("pi", map[string]string{
		"RUNTIMED_CUBE_AGENT_BASE_URL": "https://relay.test/task/anthropic",
		"RUNTIMED_CUBE_AGENT_TOKEN":    "scoped-token",
		"ANTHROPIC_BASE_URL":           "https://untrusted.test",
		"ANTHROPIC_AUTH_TOKEN":         "caller-token",
		"BRIDGE_TOKEN":                 "project-token",
	})
	m := envMap(env)
	if m["ANTHROPIC_BASE_URL"] != "https://relay.test/task/anthropic" || m["ANTHROPIC_API_KEY"] != "scoped-token" || m["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatal("trusted relay did not win")
	}
	for _, v := range env {
		if strings.Contains(v, "real-provider-secret") || strings.HasPrefix(v, "RUNTIMED_") {
			t.Fatal("inherited credential/control environment leaked")
		}
	}
	config, err := piConfig(env, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"scoped-token", "project-token"} {
		if strings.Contains(string(config), secret) {
			t.Fatal("config persisted a token")
		}
	}
	if !strings.Contains(string(config), "${BRIDGE_TOKEN}") {
		t.Fatal("missing metering header")
	}
	for _, url := range []string{"", "file:///tmp/model", "https://user:pass@relay.test", "https://relay.test?key=secret"} {
		if _, err := piConfig([]string{"ANTHROPIC_BASE_URL=" + url, "ANTHROPIC_API_KEY=test"}, "model"); err == nil {
			t.Fatalf("accepted invalid relay %q", url)
		}
	}
}
