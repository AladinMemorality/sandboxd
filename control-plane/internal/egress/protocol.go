package egress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	MaxStreams   = 32
	StreamWindow = 64 << 10
	MaxData      = 16 << 10
	MaxFrame     = 32 << 10
)

var ErrDisconnected = errors.New("egress channel disconnected")
var ErrCapacity = errors.New("egress channel stream capacity exhausted")

type frame struct {
	Type   string `json:"type"`
	ID     uint64 `json:"id"`
	Kind   string `json:"kind,omitempty"`
	Host   string `json:"host,omitempty"`
	Port   uint16 `json:"port,omitempty"`
	Data   []byte `json:"data,omitempty"`
	Credit int    `json:"credit,omitempty"`
}

type session struct {
	conn    *websocket.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	writeMu sync.Mutex
	openMu  sync.Mutex
	active  chan struct{}
	mu      sync.Mutex
	streams map[uint64]*stream
	next    uint64
	onOpen  func(*stream, frame)
	done    chan struct{}
	workers sync.WaitGroup
}

func newSession(ctx context.Context, conn *websocket.Conn, open func(*stream, frame)) *session {
	ctx, cancel := context.WithCancel(ctx)
	return &session{conn: conn, ctx: ctx, cancel: cancel, streams: make(map[uint64]*stream), onOpen: open, done: make(chan struct{}), active: make(chan struct{}, MaxStreams)}
}
func (s *session) send(f frame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.ctx.Err() != nil {
		return ErrDisconnected
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := s.conn.WriteJSON(f); err != nil {
		s.cancel()
		_ = s.conn.Close()
		return err
	}
	return nil
}
func (s *session) close() { s.cancel(); _ = s.conn.Close() }
func (s *session) run() {
	defer close(s.done)
	defer func() {
		s.close()
		s.mu.Lock()
		for _, st := range s.streams {
			st.fail(ErrDisconnected)
		}
		s.streams = make(map[uint64]*stream)
		s.mu.Unlock()
		s.workers.Wait()
	}()
	s.conn.SetReadLimit(MaxFrame)
	_ = s.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	s.conn.SetPongHandler(func(string) error { return s.conn.SetReadDeadline(time.Now().Add(90 * time.Second)) })
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				_ = s.conn.Close()
				return
			case <-ticker.C:
				if err := s.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					s.close()
					return
				}
			}
		}
	}()
	for {
		var f frame
		messageType, reader, err := s.conn.NextReader()
		if err != nil || messageType != websocket.TextMessage {
			return
		}
		decoder := json.NewDecoder(reader)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&f); err != nil {
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return
		}
		if f.ID == 0 {
			return
		}
		s.mu.Lock()
		st := s.streams[f.ID]
		lastID := s.next
		if f.Type == "open" {
			if s.onOpen == nil || st != nil || f.ID <= s.next || f.ID > 1<<53 || len(f.Data) != 0 || f.Credit != 0 || len(f.Host) > 253 || (f.Kind != "public" && f.Kind != "model" && f.Kind != "bridge") {
				s.mu.Unlock()
				return
			}
			s.next = f.ID
			if len(s.streams) >= MaxStreams {
				s.mu.Unlock()
				if s.send(frame{Type: "error", ID: f.ID}) != nil {
					return
				}
				continue
			}
			select {
			case s.active <- struct{}{}:
			default:
				s.mu.Unlock()
				if s.send(frame{Type: "error", ID: f.ID}) != nil {
					return
				}
				continue
			}
			st = newStream(s, f.ID)
			s.streams[f.ID] = st
			s.mu.Unlock()
			s.workers.Add(1)
			go func() { defer s.workers.Done(); defer func() { <-s.active }(); s.onOpen(st, f) }()
			continue
		}
		s.mu.Unlock()
		if st == nil {
			// Close/credit/data may already have been queued when the other peer cancelled.
			// IDs never repeat, so late frames cannot enter a later stream.
			if f.ID <= lastID && (f.Type == "close" || f.Type == "credit" || f.Type == "end" || f.Type == "error" || f.Type == "data" || f.Type == "opened") {
				continue
			}
			return
		}
		if f.Kind != "" || f.Host != "" || f.Port != 0 {
			return
		}
		st.mu.Lock()
		invalid := false
		switch f.Type {
		case "opened":
			if s.onOpen != nil || len(f.Data) != 0 || f.Credit != 0 || st.opened {
				invalid = true
			} else {
				st.opened = true
				st.cond.Broadcast()
			}
		case "data":
			if len(f.Data) == 0 || len(f.Data) > MaxData || f.Credit != 0 || st.eof || st.used+len(f.Data) > StreamWindow {
				invalid = true
				break
			}
			for _, b := range f.Data {
				st.buf[(st.head+st.used)%StreamWindow] = b
				st.used++
			}
			st.cond.Broadcast()
		case "credit":
			if len(f.Data) != 0 || f.Credit <= 0 || f.Credit > StreamWindow || st.credit+f.Credit > StreamWindow {
				invalid = true
			} else {
				st.credit += f.Credit
				st.cond.Broadcast()
			}
		case "end":
			if len(f.Data) != 0 || f.Credit != 0 || st.eof {
				invalid = true
			} else {
				st.eof = true
				st.cond.Broadcast()
			}
		case "close", "error":
			if len(f.Data) != 0 || f.Credit != 0 {
				invalid = true
			} else {
				st.err = ErrDisconnected
				st.cancel()
				st.cond.Broadcast()
			}
		default:
			invalid = true
		}
		st.mu.Unlock()
		if invalid {
			return
		}
		if f.Type == "close" || f.Type == "error" {
			s.remove(st)
		}
	}
}
func (s *session) remove(st *stream) {
	s.mu.Lock()
	if s.streams[st.id] == st {
		delete(s.streams, st.id)
	}
	s.mu.Unlock()
}
func (s *session) open(ctx context.Context, kind, host string, port uint16) (*stream, error) {
	s.openMu.Lock()
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		s.openMu.Unlock()
		return nil, ErrDisconnected
	}
	if len(s.streams) >= MaxStreams {
		s.mu.Unlock()
		s.openMu.Unlock()
		return nil, ErrCapacity
	}
	s.next++
	st := newStream(s, s.next)
	s.streams[st.id] = st
	s.mu.Unlock()
	errSend := s.send(frame{Type: "open", ID: st.id, Kind: kind, Host: host, Port: port})
	s.openMu.Unlock()
	if errSend != nil {
		st.Close()
		return nil, errSend
	}
	stop := context.AfterFunc(ctx, func() { st.fail(ctx.Err()) })
	defer stop()
	st.mu.Lock()
	for !st.opened && st.err == nil {
		st.cond.Wait()
	}
	err := st.err
	st.mu.Unlock()
	if err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

type stream struct {
	s                     *session
	id                    uint64
	ctx                   context.Context
	cancel                context.CancelFunc
	mu                    sync.Mutex
	writeMu               sync.Mutex
	cond                  *sync.Cond
	buf                   [StreamWindow]byte
	head, used, credit    int
	eof, opened, writeEnd bool
	err                   error
	once                  sync.Once
}

func newStream(s *session, id uint64) *stream {
	ctx, cancel := context.WithCancel(s.ctx)
	st := &stream{s: s, id: id, ctx: ctx, cancel: cancel, credit: StreamWindow}
	st.cond = sync.NewCond(&st.mu)
	return st
}
func (st *stream) fail(err error) {
	st.mu.Lock()
	if st.err == nil {
		st.err = err
	}
	st.cancel()
	st.cond.Broadcast()
	st.mu.Unlock()
}
func (st *stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	st.mu.Lock()
	for st.used == 0 && !st.eof && st.err == nil {
		st.cond.Wait()
	}
	if st.err != nil || st.ctx.Err() != nil {
		err := st.err
		if err == nil {
			err = ErrDisconnected
		}
		st.mu.Unlock()
		return 0, err
	}
	if st.used == 0 {
		st.mu.Unlock()
		return 0, io.EOF
	}
	n := len(p)
	if n > st.used {
		n = st.used
	}
	for i := 0; i < n; i++ {
		p[i] = st.buf[st.head]
		st.head = (st.head + 1) % StreamWindow
	}
	st.used -= n
	st.mu.Unlock()
	if err := st.s.send(frame{Type: "credit", ID: st.id, Credit: n}); err != nil {
		return n, err
	}
	return n, nil
}
func (st *stream) Write(p []byte) (int, error) {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()
	written := 0
	for len(p) > 0 {
		st.mu.Lock()
		for st.credit == 0 && st.err == nil && !st.writeEnd {
			st.cond.Wait()
		}
		if st.err != nil || st.ctx.Err() != nil || st.writeEnd {
			err := st.err
			if err == nil {
				err = io.ErrClosedPipe
			}
			st.mu.Unlock()
			return written, err
		}
		n := len(p)
		if n > MaxData {
			n = MaxData
		}
		if n > st.credit {
			n = st.credit
		}
		st.credit -= n
		st.mu.Unlock()
		if err := st.s.send(frame{Type: "data", ID: st.id, Data: p[:n]}); err != nil {
			return written, err
		}
		p = p[n:]
		written += n
	}
	return written, nil
}
func (st *stream) CloseWrite() error {
	st.writeMu.Lock()
	defer st.writeMu.Unlock()
	st.mu.Lock()
	if st.writeEnd {
		st.mu.Unlock()
		return nil
	}
	st.writeEnd = true
	st.mu.Unlock()
	return st.s.send(frame{Type: "end", ID: st.id})
}
func (st *stream) Close() error {
	st.once.Do(func() { st.fail(io.ErrClosedPipe); st.s.remove(st); _ = st.s.send(frame{Type: "close", ID: st.id}) })
	return nil
}
