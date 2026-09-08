// Package events provides the realtime fan-out bus used to push session,
// chunk, manual and fault events to the admin WebSocket clients.
package events

import (
	"encoding/json"
	"sync"
	"time"
)

type Type string

const (
	SessionCreated  Type = "session.created"
	SessionUpdated  Type = "session.updated"
	InputReceived   Type = "input.received"
	ChunkSent       Type = "chunk.sent"
	ManualTakeover  Type = "manual.takeover"
	ManualMessage   Type = "manual.message"
	FaultInjected   Type = "fault.injected"
	SessionClosed   Type = "session.closed"
	ModelChanged    Type = "model.changed"
	SettingsChanged Type = "settings.changed"
	StatsUpdated    Type = "stats.updated"
)

type Message struct {
	Type    Type           `json:"type"`
	Session string         `json:"session_id,omitempty"`
	At      time.Time      `json:"at"`
	Data    map[string]any `json:"data,omitempty"`
}

type Subscriber struct {
	ch     chan []byte
	closed bool
	mu     sync.Mutex
}

func (s *Subscriber) Ch() <-chan []byte { return s.ch }

func (s *Subscriber) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

type Bus struct {
	mu   sync.RWMutex
	subs map[*Subscriber]struct{}
	// ring buffer of recent messages for late subscribers
	recent   []Message
	recentMu sync.RWMutex
	recentN  int
	// coalescing: high-frequency chunk events are throttled per session
	throttle map[string]time.Time
}

func New() *Bus {
	return &Bus{
		subs:     map[*Subscriber]struct{}{},
		recent:   make([]Message, 0, 200),
		recentN:  200,
		throttle: map[string]time.Time{},
	}
}

func (b *Bus) Subscribe() *Subscriber {
	s := &Subscriber{ch: make(chan []byte, 512)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (b *Bus) Unsubscribe(s *Subscriber) {
	b.mu.Lock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		s.Close()
	}
	b.mu.Unlock()
}

func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Publish broadcasts a message. High frequency chunk events are coalesced so a
// 1M token/s session cannot flood admin browsers.
func (b *Bus) Publish(m Message) {
	if m.At.IsZero() {
		m.At = time.Now()
	}
	if m.Type == ChunkSent && m.Session != "" {
		key := string(m.Type) + ":" + m.Session
		b.mu.Lock()
		if t, ok := b.throttle[key]; ok && time.Since(t) < 100*time.Millisecond {
			b.mu.Unlock()
			return
		}
		b.throttle[key] = time.Now()
		b.mu.Unlock()
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	b.recentMu.Lock()
	b.recent = append(b.recent, m)
	if len(b.recent) > b.recentN {
		b.recent = b.recent[len(b.recent)-b.recentN:]
	}
	b.recentMu.Unlock()

	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- raw:
		default:
			// slow consumer: drop instead of blocking the engine
		}
	}
}

func (b *Bus) Recent() []Message {
	b.recentMu.RLock()
	defer b.recentMu.RUnlock()
	out := make([]Message, len(b.recent))
	copy(out, b.recent)
	return out
}
