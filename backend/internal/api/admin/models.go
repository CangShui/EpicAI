package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/epicai/epicai/backend/internal/events"
	"github.com/epicai/epicai/backend/internal/storage"
)

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.deps.Store.ListModels(r.Context())
		if err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		writeJSON(w, 200, list)
	case http.MethodPost:
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, 400, "cannot read body", "bad_request")
			return
		}
		var m storage.Model
		if err := json.Unmarshal(bodyBytes, &m); err != nil {
			writeErr(w, 400, "invalid json", "bad_request")
			return
		}
		if m.ModelID == "" {
			writeErr(w, 400, "model_id is required", "bad_request")
			return
		}
		if m.DisplayName == "" {
			m.DisplayName = m.ModelID
		}
		if m.Behavior == "" {
			m.Behavior = storage.BehaviorInfiniteEcho
		}
		var rawMap map[string]any
		_ = json.Unmarshal(bodyBytes, &rawMap)
		_, hasDef := rawMap["default_echo_interval_ms"]
		_, hasEcho := rawMap["echo_interval_ms"]
		if !hasDef && !hasEcho {
			m.EchoIntervalMS = 500
		}
		if m.ProtocolMode == "" {
			m.ProtocolMode = "openai"
		}
		m.Enabled = true
		if err := s.deps.Store.CreateModel(r.Context(), &m); err != nil {
			writeErr(w, 409, err.Error(), "conflict")
			return
		}
		s.deps.Audit.Log(auditEntry(r, "CREATE_MODEL", "", m.ModelID))
		s.deps.Bus.Publish(events.Message{Type: events.ModelChanged, Data: map[string]any{"action": "create", "model": m.ModelID}})
		writeJSON(w, 201, m)
	default:
		writeErr(w, 405, "method not allowed", "")
	}
}

func (s *Server) handleModelDetail(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/api/models/"), "/")
	if rest == "" {
		writeErr(w, 400, "model id required", "bad_request")
		return
	}
	parts := strings.Split(rest, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch r.Method {
	case http.MethodGet:
		m, err := s.deps.Store.GetModel(r.Context(), id)
		if err != nil || m == nil {
			writeErr(w, 404, "model not found", "not_found")
			return
		}
		writeJSON(w, 200, m)
	case http.MethodPut, http.MethodPatch:
		m, err := s.deps.Store.GetModel(r.Context(), id)
		if err != nil || m == nil {
			writeErr(w, 404, "model not found", "not_found")
			return
		}
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeErr(w, 400, "invalid json", "bad_request")
			return
		}
		applyModelPatch(m, patch)
		if err := s.deps.Store.UpdateModel(r.Context(), m); err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		s.deps.Audit.Log(auditEntry(r, "UPDATE_MODEL", "", id))
		s.deps.Bus.Publish(events.Message{Type: events.ModelChanged, Data: map[string]any{"action": "update", "model": id}})
		writeJSON(w, 200, m)
	case http.MethodDelete:
		if err := s.deps.Store.DeleteModel(r.Context(), id); err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		s.deps.Audit.Log(auditEntry(r, "DELETE_MODEL", "", id))
		s.deps.Bus.Publish(events.Message{Type: events.ModelChanged, Data: map[string]any{"action": "delete", "model": id}})
		writeJSON(w, 200, map[string]any{"ok": true})
	case http.MethodPost:
		// clone
		if action != "clone" {
			writeErr(w, 404, "unknown action", "not_found")
			return
		}
		src, err := s.deps.Store.GetModel(r.Context(), id)
		if err != nil || src == nil {
			writeErr(w, 404, "model not found", "not_found")
			return
		}
		var body struct {
			ModelID string `json:"model_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.ModelID == "" {
			body.ModelID = id + "-copy"
		}
		clone := *src
		clone.ModelID = body.ModelID
		clone.DisplayName = src.DisplayName + " (copy)"
		clone.CreatedAt = time.Now()
		if err := s.deps.Store.CreateModel(r.Context(), &clone); err != nil {
			writeErr(w, 409, err.Error(), "conflict")
			return
		}
		s.deps.Audit.Log(auditEntry(r, "CLONE_MODEL", "", body.ModelID))
		writeJSON(w, 201, clone)
	default:
		writeErr(w, 405, "method not allowed", "")
	}
}

func applyModelPatch(m *storage.Model, p map[string]any) {
	for k, v := range p {
		switch k {
		case "display_name":
			if sv, ok := v.(string); ok {
				m.DisplayName = sv
			}
		case "model_id":
			if sv, ok := v.(string); ok && sv != "" {
				m.ModelID = sv
			}
		case "enabled":
			m.Enabled = toBool(v)
		case "behavior":
			if sv, ok := v.(string); ok && sv != "" {
				m.Behavior = storage.ModelBehavior(sv)
			}
		case "default_echo_interval_ms", "echo_interval_ms":
			m.EchoIntervalMS = int(toInt64(v))
		case "protocol_mode":
			if sv, ok := v.(string); ok {
				m.ProtocolMode = sv
			}
		case "description":
			if sv, ok := v.(string); ok {
				m.Description = sv
			}
		case "static_response":
			if sv, ok := v.(string); ok {
				m.StaticResponse = sv
			}
		case "error_status":
			m.ErrorStatus = int(toInt64(v))
		case "error_code":
			if sv, ok := v.(string); ok {
				m.ErrorCode = sv
			}
		case "error_type":
			if sv, ok := v.(string); ok {
				m.ErrorType = sv
			}
		case "error_message":
			if sv, ok := v.(string); ok {
				m.ErrorMessage = sv
			}
		case "token_rate":
			m.TokenRate = toInt64(v)
		case "echo_content_mode":
			if sv, ok := v.(string); ok {
				m.EchoContentMode = sv
			}
		case "max_echo_count":
			m.MaxEchoCount = int(toInt64(v))
		case "enable_agent":
			m.EnableAgent = toBool(v)
		case "subagent_count":
			m.SubagentCount = int(toInt64(v))
		case "max_token_chunk":
			m.MaxTokenChunk = int(toInt64(v))
		case "metadata":
			if mv, ok := v.(map[string]any); ok {
				m.Metadata = mv
			}
		}
	}
}


