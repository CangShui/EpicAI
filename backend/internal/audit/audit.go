// Package audit records administrator operations.
package audit

import (
	"context"
	"sync"
	"time"

	"github.com/epicai/epicai/backend/internal/storage"
)

type Entry struct {
	Admin    string
	Session  string
	Action   string
	Params   string
	IP       string
}

type Logger struct {
	store storage.Store
	mu    sync.Mutex
	batch []storage.AuditEntry
	done  chan struct{}
}

func New(store storage.Store) *Logger {
	l := &Logger{store: store, batch: make([]storage.AuditEntry, 0, 32), done: make(chan struct{})}
	go l.flushLoop()
	return l
}

func (l *Logger) Log(e Entry) {
	l.mu.Lock()
	l.batch = append(l.batch, storage.AuditEntry{
		Timestamp: time.Now(), Admin: e.Admin, SessionID: e.Session,
		Action: e.Action, Params: e.Params, IP: e.IP,
	})
	n := len(l.batch)
	l.mu.Unlock()
	if n >= 32 {
		l.Flush()
	}
}

func (l *Logger) Flush() {
	l.mu.Lock()
	if len(l.batch) == 0 {
		l.mu.Unlock()
		return
	}
	batch := l.batch
	l.batch = make([]storage.AuditEntry, 0, 32)
	l.mu.Unlock()
	for i := range batch {
		_ = l.store.AppendAudit(context.Background(), &batch[i])
	}
}

func (l *Logger) flushLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			l.Flush()
		case <-l.done:
			l.Flush()
			return
		}
	}
}

func (l *Logger) Close() {
	close(l.done)
	l.Flush()
}
