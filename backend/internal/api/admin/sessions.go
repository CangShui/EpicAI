package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/epicai/epicai/backend/internal/audit"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/storage"
)

// sessionView is the enriched payload returned to the admin UI.
type sessionView struct {
	storage.Session
	DurationMS     int64 `json:"duration_ms"`
	Live           bool  `json:"live"`
	EffectiveLimit int64 `json:"effective_limit"`
	GlobalLimit    int64 `json:"global_limit"`
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listSessions(w, r)
	case http.MethodDelete:
		s.deleteAllSessions(w, r)
	default:
		writeErr(w, 405, "method not allowed", "")
	}
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := storage.SessionFilter{
		Query: q.Get("q"), Model: q.Get("model"), Protocol: q.Get("protocol"),
		State: q.Get("state"), IP: q.Get("ip"), Key: q.Get("key"),
		Limit: queryInt(r, "limit", 100), Offset: queryInt(r, "offset", 0),
		ActiveOnly: q.Get("active") == "true",
	}
	if since := q.Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			f.Since = &t
		}
	}
	if until := q.Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			f.Until = &t
		}
	}
	list, err := s.deps.Store.ListSessions(r.Context(), f)
	if err != nil {
		writeErr(w, 500, err.Error(), "internal")
		return
	}
	total, _ := s.deps.Store.CountSessions(r.Context(), f)

	live := map[string]bool{}
	for _, ses := range s.deps.Manager.Live() {
		live[ses.ID] = true
	}
	out := make([]sessionView, 0, len(list))
	for i := range list {
		v := sessionView{Session: list[i], Live: live[list[i].ID]}
		if ended := list[i].EndedAt; ended != nil {
			v.DurationMS = ended.Sub(list[i].CreatedAt).Milliseconds()
		} else {
			v.DurationMS = time.Since(list[i].CreatedAt).Milliseconds()
		}
		rt := config.C().Runtime()
		v.GlobalLimit = rt.GlobalTokenRate
		v.EffectiveLimit = list[i].Rate.TokenRate
		out = append(out, v)
	}
	writeJSON(w, 200, map[string]any{"sessions": out, "total": total, "limit": f.Limit, "offset": f.Offset})
}

func (s *Server) deleteAllSessions(w http.ResponseWriter, r *http.Request) {
	for _, ses := range s.deps.Manager.Live() {
		ses.Close(storage.StateEnded, "admin_delete")
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/api/sessions/"), "/")
	if rest == "" {
		writeErr(w, 400, "session id required", "bad_request")
		return
	}
	parts := strings.Split(rest, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	ses := s.deps.Manager.Get(id)
	if ses == nil {
		rec, err := s.deps.Store.GetSession(r.Context(), id)
		if err != nil || rec == nil {
			writeErr(w, 404, "session not found", "not_found")
			return
		}
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		s.getSession(w, r, id)
	case action == "" && r.Method == http.MethodDelete:
		s.deleteSession(w, r, id)
	case action == "events":
		s.sessionEvents(w, r, id)
	case action == "raw-sse":
		s.sessionRawSSE(w, r, id)
	case action == "control" && r.Method == http.MethodPost:
		s.sessionControl(w, r, id, ses)
	default:
		writeErr(w, 404, "unknown action", "not_found")
	}
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request, id string) {
	rec, err := s.deps.Store.GetSession(r.Context(), id)
	if err != nil || rec == nil {
		writeErr(w, 404, "session not found", "not_found")
		return
	}
	if live := s.deps.Manager.Get(id); live != nil {
		snap := live.SnapshotSession()
		rec = snap
	}
	v := sessionView{Session: *rec, Live: s.deps.Manager.Get(id) != nil}
	if rec.EndedAt != nil {
		v.DurationMS = rec.EndedAt.Sub(rec.CreatedAt).Milliseconds()
	} else {
		v.DurationMS = time.Since(rec.CreatedAt).Milliseconds()
	}
	rt := config.C().Runtime()
	v.GlobalLimit = rt.GlobalTokenRate
	v.EffectiveLimit = rec.Rate.TokenRate
	writeJSON(w, 200, v)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request, id string) {
	if live := s.deps.Manager.Get(id); live != nil {
		live.Close(storage.StateEnded, "admin_delete")
	}
	_ = s.deps.Store.DeleteSession(r.Context(), id)
	s.deps.Audit.Log(auditEntry(r, "DELETE_SESSION", id, ""))
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) sessionEvents(w http.ResponseWriter, r *http.Request, id string) {
	limit := queryInt(r, "limit", 500)
	evs, err := s.deps.Store.ListEvents(r.Context(), storage.EventFilter{SessionID: id, Limit: limit})
	if err != nil {
		writeErr(w, 500, err.Error(), "internal")
		return
	}
	// results come back newest-first; reverse for chronological display
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}
	total, _ := s.deps.Store.CountEvents(r.Context(), id)
	writeJSON(w, 200, map[string]any{"events": evs, "total": total, "limit": limit})
}

func (s *Server) sessionRawSSE(w http.ResponseWriter, r *http.Request, id string) {
	if s.deps.StreamOf == nil {
		writeJSON(w, 200, map[string]any{"frames": []any{}})
		return
	}
	view := s.deps.StreamOf(id)
	if view == nil || view.Frames == nil {
		writeJSON(w, 200, map[string]any{"frames": []any{}, "note": "stream is no longer active"})
		return
	}
	writeJSON(w, 200, map[string]any{"frames": view.Frames()})
}

// sessionControl applies administrator commands to a live session.
func (s *Server) sessionControl(w http.ResponseWriter, r *http.Request, id string, ses *sessions.Session) {
	if ses == nil {
		writeErr(w, 409, "session is not live", "not_live")
		return
	}
	var body struct {
		Action      string `json:"action"`
		Text        string `json:"text"`
		Rate        int64  `json:"rate"`
		Mode        string `json:"mode"`
		IntervalMS  int    `json:"interval_ms"`
		ChunkSize   int    `json:"chunk_size"`
		HTTPStatus  int    `json:"http_status"`
		Code        string `json:"code"`
		Type        string `json:"type"`
		Message     string `json:"message"`
		Param       string `json:"param"`
		RawBody     string `json:"raw_body"`
		RawMode     bool   `json:"raw_mode"`
		DelayMS     int    `json:"delay_ms"`
		AfterChunks int    `json:"after_chunks"`
		FaultMode   string `json:"fault_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid json", "bad_request")
		return
	}

	auditAction := strings.ToUpper(body.Action)
	params := summarizeControl(body)

	switch strings.ToLower(body.Action) {
	case "pause":
		ses.Pause()
		_ = ses.Send(sessions.Command{Type: "pause"})
	case "resume":
		ses.Resume()
		_ = ses.Send(sessions.Command{Type: "resume"})
	case "takeover":
		ses.TakeOver()
		_ = ses.Send(sessions.Command{Type: "takeover"})
	case "return":
		ses.ReturnToEcho()
		_ = ses.Send(sessions.Command{Type: "return"})
	case "send":
		if body.Text == "" {
			writeErr(w, 400, "text is required", "bad_request")
			return
		}
		if err := ses.SendManual(body.Text); err != nil {
			writeErr(w, 409, err.Error(), "not_live")
			return
		}
	case "finish":
		ses.Finish("stop")
	case "drop":
		_ = ses.Send(sessions.Command{Type: "drop"})
	case "rate":
		rt := config.C().Runtime()
		mode := body.Mode
		if mode == "" {
			mode = rt.RateMode
		}
		ses.SetRate(body.Rate, mode, rt.SessionBurstSeconds, body.ChunkSize)
		_ = ses.Send(sessions.Command{Type: "rate", Rate: body.Rate, Mode: mode, ChunkSize: body.ChunkSize})
	case "interval":
		ses.SetInterval(body.IntervalMS)
		_ = ses.Send(sessions.Command{Type: "interval", IntervalMS: body.IntervalMS})
	case "echo_mode":
		ses.SetEchoMode(body.Mode)
		_ = ses.Send(sessions.Command{Type: "mode", Mode: body.Mode})
	case "inject":
		status := body.HTTPStatus
		if status <= 0 {
			status = 500
		}
		mode := body.FaultMode
		if mode == "" {
			mode = "sse_error"
		}
		spec := &sessions.FaultSpec{
			HTTPStatus: status, Code: body.Code, Type: body.Type, Message: body.Message,
			Param: body.Param, RawBody: body.RawBody, RawMode: body.RawMode,
			DelayMS: body.DelayMS, AfterChunks: body.AfterChunks, Mode: mode,
		}
		if body.AfterChunks > 0 {
			// chunk-count trigger is evaluated by the echo loop
			ses.SetPendingFault(spec)
			params += " after_chunks=" + strconv.Itoa(body.AfterChunks)
		} else {
			ses.InjectFault(spec)
		}
	default:
		writeErr(w, 400, "unknown action: "+body.Action, "bad_request")
		return
	}

	s.deps.Audit.Log(auditEntry(r, auditAction, id, params))
	writeJSON(w, 200, map[string]any{"ok": true, "state": string(ses.State()), "mode": string(ses.Mode())})
}

func summarizeControl(b struct {
	Action      string `json:"action"`
	Text        string `json:"text"`
	Rate        int64  `json:"rate"`
	Mode        string `json:"mode"`
	IntervalMS  int    `json:"interval_ms"`
	ChunkSize   int    `json:"chunk_size"`
	HTTPStatus  int    `json:"http_status"`
	Code        string `json:"code"`
	Type        string `json:"type"`
	Message     string `json:"message"`
	Param       string `json:"param"`
	RawBody     string `json:"raw_body"`
	RawMode     bool   `json:"raw_mode"`
	DelayMS     int    `json:"delay_ms"`
	AfterChunks int    `json:"after_chunks"`
	FaultMode   string `json:"fault_mode"`
}) string {
	var parts []string
	if b.HTTPStatus > 0 {
		parts = append(parts, "http="+strconv.Itoa(b.HTTPStatus))
	}
	if b.Code != "" {
		parts = append(parts, "code="+b.Code)
	}
	if b.Type != "" {
		parts = append(parts, "type="+b.Type)
	}
	if b.Message != "" {
		parts = append(parts, "message="+b.Message)
	}
	if b.Rate > 0 {
		parts = append(parts, "rate="+strconv.FormatInt(b.Rate, 10))
	}
	if b.IntervalMS > 0 {
		parts = append(parts, "interval_ms="+strconv.Itoa(b.IntervalMS))
	}
	if b.Text != "" {
		parts = append(parts, "text="+truncateStr(b.Text, 80))
	}
	if b.DelayMS > 0 {
		parts = append(parts, "delay_ms="+strconv.Itoa(b.DelayMS))
	}
	if b.FaultMode != "" {
		parts = append(parts, "mode="+b.FaultMode)
	}
	return strings.Join(parts, " ")
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func auditEntry(r *http.Request, action, session, params string) audit.Entry {
	return audit.Entry{Admin: adminUser(r), Session: session, Action: action, Params: params, IP: clientIP(r)}
}
