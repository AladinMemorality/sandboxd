package main

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// stampWriter turns an agent's raw stdout stream into a timestamped
// per-line JSONL transcript (`.runtimed/tasks/<id>/stream.jsonl`). The
// canonical events log (`events.jsonl`) keeps only the mapped, truncated
// view of a task; this file keeps the agent CLI's exact output — every
// stream-json line verbatim — so what the agent actually did can be
// debugged after the fact. Each complete line becomes one record:
//
//	{"ts":"2026-08-27T17:47:06.735983628Z","ev":{…}}   line was JSON
//	{"ts":"2026-08-27T17:47:06.735983628Z","text":"…"} line was not
//
// The timestamp is taken when the line's newline arrives on the pipe, so
// records carry the real wall-clock timing of the agent's activity.
// Write errors are swallowed: transcript logging must never fail a task.
type stampWriter struct {
	mu  sync.Mutex
	out io.Writer
	buf bytes.Buffer
	now func() time.Time // injectable for tests
}

func newStampWriter(out io.Writer) *stampWriter {
	return &stampWriter{out: out, now: time.Now}
}

func (w *stampWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		b := w.buf.Bytes()
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}
		line := append([]byte(nil), b[:i]...)
		w.buf.Next(i + 1)
		w.stamp(line)
	}
	return len(p), nil
}

// Close flushes a trailing partial line — a killed agent can end mid-line.
func (w *stampWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.stamp(append([]byte(nil), w.buf.Bytes()...))
		w.buf.Reset()
	}
	return nil
}

func (w *stampWriter) stamp(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	rec := struct {
		TS   string          `json:"ts"`
		Ev   json.RawMessage `json:"ev,omitempty"`
		Text string          `json:"text,omitempty"`
	}{TS: w.now().UTC().Format(time.RFC3339Nano)}
	if json.Valid(line) {
		rec.Ev = json.RawMessage(line)
	} else {
		rec.Text = string(line)
	}
	enc, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = w.out.Write(append(enc, '\n'))
}

// teeStream mirrors r into w (the task's transcript writer) as it is
// read. A nil w returns r unchanged, so adapters can pass spec.streamLog
// straight through without a guard.
func teeStream(r io.Reader, w io.Writer) io.Reader {
	if w == nil {
		return r
	}
	return io.TeeReader(r, w)
}
