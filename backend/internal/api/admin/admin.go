// Package admin implements the management API: it is logically isolated from
// the OpenAI surface (/v1/*) and requires administrator authentication.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/epicai/epicai/backend/internal/audit"
	"github.com/epicai/epicai/backend/internal/auth"
	"github.com/epicai/epicai/backend/internal/benchmark"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/engine/fault"
	"github.com/epicai/epicai/backend/internal/events"
	"github.com/epicai/epicai/backend/internal/ratelimit"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/epicai/epicai/backend/internal/vlog"
	"github.com/google/uuid"
	"log/slog"
)

type Deps struct {
	Store    storage.Store
	Manager  *sessions.Manager
	Bus      *events.Bus
	Admin    *auth.AdminAuth
	Limiter  *ratelimit.Limiter
	Audit    *audit.Logger
	Log      *slog.Logger
	StreamOf func(sessionID string) *StreamView
}

// StreamView exposes captured SSE frames to the admin inspector.
type StreamView struct {
	Frames func() []Frame
}

type Frame struct {
	Seq  int64  `json:"seq"`
	At   string `json:"at"`
	Data string `json:"data"`
}

type Server struct{ deps Deps }

func New(d Deps) *Server { return &Server{deps: d} }

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, msg, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Error: msg, Code: code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// authn validates the admin bearer token.
func (s *Server) authn(r *http.Request) (string, bool) {
	tok := r.Header.Get("X-Admin-Token")
	if tok == "" {
		tok = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if tok == "" {
		if c, err := r.Cookie("epicai_admin"); err == nil {
			tok = c.Value
		}
	}
	return s.deps.Admin.User(tok), s.deps.Admin.Valid(tok)
}

// Routes returns the admin HTTP mux mounted under /admin.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/api/login", s.handleLogin)
	mux.HandleFunc("/admin/api/logout", s.handleLogout)
	mux.HandleFunc("/admin/api/me", s.guard(s.handleMe))

	mux.HandleFunc("/admin/api/stats", s.guard(s.handleStats))
	mux.HandleFunc("/admin/api/settings", s.guard(s.handleSettings))
	mux.HandleFunc("/admin/api/benchmarks", s.guard(s.handleBenchmarks))

	mux.HandleFunc("/admin/api/sessions", s.guard(s.handleSessions))
	mux.HandleFunc("/admin/api/sessions/", s.guard(s.handleSessionDetail))

	mux.HandleFunc("/admin/api/models", s.guard(s.handleModels))
	mux.HandleFunc("/admin/api/models/", s.guard(s.handleModelDetail))

	mux.HandleFunc("/admin/api/scenarios", s.guard(s.handleScenarios))
	mux.HandleFunc("/admin/api/scenarios/", s.guard(s.handleScenarioDetail))

	mux.HandleFunc("/admin/api/keys", s.guard(s.handleKeys))
	mux.HandleFunc("/admin/api/keys/", s.guard(s.handleKeyDetail))

	mux.HandleFunc("/admin/api/files", s.guard(s.handleFiles))
	mux.HandleFunc("/admin/api/files/", s.guard(s.handleFileDetail))

	mux.HandleFunc("/admin/api/logs", s.guard(s.handleLogs))
	mux.HandleFunc("/admin/api/audit", s.guard(s.handleAudit))
	mux.HandleFunc("/admin/api/vlogs", s.guard(s.handleVlogs))
	mux.HandleFunc("/admin/api/fault-presets", s.guard(s.handlePresets))

	mux.HandleFunc("/admin/api/ws", s.handleWS)
	return mux
}

func (s *Server) guard(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.authn(r)
		if !ok {
			writeErr(w, 401, "admin authentication required", "unauthorized")
			return
		}
		r.Header.Set("X-EpicAI-Admin", user)
		next(w, r)
	}
}

func adminUser(r *http.Request) string { return r.Header.Get("X-EpicAI-Admin") }

// ---------------- auth ----------------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed", "")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	token, ok := s.deps.Admin.Login(req.Username, req.Password)
	if !ok {
		writeErr(w, 401, "invalid credentials", "invalid_credentials")
		return
	}
	s.deps.Audit.Log(audit.Entry{Action: "LOGIN", Admin: req.Username, IP: clientIP(r)})
	writeJSON(w, 200, map[string]any{"token": token, "user": req.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	tok := r.Header.Get("X-Admin-Token")
	s.deps.Admin.Logout(tok)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"user": adminUser(r)})
}

// ---------------- stats ----------------

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	rt := config.C().Runtime()
	stats := s.deps.Limiter.Stats()
	active := s.deps.Manager.ActiveCount()
	manual := 0
	paused := 0
	streaming := 0
	for _, ses := range s.deps.Manager.Live() {
		switch ses.State() {
		case storage.StateManual:
			manual++
		case storage.StatePaused:
			paused++
		}
		if ses.Streaming {
			streaming++
		}
	}
	files, _ := s.deps.Store.ListFiles(r.Context(), 1000, 0)
	var storageBytes int64
	for _, f := range files {
		storageBytes += f.Bytes
	}
	writeJSON(w, 200, map[string]any{
		"active_sessions": active,
		"max_active_sessions": rt.MaxActiveSessions,
		"total_requests": s.deps.Manager.TotalRequests(),
		"streaming_connections": streaming,
		"manual_sessions": manual,
		"paused_sessions": paused,
		"errors_injected": s.deps.Manager.ErrorsInjected(),
		"uploaded_assets": len(files),
		"storage_usage": storageBytes,
		"throughput": stats,
		"uptime_seconds": int(time.Since(startTime).Seconds()),
	})
}

var startTime = time.Now()

// ---------------- settings ----------------

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rt := config.C().Runtime()
		writeJSON(w, 200, map[string]any{
			"runtime": rt,
			"static":  config.C().Static(),
			"tokenizer": map[string]string{"name": "EpicAI Canonical Tokenizer"},
		})
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeErr(w, 400, "invalid json", "bad_request")
			return
		}
		rt := config.C().Update(func(c *config.Runtime) {
			applySettings(c, patch)
		})
		ratelimit.ApplyRuntimeConfig()
		s.deps.Audit.Log(audit.Entry{Action: "UPDATE_SETTINGS", Admin: adminUser(r), IP: clientIP(r), Params: summarize(patch)})
		s.deps.Bus.Publish(events.Message{Type: events.SettingsChanged, Data: map[string]any{"by": adminUser(r)}})
		writeJSON(w, 200, rt)
	default:
		writeErr(w, 405, "method not allowed", "")
	}
}

func applySettings(c *config.Runtime, p map[string]any) {
	for k, v := range p {
		switch k {
		case "cors_mode":
			if s, ok := v.(string); ok {
				c.CORSMode = config.CORS(s)
			}
		case "cors_allow_origins":
			if arr, ok := v.([]any); ok {
				out := make([]string, 0, len(arr))
				for _, a := range arr {
					if s, ok := a.(string); ok {
						out = append(out, s)
					}
				}
				c.CORSAllowOrigins = out
			}
		case "key_mode":
			if s, ok := v.(string); ok {
				c.KeyMode = config.KeyMode(s)
			}
		case "global_token_rate":
			c.GlobalTokenRate = toInt64(v)
		case "global_burst_seconds":
			c.GlobalBurstSeconds = toFloat(v)
		case "session_token_rate":
			c.SessionTokenRate = toInt64(v)
		case "session_burst_seconds":
			c.SessionBurstSeconds = toFloat(v)
		case "rate_mode":
			if s, ok := v.(string); ok {
				c.RateMode = s
			}
		case "chunk_size_tokens":
			c.ChunkSizeTokens = int(toInt64(v))
		case "max_active_sessions":
			c.MaxActiveSessions = int(toInt64(v))
		case "max_sessions_per_ip":
			c.MaxSessionsPerIP = int(toInt64(v))
		case "max_sessions_per_key":
			c.MaxSessionsPerKey = int(toInt64(v))
		case "overload_behavior":
			if s, ok := v.(string); ok {
				c.OverloadBehavior = config.OverloadBehavior(s)
			}
		case "overload_custom_body":
			if s, ok := v.(string); ok {
				c.OverloadCustomBody = s
			}
		case "overload_custom_code":
			c.OverloadCustomCode = int(toInt64(v))
		case "max_file_size":
			c.MaxFileSize = toInt64(v)
		case "max_assets_size":
			c.MaxAssetsSize = toInt64(v)
		case "max_events_per_session":
			c.MaxEventsPerSession = int(toInt64(v))
		case "max_echo_rate":
			c.MaxEchoRate = toFloat(v)
		case "resource_action":
			if s, ok := v.(string); ok {
				c.ResourceAction = config.ResourceAction(s)
			}
		case "retention_days":
			c.RetentionDays = int(toInt64(v))
		case "hold_connection_forever":
			c.HoldConnectionForever = toBool(v)
		case "bypass_manual_rate":
			c.BypassManualRate = toBool(v)
		case "default_echo_interval_ms":
			c.DefaultEchoIntervalMS = int(toInt64(v))
		case "echo_content_mode":
			if s, ok := v.(string); ok {
				c.EchoContentMode = s
			}
		case "image_echo_mode":
			if s, ok := v.(string); ok {
				c.ImageEchoMode = s
			}
		case "repeat_file_reference":
			c.RepeatFileReference = toBool(v)
		case "repeat_download_url":
			c.RepeatDownloadURL = toBool(v)
		case "repeat_metadata":
			c.RepeatMetadata = toBool(v)
		case "unknown_model_behavior":
			if s, ok := v.(string); ok {
				c.UnknownModelBehavior = s
			}
		case "admin_max_token_rate":
			c.AdminMaxTokenRate = toInt64(v)
		case "manual_chars_per_second":
			c.ManualCharsPerSecond = int(toInt64(v))
		}
	}
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case bool:
		if n {
			return 1
		}
		return 0
	case string:
		if i, err := strconv.ParseInt(n, 10, 64); err == nil {
			return i
		}
	}
	return 0
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f
		}
	}
	return 0
}

func toBool(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case float64:
		return n != 0
	case string:
		return n == "true" || n == "1"
	}
	return false
}

func summarize(p map[string]any) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	if len(keys) > 8 {
		keys = keys[:8]
	}
	return strings.Join(keys, ",")
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		return strings.TrimSpace(strings.Split(xf, ",")[0])
	}
	return r.RemoteAddr
}

// ---------------- fault presets ----------------

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, fault.Presets)
}

// ---------------- logs / audit ----------------

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	s.deps.Audit.Flush()
	limit := queryInt(r, "limit", 200)
	offset := queryInt(r, "offset", 0)
	entries, err := s.deps.Store.ListAudit(r.Context(), limit, offset)
	if err != nil {
		writeErr(w, 500, err.Error(), "internal")
		return
	}
	writeJSON(w, 200, entries)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	sid := r.URL.Query().Get("session_id")
	limit := queryInt(r, "limit", 200)
	if sid != "" {
		evs, err := s.deps.Store.ListEvents(r.Context(), storage.EventFilter{SessionID: sid, Limit: limit})
		if err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		writeJSON(w, 200, evs)
		return
	}
	writeJSON(w, 200, map[string]any{
		"kind": kind,
		"logs": map[string]string{
			"access":       "stored in sessions table",
			"conversation": "stored in events table",
			"admin":        "stored in audit table",
			"error":        "stored in events table with kind=error",
			"protocol":     "stored in sessions.raw_request",
		},
	})
}

func (s *Server) handleVlogs(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 200)
	audits, _ := vlog.ReadRecentAudit(limit)
	fronts, _ := vlog.ReadRecentFrontend(limit)
	writeJSON(w, 200, map[string]any{
		"audit":    audits,
		"frontend": fronts,
	})
}

func (s *Server) handleBenchmarks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Protocol   string `json:"protocol"`
			DurationMS int    `json:"duration_ms"`
			ChunkSize  int    `json:"chunk_size"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Protocol == "" {
			body.Protocol = "chat.completions"
		}
		if body.DurationMS <= 0 {
			body.DurationMS = 3000
		}
		if body.ChunkSize <= 0 {
			body.ChunkSize = 32
		}
		dur := time.Duration(body.DurationMS) * time.Millisecond
		res := benchmark.RunUncapped(r.Context(), body.Protocol, body.ChunkSize, dur)
		// Persist the measured maximum so the rate control UI can use it.
		_ = s.deps.Store.SaveBenchmark(r.Context(), res.ToStorage())
		_ = s.deps.Store.SetSetting(r.Context(), "measured_max_"+body.Protocol,
			strconv.FormatInt(int64(res.AverageRate), 10))
		s.deps.Audit.Log(audit.Entry{Action: "RUN_BENCHMARK", Admin: adminUser(r), IP: clientIP(r),
			Params: fmt.Sprintf("protocol=%s chunk=%d duration_ms=%d avg=%.0f", body.Protocol, body.ChunkSize, body.DurationMS, res.AverageRate)})
		writeJSON(w, 200, res)
		return
	}
	list, err := s.deps.Store.ListBenchmarks(r.Context(), 50)
	if err != nil {
		writeErr(w, 500, err.Error(), "internal")
		return
	}
	measured := map[string]string{}
	for _, proto := range []string{"chat.completions", "responses"} {
		if v, err := s.deps.Store.GetSetting(r.Context(), "measured_max_"+proto); err == nil {
			measured[proto] = v
		}
	}
	writeJSON(w, 200, map[string]any{"benchmarks": list, "measured_maximum": measured})
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func newID(prefix string) string { return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] }

var _ = context.Background
