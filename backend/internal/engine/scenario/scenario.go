// Package scenario executes scripted action sequences bound to a model.
package scenario

import (
	"context"
	"fmt"
	"time"

	"github.com/epicai/epicai/backend/internal/canonical"
	"github.com/epicai/epicai/backend/internal/engine/fault"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/storage"
)

// Step actions supported by the Scenario DSL.
const (
	ActEcho             = "echo"
	ActWait             = "wait"
	ActPause            = "pause"
	ActSendText         = "send_text"
	ActSendAsset        = "send_asset"
	ActSendRawSSE       = "send_raw_sse"
	ActSendMalformedSSE = "send_malformed_sse"
	ActFinish           = "finish"
	ActError            = "error"
	ActDisconnect       = "disconnect"
	ActLoop             = "loop"
)

// Sink is implemented by the streaming writer so the scenario engine stays
// independent from any specific protocol adapter.
type Sink interface {
	// EmitText writes one text delta to the client.
	EmitText(ctx context.Context, delta string) error
	// EmitRaw writes a raw protocol line (SSE frame or JSON) to the client.
	EmitRaw(ctx context.Context, payload string) error
	// Finish ends the stream normally.
	Finish(ctx context.Context, reason string) error
	// Fail ends the stream with an error using the given mode.
	Fail(ctx context.Context, spec *sessions.FaultSpec) error
	// Disconnect closes the socket abruptly.
	Disconnect() error
}

// Runner executes a scenario against a live session.
type Runner struct {
	session *sessions.Session
	sink    Sink
	steps   []storage.ScenarioStep
	conv    *canonical.Conversation
}

func New(s *sessions.Session, sink Sink, steps []storage.ScenarioStep, conv *canonical.Conversation) *Runner {
	return &Runner{session: s, sink: sink, steps: steps, conv: conv}
}

// Run executes the scenario. It returns when the scenario terminates the
// session, the client disconnects, or the context is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	if len(r.steps) == 0 {
		return fmt.Errorf("scenario has no steps")
	}
	loopStart := 0
	loopRemaining := 0
	for i := 0; i < len(r.steps); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.session.Done():
			return sessions.ErrSessionClosed
		default:
		}
		st := r.steps[i]
		if err := r.exec(ctx, st); err != nil {
			return err
		}
		switch st.Action {
		case ActFinish, ActError, ActDisconnect:
			return nil
		case ActLoop:
			if st.LoopCount > 0 {
				if loopRemaining <= 0 {
					loopRemaining = st.LoopCount
				}
				loopRemaining--
				if loopRemaining <= 0 {
					i++
					continue
				}
			}
			i = loopStart
			continue
		}
		if loopRemaining == 0 && loopStart == 0 && st.Action == ActLoop {
			i++
			continue
		}
		i++
	}
	return nil
}

func (r *Runner) exec(ctx context.Context, st storage.ScenarioStep) error {
	s := r.session
	switch st.Action {
	case ActEcho:
		n := st.Count
		if n <= 0 {
			n = 1
		}
		interval := time.Duration(st.IntervalMS) * time.Millisecond
		if st.IntervalMS == 0 {
			interval = time.Duration(s.IntervalMS()) * time.Millisecond
		}
		for k := 0; k < n; k++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-s.Done():
				return sessions.ErrSessionClosed
			default:
			}
			text := r.conv.LastUserText()
			if b := s.Bucket(); b != nil {
				if ok := b.Wait(ctx, tokenCount(text)); !ok {
					return sessions.ErrSessionClosed
				}
			}
			if err := r.sink.EmitText(ctx, text); err != nil {
				return err
			}
			s.AddEcho()
			if interval > 0 {
				select {
				case <-time.After(interval):
				case <-ctx.Done():
					return ctx.Err()
				case <-s.Done():
					return sessions.ErrSessionClosed
				}
			}
		}
		return nil
	case ActWait:
		d := time.Duration(st.WaitMS) * time.Millisecond
		if d <= 0 {
			d = time.Duration(st.IntervalMS) * time.Millisecond
		}
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		case <-s.Done():
			return sessions.ErrSessionClosed
		}
		return nil
	case ActPause:
		s.Pause()
		return nil
	case ActSendText:
		if err := r.sink.EmitText(ctx, st.Text); err != nil {
			return err
		}
		return nil
	case ActSendRawSSE:
		return r.sink.EmitRaw(ctx, st.Raw)
	case ActSendMalformedSSE:
		_ = r.sink.EmitRaw(ctx, fault.MalformedChunk())
		return r.sink.Disconnect()
	case ActFinish:
		return r.sink.Finish(ctx, "stop")
	case ActError:
		return r.sink.Fail(ctx, &sessions.FaultSpec{
			HTTPStatus: orStatus(st.HTTPStatus), Code: st.ErrorCode, Type: st.ErrorType,
			Message: st.ErrorMessage, Mode: orMode(st.ErrorMode), RawBody: st.Raw,
			RawMode: st.Raw != "" && st.ErrorMode == "raw",
		})
	case ActDisconnect:
		return r.sink.Disconnect()
	default:
		return nil
	}
}

func orStatus(n int) int {
	if n <= 0 {
		return 500
	}
	return n
}

func orMode(m string) string {
	if m == "" {
		return "sse_error"
	}
	return m
}

func tokenCount(s string) int {
	return canonicalTokenCount(s)
}
