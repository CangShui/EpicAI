package api

import (
	"net/http"
	"runtime"
	"time"

	"github.com/epicai/epicai/backend/internal/adapters/openai"
	"github.com/epicai/epicai/backend/internal/config"
)

// HandleHealth serves GET /health.
func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if openai.ApplyCORS(w, r) {
		return
	}
	writeJSON(w, 200, map[string]any{
		"status":  "ok",
		"service": "epicai",
		"version": Version,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

// HandleReady serves GET /ready with dependency checks.
func (s *Server) HandleReady(w http.ResponseWriter, r *http.Request) {
	if openai.ApplyCORS(w, r) {
		return
	}
	checks := map[string]string{}
	ok := true

	if err := s.deps.Store.Ping(r.Context()); err != nil {
		checks["database"] = "fail: " + err.Error()
		ok = false
	} else {
		checks["database"] = "ok"
	}

	if s.deps.Assets != nil {
		checks["storage"] = "ok"
	} else {
		checks["storage"] = "fail: not configured"
		ok = false
	}

	if s.deps.Bus != nil {
		checks["event_engine"] = "ok"
	} else {
		checks["event_engine"] = "fail: not configured"
		ok = false
	}

	status := 200
	if !ok {
		status = 503
	}
	writeJSON(w, status, map[string]any{
		"status":              statusText(ok),
		"checks":              checks,
		"uptime_seconds":      int(time.Since(startTime).Seconds()),
		"goroutines":          runtime.NumGoroutine(),
		"active_sessions":     s.deps.Manager.ActiveCount(),
		"max_active_sessions": config.C().Runtime().MaxActiveSessions,
	})
}

func statusText(ok bool) string {
	if ok {
		return "ok"
	}
	return "unavailable"
}
