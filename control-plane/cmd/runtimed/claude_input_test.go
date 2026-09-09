package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
	"io"
	"strings"
	"sync"
	"testing"
)

type inputBuffer struct {
	bytes.Buffer
	closed bool
}

func (w *inputBuffer) Close() error { w.closed = true; return nil }

func inputMessage() runtime.TaskMessage {
	return runtime.TaskMessage{MessageID: "00000000-0000-4000-8000-000000000002", Prompt: "Use blue instead"}
}

func TestLiveInputBeforeStartAndReplay(t *testing.T) {
	in := newClaudeInput()
	req := inputMessage()
	if added, err := in.send(req); err != nil || !added {
		t.Fatalf("send: %v %v", added, err)
	}
	w := &inputBuffer{}
	if err := in.attach(w, "Build the site"); err != nil {
		t.Fatal(err)
	}
	var messages []map[string]any
	d := json.NewDecoder(strings.NewReader(w.String()))
	for {
		var m map[string]any
		if err := d.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, m)
	}
	if len(messages) != 2 || messages[1]["uuid"] != req.MessageID {
		t.Fatalf("messages: %v", messages)
	}
	before := w.Len()
	if added, err := in.send(req); err != nil || added || w.Len() != before {
		t.Fatalf("duplicate: %v %v", added, err)
	}
	in.result(false)
	if w.closed {
		t.Fatal("closed stdin while a follow-up is awaiting CLI acknowledgement")
	}
	sink, events := collectSink()
	parseClaudeStreamInput(strings.NewReader(`{"type":"user","uuid":"`+req.MessageID+`","message":{"role":"user","content":"Use blue instead"}}`+"\n"+`{"type":"result","subtype":"success","result":"Applied"}`+"\n"), sink, in)
	if !w.closed {
		t.Fatal("stdin must close after the final result")
	}
	if len(*events) != 1 || (*events)[0].typ != "input" {
		t.Fatalf("acknowledgement: %v", *events)
	}
	if _, err := in.send(runtime.TaskMessage{MessageID: "other", Prompt: "late"}); !errors.Is(err, errAgentInputClosed) {
		t.Fatalf("late: %v", err)
	}
}

func TestLiveInputConflictingIdAndFailure(t *testing.T) {
	in := newClaudeInput()
	w := &inputBuffer{}
	_ = in.attach(w, "initial")
	req := inputMessage()
	_, _ = in.send(req)
	req.Prompt = "different"
	if _, err := in.send(req); err == nil {
		t.Fatal("accepted reused id with different content")
	}
	in.result(true)
	if !w.closed {
		t.Fatal("error result left stdin open")
	}
}

func TestLiveInputSendCompletionRace(t *testing.T) {
	for i := 0; i < 100; i++ {
		in := newClaudeInput()
		w := &inputBuffer{}
		_ = in.attach(w, "initial")
		var wg sync.WaitGroup
		wg.Add(2)
		var added bool
		var err error
		go func() { defer wg.Done(); added, err = in.send(inputMessage()) }()
		go func() { defer wg.Done(); in.result(false) }()
		wg.Wait()
		if added {
			if err != nil || w.closed {
				t.Fatal("accepted input was abandoned at completion")
			}
			in.acknowledge(inputMessage().MessageID)
			in.result(false)
		} else if !errors.Is(err, errAgentInputClosed) {
			t.Fatalf("unexpected rejection: %v", err)
		}
		if !w.closed {
			t.Fatal("stdin not closed")
		}
	}
}
