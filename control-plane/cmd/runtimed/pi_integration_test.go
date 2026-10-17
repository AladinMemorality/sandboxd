package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)

// Opt-in runs the pinned, real Pi package against a local Anthropic-protocol
// fixture. No upstream credential, paid model call or tenant data is used.
func requirePi(t *testing.T) {
	t.Helper()
	if os.Getenv("BAARCHA_TEST_PI") != "1" {
		t.Skip("set BAARCHA_TEST_PI=1 with the pinned Pi binary on PATH")
	}
	if _, err := exec.LookPath("pi"); err != nil {
		t.Fatal(err)
	}
}

func piSSE(w http.ResponseWriter, tool bool) {
	name, input := "", ""
	if tool {
		name, input = "write", `{"path":"result.txt","content":"Pi wrote this file.\n"}`
	}
	piSSEMessage(w, name, input)
}

func piSSEMessage(w http.ResponseWriter, toolName, input string) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(kind string, data map[string]any) {
		data["type"] = kind
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
	}
	send("message_start", map[string]any{"message": map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "model": "fixture-model", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}})
	stop := "end_turn"
	if toolName != "" {
		send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "tool_use", "id": "tool_write_1", "name": toolName, "input": map[string]any{}}})
		send("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": input}})
		stop = "tool_use"
	} else {
		send("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		send("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "Created result.txt and applied your follow-up."}})
	}
	send("content_block_stop", map[string]any{"index": 0})
	send("message_delta", map[string]any{"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 5}})
	send("message_stop", map[string]any{})
}

func TestPiCLIIntegrationWriteFollowupAndContinue(t *testing.T) {
	requirePi(t)
	in := newPiInput()
	var requests atomic.Int32
	var sawFollowup, sawSystem atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected model path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("x-api-key") != "scoped-fixture" || r.Header.Get("x-baarcha-bridge") != "bridge-fixture" {
			t.Error("relay or billing identity lost")
		}
		b, _ := io.ReadAll(r.Body)
		if bytes.Contains(b, []byte("PLATFORM_CONTEXT_SENTINEL")) {
			sawSystem.Store(true)
		}
		if bytes.Contains(b, []byte("Use blue instead")) {
			sawFollowup.Store(true)
		}
		n := requests.Add(1)
		if n == 1 {
			if _, err := in.send(inputMessage()); err != nil {
				t.Errorf("follow-up: %v", err)
			}
		}
		piSSE(w, n == 1)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	sessions := t.TempDir()
	spec := agentSpec{input: in, workDir: dir, prompt: "Write result.txt", model: "fixture-model", systemPrompt: "PLATFORM_CONTEXT_SENTINEL", sessionDir: sessions,
		env: map[string]string{"RUNTIMED_CUBE_AGENT_BASE_URL": server.URL, "RUNTIMED_CUBE_AGENT_TOKEN": "scoped-fixture", "BRIDGE_TOKEN": "bridge-fixture"}}
	var raw bytes.Buffer
	spec.rawLog = &raw
	sink, events := collectSink()
	final, usage, err := (&piAgent{}).run(ctx, spec, sink)
	if err != nil || ctx.Err() != nil {
		t.Fatalf("run: %v context=%v stderr=%s", err, ctx.Err(), raw.String())
	}
	if !strings.Contains(final, "Created result.txt") || usage.Input != 20 || usage.Output != 10 {
		t.Fatalf("result=%q usage=%+v", final, usage)
	}
	if !sawFollowup.Load() || !sawSystem.Load() {
		t.Fatal("follow-up or platform context missing from model request")
	}
	b, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	if err != nil || string(b) != "Pi wrote this file.\n" {
		t.Fatalf("tool result %q %v", b, err)
	}
	received := false
	for _, ev := range *events {
		if ev.typ == "input" && ev.data.(map[string]any)["status"] == "received" {
			received = true
		}
	}
	if !received {
		t.Fatal("missing follow-up consumption event")
	}
	// A second process resumes the private session rather than replaying a new
	// task into an arbitrary workspace-local Pi configuration.
	spec.input = newPiInput()
	spec.cont = true
	spec.prompt = "Summarize the result"
	_, _, err = (&piAgent{}).run(ctx, spec, func(string, any) {})
	if err != nil || ctx.Err() != nil {
		t.Fatalf("continuation: %v context=%v stderr=%s", err, ctx.Err(), raw.String())
	}
	if requests.Load() != 3 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestPiCLIIntegrationCancellation(t *testing.T) {
	requirePi(t)
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-started:
			cancel()
		case <-time.After(15 * time.Second):
			cancel()
		}
	}()
	begin := time.Now()
	_, _, _ = (&piAgent{}).run(ctx, agentSpec{workDir: t.TempDir(), prompt: "wait", model: "fixture-model", env: map[string]string{
		"RUNTIMED_CUBE_AGENT_BASE_URL": server.URL, "RUNTIMED_CUBE_AGENT_TOKEN": "scoped-fixture",
	}}, func(string, any) {})
	if ctx.Err() == nil {
		t.Fatal("agent ended before cancellation")
	}
	if time.Since(begin) > 12*time.Second {
		t.Fatal("cancellation did not terminate Pi promptly")
	}
}

func TestPiCLIIntegrationSupervisedTask(t *testing.T) {
	requirePi(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		piSSE(w, requests.Add(1) == 1)
	}))
	defer server.Close()
	build := "test -s result.txt"
	a := &app{appDir: t.TempDir(), runtimeDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil)), build: &BuildSpec{Command: &build, TimeoutSeconds: 5}}
	task, err := newTask(runtime.StartTaskRequest{TaskID: "pi-supervised", Agent: "pi", Model: "fixture-model", Prompt: "Write result.txt", TimeoutS: 20,
		Env: map[string]string{"RUNTIMED_CUBE_AGENT_BASE_URL": server.URL, "RUNTIMED_CUBE_AGENT_TOKEN": "scoped-fixture"}}, filepath.Join(a.runtimeDir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	a.runTask(task)
	b, err := os.ReadFile(filepath.Join(task.dir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result runtime.TaskResult
	if err = json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != runtime.TaskSucceeded || result.BuildStatus != runtime.BuildPassed || result.Tokens.Total != 30 || result.CheckpointID == "" {
		t.Fatalf("supervised result: %+v", result)
	}
	found := false
	for _, file := range result.FilesChanged {
		if file == "result.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed file absent from result: %v", result.FilesChanged)
	}
}

func TestPiCLIIntegrationBash(t *testing.T) {
	requirePi(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if requests.Add(1) == 1 {
			piSSEMessage(w, "bash", `{"command":"printf pi-shell-ok | tee shell.txt"}`)
		} else {
			piSSE(w, false)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := t.TempDir()
	_, _, err := (&piAgent{}).run(ctx, agentSpec{workDir: dir, prompt: "Run the shell check", model: "fixture-model", env: map[string]string{
		"RUNTIMED_CUBE_AGENT_BASE_URL": server.URL, "RUNTIMED_CUBE_AGENT_TOKEN": "scoped-fixture",
	}}, func(string, any) {})
	if err != nil || ctx.Err() != nil {
		t.Fatalf("bash task: %v context=%v", err, ctx.Err())
	}
	b, err := os.ReadFile(filepath.Join(dir, "shell.txt"))
	if err != nil || string(b) != "pi-shell-ok" {
		t.Fatalf("bash tool did not execute: %q %v", b, err)
	}
}
