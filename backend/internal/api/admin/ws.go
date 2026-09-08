package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/epicai/epicai/backend/internal/events"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// handleWS streams realtime session/chunk/fault events to the admin UI.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authn(r); !ok {
		// Also accept the token from the query string for browser WebSocket.
		tok := r.URL.Query().Get("token")
		if !s.deps.Admin.Valid(tok) {
			writeErr(w, 401, "admin authentication required", "unauthorized")
			return
		}
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sub := s.deps.Bus.Subscribe()
	defer s.deps.Bus.Unsubscribe(sub)

	// initial snapshot of live sessions
	if snap, err := json.Marshal(map[string]any{
		"type": "snapshot",
		"data": s.liveSnapshot(),
	}); err == nil {
		_ = conn.WriteMessage(websocket.TextMessage, snap)
	}

	// ping ticker to keep proxies from closing the connection
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = conn.WriteMessage(websocket.PingMessage, nil)
			case <-done:
				return
			}
		}
	}()
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case msg, ok := <-sub.Ch():
			if !ok {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

func (s *Server) liveSnapshot() []map[string]any {
	out := make([]map[string]any, 0)
	for _, ses := range s.deps.Manager.Live() {
		snap := ses.SnapshotSession()
		out = append(out, map[string]any{
			"session_id": snap.ID, "model": snap.Model, "protocol": snap.Protocol,
			"state": snap.State, "mode": snap.Mode, "client_ip": snap.ClientIP,
			"created_at": snap.CreatedAt, "echo_count": snap.EchoCount,
			"bytes_out": snap.BytesOut, "output_tokens": snap.OutputTokens,
			"current_rate": snap.CurrentRate, "streaming": snap.Streaming,
		})
	}
	return out
}

var _ = events.SessionCreated
