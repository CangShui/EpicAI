// Package fault builds error payloads and applies the different failure modes.
package fault

import (
	"encoding/json"
	"net/http"

	"github.com/epicai/epicai/backend/internal/sessions"
)

// OpenAIError is the standard OpenAI error envelope.
type OpenAIError struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   any    `json:"param"`
	Code    any    `json:"code"`
}

// Presets are the built-in error templates required by the specification.
var Presets = []sessions.FaultSpec{
	preset(400, "bad_request", "invalid_request_error", "Bad request"),
	preset(401, "invalid_api_key", "invalid_request_error", "Incorrect API key provided"),
	preset(403, "forbidden", "invalid_request_error", "Forbidden"),
	preset(404, "model_not_found", "invalid_request_error", "The model does not exist"),
	preset(408, "timeout", "timeout_error", "Request timeout"),
	preset(409, "conflict", "conflict_error", "Conflict"),
	preset(413, "payload_too_large", "invalid_request_error", "Payload too large"),
	preset(422, "unprocessable_entity", "invalid_request_error", "Unprocessable entity"),
	preset(429, "rate_limit_exceeded", "rate_limit_error", "Rate limit exceeded"),
	preset(500, "internal_server_error", "server_error", "Internal server error"),
	preset(502, "bad_gateway", "server_error", "Bad gateway"),
	preset(503, "service_unavailable", "server_error", "Service unavailable"),
	preset(504, "gateway_timeout", "server_error", "Gateway timeout"),
}

func preset(status int, code, typ, msg string) sessions.FaultSpec {
	return sessions.FaultSpec{HTTPStatus: status, Code: code, Type: typ, Message: msg, Mode: "http_error"}
}

func ModelNotFound(model string) sessions.FaultSpec {
	return sessions.FaultSpec{
		HTTPStatus: http.StatusNotFound,
		Code:       "model_not_found",
		Type:       "invalid_request_error",
		Message:    "The model `" + model + "` does not exist or you do not have access to it.",
		Mode:       "http_error",
	}
}

func ModelDisabled(model string) sessions.FaultSpec {
	return sessions.FaultSpec{
		HTTPStatus: http.StatusNotFound,
		Code:       "model_disabled",
		Type:       "invalid_request_error",
		Message:    "The model `" + model + "` is disabled.",
		Mode:       "http_error",
	}
}

func PayloadTooLarge(limit int64) sessions.FaultSpec {
	return sessions.FaultSpec{
		HTTPStatus: http.StatusRequestEntityTooLarge,
		Code:       "payload_too_large",
		Type:       "invalid_request_error",
		Message:    "Request payload exceeds the maximum allowed size.",
		Mode:       "http_error",
	}
}

func Unauthorized(reason string) sessions.FaultSpec {
	return sessions.FaultSpec{
		HTTPStatus: http.StatusUnauthorized,
		Code:       reason,
		Type:       "invalid_request_error",
		Message:    "Invalid or missing API key.",
		Mode:       "http_error",
	}
}

// Body renders the response body for a fault spec. Raw mode returns the
// administrator supplied JSON byte-for-byte.
func Body(f *sessions.FaultSpec) ([]byte, string) {
	if f == nil {
		return nil, "application/json"
	}
	if f.RawMode && f.RawBody != "" {
		return []byte(f.RawBody), "application/json"
	}
	code := any(f.Code)
	if code == "" {
		code = nil
	}
	if f.HTTPStatus == 429 && f.Code == "6004" {
		// numeric custom code must be preserved as a number
		if n, err := parseInt(f.Code); err == nil {
			code = n
		}
	} else if n, err := parseInt(f.Code); err == nil {
		code = n
	}
	e := OpenAIError{Error: ErrorBody{
		Message: f.Message,
		Type:    f.Type,
		Param:   nil,
		Code:    code,
	}}
	if f.Param != "" {
		e.Error.Param = f.Param
	}
	b, _ := json.Marshal(e)
	return b, "application/json"
}

func parseInt(s string) (int, error) {
	n := 0
	sign := 1
	i := 0
	if s == "" {
		return 0, errNotNumber
	}
	if s[0] == '-' {
		sign = -1
		i = 1
	}
	if i >= len(s) {
		return 0, errNotNumber
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errNotNumber
		}
		n = n*10 + int(s[i]-'0')
	}
	return sign * n, nil
}

var errNotNumber = &parseErr{}

type parseErr struct{}

func (e *parseErr) Error() string { return "not a number" }

// MalformedChunk produces an intentionally invalid SSE payload.
func MalformedChunk() string {
	return "data: {\"id\":\"chatcmpl_broken\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":"
}
