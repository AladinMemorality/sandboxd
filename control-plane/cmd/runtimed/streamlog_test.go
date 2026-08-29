package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type stampRec struct {
	TS   string          `json:"ts"`
	Ev   json.RawMessage `json:"ev"`
	Text string          `json:"text"`
}

func decodeStamps(t *testing.T, buf *bytes.Buffer) []stampRec {
	t.Helper()
	var out []stampRec
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var r stampRec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("record %q is not valid JSON: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// Lines arriving split across writes are reassembled, stamped once per
// complete line, and JSON lines are embedded verbatim.
func TestStampWriterReassemblesChunkedLines(t *testing.T) {
	var buf bytes.Buffer
	w := newStampWriter(&buf)
	w.now = func() time.Time { return time.Date(2026, 8, 27, 18, 0, 0, 123456789, time.UTC) }

	for _, chunk := range []string{`{"type":"assist`, `ant","n":1}` + "\n" + `not json`, "\n"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = (%d, %v)", chunk, n, err)
		}
	}

	recs := decodeStamps(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2: %s", len(recs), buf.String())
	}
	if string(recs[0].Ev) != `{"type":"assistant","n":1}` {
		t.Errorf("JSON line not embedded verbatim: %s", recs[0].Ev)
	}
	if recs[1].Text != "not json" {
		t.Errorf("non-JSON line: got %q", recs[1].Text)
	}
	for _, r := range recs {
		if _, err := time.Parse(time.RFC3339Nano, r.TS); err != nil {
			t.Errorf("ts %q is not RFC3339Nano: %v", r.TS, err)
		}
	}
}

// A trailing partial line (agent killed mid-write) is flushed by Close,
// and blank lines never produce records.
func TestStampWriterCloseFlushesPartialLine(t *testing.T) {
	var buf bytes.Buffer
	w := newStampWriter(&buf)

	_, _ = w.Write([]byte("\n  \n" + `{"cut":`)) // blanks, then an unterminated line
	if recs := decodeStamps(t, &buf); len(recs) != 0 {
		t.Fatalf("blank/partial input produced %d records early", len(recs))
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	recs := decodeStamps(t, &buf)
	if len(recs) != 1 || recs[0].Text != `{"cut":` {
		t.Fatalf("partial line not flushed as text: %+v", recs)
	}
}

// teeStream with a nil writer must hand back the reader untouched.
func TestTeeStreamNilWriter(t *testing.T) {
	r := strings.NewReader("x")
	if teeStream(r, nil) != r {
		t.Error("teeStream(r, nil) should return r unchanged")
	}
}
