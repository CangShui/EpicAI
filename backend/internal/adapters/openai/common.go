// Package openai provides the shared OpenAI protocol surface: identifiers,
// error envelopes, CORS and the streaming sink abstraction used by adapters.
package openai

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/engine/fault"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/google/uuid"
)

func ChatID() string  { return "chatcmpl_epic_" + shortID() }
func RespID() string  { return "resp_epic_" + shortID() }
func MsgID() string   { return "msg_epic_" + shortID() }
func shortID() string { return strings.ReplaceAll(uuid.NewString(), "-", "")[:20] }

// WriteError writes an OpenAI style error response.
func WriteError(w http.ResponseWriter, status int, code, typ, msg string) {
	WriteSpec(w, &sessions.FaultSpec{HTTPStatus: status, Code: code, Type: typ, Message: msg, Mode: "http_error"})
}

// WriteSpec writes a fault spec as an HTTP response, honoring raw mode.
func WriteSpec(w http.ResponseWriter, spec *sessions.FaultSpec) {
	body, ctype := fault.Body(spec)
	status := spec.HTTPStatus
	if status <= 0 {
		status = 500
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-EpicAI-Error", "true")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ApplyCORS implements the configured CORS policy.
func ApplyCORS(w http.ResponseWriter, r *http.Request) bool {
	rt := config.C().Runtime()
	switch rt.CORSMode {
	case config.CORSDisabled:
		if origin := r.Header.Get("Origin"); origin != "" {
			// Still answer preflight with 204 to avoid confusing clients.
		}
		// no CORS headers
	case config.CORSAllowAll:
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-Id,OpenAI-Beta,Accept")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id,X-Epic-Session-Id")
		w.Header().Set("Access-Control-Max-Age", "600")
	case config.CORSAllowList:
		origin := r.Header.Get("Origin")
		for _, allowed := range rt.CORSAllowOrigins {
			if allowed == "*" || strings.EqualFold(allowed, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-Id,OpenAI-Beta,Accept")
				w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id,X-Epic-Session-Id")
				break
			}
		}
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

type ErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   any    `json:"param"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func Marshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
