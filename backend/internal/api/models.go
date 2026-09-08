package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/epicai/epicai/backend/internal/adapters/openai"
	"github.com/epicai/epicai/backend/internal/storage"
)

type ModelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	// EpicAI extensions (documented, additive)
	DisplayName string `json:"display_name,omitempty"`
	Behavior    string `json:"behavior,omitempty"`
}

type ModelList struct {
	Object string        `json:"object"`
	Data   []ModelObject `json:"data"`
}

// HandleModels serves GET /v1/models and GET /v1/models/{id}.
func (s *Server) HandleModels(w http.ResponseWriter, r *http.Request) {
	if openai.ApplyCORS(w, r) {
		return
	}
	if _, ok, _ := s.resolveKey(w, r); !ok {
		return
	}
	id := r.URL.Path[len("/v1/models"):]
	id = trimSlash(id)

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonErr(w, 405, "method_not_allowed", "invalid_request_error", "Only GET is supported")
		return
	}

	if id != "" {
		m, err := s.deps.Store.GetModel(r.Context(), id)
		if err != nil || m == nil || !m.Enabled {
			jsonErr(w, 404, "model_not_found", "invalid_request_error", "The model `"+id+"` does not exist")
			return
		}
		writeJSON(w, 200, toObject(m))
		return
	}

	models, err := s.deps.Store.ListEnabledModels(r.Context())
	if err != nil {
		jsonErr(w, 500, "internal_error", "server_error", "Failed to list models")
		return
	}
	sort.Slice(models, func(i, j int) bool { return models[i].CreatedAt.Before(models[j].CreatedAt) })
	out := ModelList{Object: "list", Data: make([]ModelObject, 0, len(models))}
	for i := range models {
		out.Data = append(out.Data, toObject(&models[i]))
	}
	writeJSON(w, 200, out)
}

func toObject(m *storage.Model) ModelObject {
	return ModelObject{
		ID: m.ModelID, Object: "model", Created: m.CreatedAt.Unix(), OwnedBy: "epicai",
		DisplayName: m.DisplayName, Behavior: string(m.Behavior),
	}
}

func trimSlash(s string) string {
	for len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		jsonErr(w, 500, "serialization_error", "server_error", "Failed to serialize response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-EpicAI-Version", Version)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

const Version = "1.0.0"

var startTime = time.Now()
