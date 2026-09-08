package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/epicai/epicai/backend/internal/auth"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/google/uuid"
)

// ---------------- API keys ----------------

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.deps.Store.ListKeys(r.Context())
		if err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		// Never return the key hash; only the fingerprint and masked form.
		out := make([]map[string]any, 0, len(list))
		for _, k := range list {
			out = append(out, map[string]any{
				"id": k.ID, "name": k.Name, "fingerprint": k.Fingerprint,
				"masked": auth.MaskFingerprint(k.Fingerprint), "enabled": k.Enabled,
				"created_at": k.CreatedAt, "last_used_at": k.LastUsedAt,
				"use_count": k.UseCount, "models": k.Models,
				"max_sessions": k.MaxSessions, "rate_limit": k.RateLimit,
			})
		}
		writeJSON(w, 200, out)
	case http.MethodPost:
		var body struct {
			Name        string   `json:"name"`
			Models      []string `json:"models"`
			MaxSessions int      `json:"max_sessions"`
			RateLimit   int64    `json:"rate_limit"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Name == "" {
			body.Name = "key-" + time.Now().Format("20060102-150405")
		}
		plain, hash, fp, prefix, suffix := auth.GenerateKey()
		k := storage.APIKey{
			ID: "key_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16],
			Name: body.Name, KeyHash: hash, Fingerprint: fp, Prefix: prefix, Suffix: suffix,
			Enabled: true, CreatedAt: time.Now(), Models: body.Models,
			MaxSessions: body.MaxSessions, RateLimit: body.RateLimit,
		}
		if err := s.deps.Store.CreateKey(r.Context(), &k); err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		s.deps.Audit.Log(auditEntry(r, "CREATE_API_KEY", "", k.ID))
		// The plaintext key is only ever returned once, at creation time.
		writeJSON(w, 201, map[string]any{
			"id": k.ID, "name": k.Name, "key": plain, "fingerprint": k.Fingerprint,
			"masked": auth.MaskFingerprint(k.Fingerprint), "enabled": k.Enabled,
			"created_at": k.CreatedAt, "models": k.Models,
			"max_sessions": k.MaxSessions, "rate_limit": k.RateLimit,
		})
	default:
		writeErr(w, 405, "method not allowed", "")
	}
}

func (s *Server) handleKeyDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/api/keys/"), "/")
	if id == "" {
		writeErr(w, 400, "key id required", "bad_request")
		return
	}
	list, err := s.deps.Store.ListKeys(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error(), "internal")
		return
	}
	var target *storage.APIKey
	for i := range list {
		if list[i].ID == id || list[i].Fingerprint == id {
			target = &list[i]
			break
		}
	}
	if target == nil {
		writeErr(w, 404, "key not found", "not_found")
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeErr(w, 400, "invalid json", "bad_request")
			return
		}
		for k, v := range patch {
			switch k {
			case "name":
				if sv, ok := v.(string); ok {
					target.Name = sv
				}
			case "enabled":
				target.Enabled = toBool(v)
			case "models":
				if arr, ok := v.([]any); ok {
					out := make([]string, 0, len(arr))
					for _, a := range arr {
						if sv, ok := a.(string); ok {
							out = append(out, sv)
						}
					}
					target.Models = out
				}
			case "max_sessions":
				target.MaxSessions = int(toInt64(v))
			case "rate_limit":
				target.RateLimit = toInt64(v)
			}
		}
		if err := s.deps.Store.UpdateKey(r.Context(), target); err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		s.deps.Audit.Log(auditEntry(r, "UPDATE_API_KEY", "", id))
		writeJSON(w, 200, map[string]any{
			"id": target.ID, "name": target.Name, "fingerprint": target.Fingerprint,
			"masked": auth.MaskFingerprint(target.Fingerprint), "enabled": target.Enabled,
		})
	case http.MethodDelete:
		if err := s.deps.Store.DeleteKey(r.Context(), target.ID); err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		s.deps.Audit.Log(auditEntry(r, "DELETE_API_KEY", "", id))
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, "method not allowed", "")
	}
}

// ---------------- files ----------------

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := queryInt(r, "limit", 200)
		offset := queryInt(r, "offset", 0)
		files, err := s.deps.Store.ListFiles(r.Context(), limit, offset)
		if err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		out := make([]map[string]any, 0, len(files))
		for _, f := range files {
			out = append(out, map[string]any{
				"id": f.ID, "filename": f.Filename, "mime_type": f.MimeType,
				"bytes": f.Bytes, "sha256": f.SHA256, "purpose": f.Purpose,
				"uploaded_at": f.UploadedAt, "session_id": f.SessionID,
				"key_fingerprint": f.KeyFingerprint,
				"preview_url": "/epic-assets/" + f.ID,
				"is_image": strings.HasPrefix(f.MimeType, "image/"),
			})
		}
		writeJSON(w, 200, map[string]any{"files": out, "max_file_size": config.C().Runtime().MaxFileSize})
	default:
		writeErr(w, 405, "method not allowed", "")
	}
}

func (s *Server) handleFileDetail(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/api/files/"), "/")
	if rest == "" {
		writeErr(w, 400, "file id required", "bad_request")
		return
	}
	parts := strings.Split(rest, "/")
	id := parts[0]

	switch r.Method {
	case http.MethodGet:
		f, err := s.deps.Store.GetFile(r.Context(), id)
		if err != nil || f == nil {
			writeErr(w, 404, "file not found", "not_found")
			return
		}
		writeJSON(w, 200, f)
	case http.MethodDelete:
		f, err := s.deps.Store.GetFile(r.Context(), id)
		if err != nil || f == nil {
			writeErr(w, 404, "file not found", "not_found")
			return
		}
		if f.StoragePath != "" {
			_ = os.Remove(f.StoragePath)
		}
		if err := s.deps.Store.DeleteFile(r.Context(), id); err != nil {
			writeErr(w, 500, err.Error(), "internal")
			return
		}
		s.deps.Audit.Log(auditEntry(r, "DELETE_FILE", "", id))
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, "method not allowed", "")
	}
}
