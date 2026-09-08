// Package stream implements the shared SSE writer used by every OpenAI style
// protocol adapter: framing, flushing, raw capture, fault modes and backpressure.
package stream

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/epicai/epicai/backend/internal/engine/fault"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/tokenizer"
)

var ErrClientGone = errors.New("client disconnected")

// Captured is one SSE frame retained for the admin Raw SSE inspector.
type Captured struct {
	Seq  int64  `json:"seq"`
	At   string `json:"at"`
	Data string `json:"data"`
}

// Writer streams SSE to an HTTP client and implements scenario.Sink.
type Writer struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	session  *sessions.Session
	mu       sync.Mutex
	closed   atomic.Bool
	bytesOut int64

	// raw capture ring buffer for the admin inspector
	capMu   sync.Mutex
	capture []Captured
	capMax  int
	capSeq  int64

	// doneFrame is written on a clean finish, e.g. "data: [DONE]\n\n"
	doneFrame string

	// adapter hooks
	textFn   func(delta string) error
	finishFn func(reason string) error
}

func NewWriter(w http.ResponseWriter, session *sessions.Session) (*Writer, error) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming unsupported")
	}
	sw := &Writer{
		w:         w,
		flusher:   fl,
		session:   session,
		capture:   make([]Captured, 0, 200),
		capMax:    200,
		doneFrame: "data: [DONE]\n\n",
	}
	return sw, nil
}

func (s *Writer) SetDoneFrame(f string) { s.doneFrame = f }

func (s *Writer) Header() http.Header { return s.w.Header() }

// Start writes SSE headers.
func (s *Writer) Start() {
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Set("X-Epic-Session-Id", s.session.ID)
	h.Set("X-Request-Id", s.session.RequestID)
	s.w.WriteHeader(http.StatusOK)
	_ = s.emit(": epicai stream opened\n\n")
}

// WriteFrame emits one SSE data frame and flushes.
func (s *Writer) WriteFrame(data string) error {
	return s.emit("data: " + data + "\n\n")
}

// WriteComment emits an SSE comment (keep-alive).
func (s *Writer) WriteComment(text string) error {
	return s.emit(": " + text + "\n\n")
}

// WriteRaw emits a pre-framed payload verbatim (used for malformed chunks).
func (s *Writer) WriteRaw(payload string) error {
	return s.emit(payload)
}

func (s *Writer) emit(payload string) error {
	if s.closed.Load() {
		return ErrClientGone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.w.Write([]byte(payload))
	if n > 0 {
		atomic.AddInt64(&s.bytesOut, int64(n))
		s.session.AddBytesOut(n)
	}
	if err != nil {
		s.closed.Store(true)
		return err
	}
	s.captureFrame(payload)
	s.flusher.Flush()
	return nil
}

func (s *Writer) captureFrame(payload string) {
	s.capMu.Lock()
	s.capSeq++
	s.capture = append(s.capture, Captured{
		Seq:  s.capSeq,
		At:   time.Now().Format(time.RFC3339Nano),
		Data: strings.TrimRight(payload, "\n"),
	})
	if len(s.capture) > s.capMax {
		s.capture = s.capture[len(s.capture)-s.capMax:]
	}
	s.capMu.Unlock()
}

func (s *Writer) Captured() []Captured {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	out := make([]Captured, len(s.capture))
	copy(out, s.capture)
	return out
}

func (s *Writer) BytesOut() int64 { return atomic.LoadInt64(&s.bytesOut) }

// EmitText writes a text delta with token accounting and rate limiting.
func (s *Writer) EmitText(ctx context.Context, delta string) error {
	if delta == "" {
		return nil
	}
	tokens := tokenizer.Count(delta)
	if b := s.session.Bucket(); b != nil {
		if !b.Wait(ctx, tokens) {
			return ErrClientGone
		}
	}
	s.session.AddOutputTokens(tokens)
	return s.WriteText(delta)
}

// WriteText is protocol specific and set by the adapter.
func (s *Writer) WriteText(delta string) error {
	if s.textFn == nil {
		return errors.New("no text writer configured")
	}
	return s.textFn(delta)
}

type textFunc func(delta string) error

var _ textFunc

// EmitRaw implements scenario.Sink: writes a raw protocol line.
func (s *Writer) EmitRaw(ctx context.Context, payload string) error {
	return s.WriteRaw(payload)
}

// Finish ends the stream cleanly with the protocol done frame.
func (s *Writer) Finish(ctx context.Context, reason string) error {
	if s.finishFn != nil {
		if err := s.finishFn(reason); err != nil {
			return err
		}
	}
	if s.doneFrame != "" {
		_ = s.emit(s.doneFrame)
	}
	s.closed.Store(true)
	return nil
}

// Fail applies one of the four stream error modes.
func (s *Writer) Fail(ctx context.Context, spec *sessions.FaultSpec) error {
	mode := spec.Mode
	if mode == "" {
		mode = "sse_error"
	}
	switch strings.ToLower(mode) {
	case "close", "disconnect":
		// Mode A: close the connection immediately.
		s.Disconnect()
		return nil
	case "http_error":
		// Mode C: emit the error inside the SSE stream as an error event, then
		// close. HTTP status cannot change once streaming started, so the
		// error is expressed in-band which is what clients observe.
		body, _ := fault.Body(spec)
		_ = s.emit("event: error\ndata: " + string(body) + "\n\n")
		if s.doneFrame != "" {
			_ = s.emit(s.doneFrame)
		}
		s.closed.Store(true)
		return nil
	case "malformed":
		// Mode D: malformed chunk then close.
		_ = s.emit("data: {\"id\":\"chatcmpl_epic_broken\",\"object\":\"chat.completion.chunk\",\"choices\":[")
		s.Disconnect()
		return nil
	default:
		// Mode B: SSE error event.
		body, _ := fault.Body(spec)
		_ = s.emit("event: error\ndata: " + string(body) + "\n\n")
		s.closed.Store(true)
		return nil
	}
}

// Disconnect abruptly closes the socket: connection reset / unexpected EOF.
func (s *Writer) Disconnect() error {
	s.closed.Store(true)
	s.session.Drop()
	if hj, ok := s.w.(http.Hijacker); ok {
		conn, buf, err := hj.Hijack()
		if err == nil {
			if buf != nil {
				buf.Flush()
			}
			if conn != nil {
				if tc, ok := conn.(*net.TCPConn); ok {
					_ = tc.SetLinger(0)
				}
				_ = conn.Close()
			}
			return net.ErrClosed
		}
	}
	panic(http.ErrAbortHandler)
}

// Configure wires adapter specific behaviour into the shared writer.
func (s *Writer) Configure(text func(delta string) error, finish func(reason string) error) {
	s.textFn = text
	s.finishFn = finish
}

// IsClosed reports whether the stream has ended.
func (s *Writer) IsClosed() bool { return s.closed.Load() }

var _ = bufio.NewWriter
