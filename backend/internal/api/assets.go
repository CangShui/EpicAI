package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/epicai/epicai/backend/internal/adapters/openai"
)

// HandleAssets serves /epic-assets/{asset_id}, used for image echo URLs.
func (s *Server) HandleAssets(w http.ResponseWriter, r *http.Request) {
	if openai.ApplyCORS(w, r) {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/epic-assets/"), "/")
	if id == "" {
		jsonErr(w, 404, "asset_not_found", "invalid_request_error", "Asset id is required")
		return
	}
	if s.deps.Assets == nil {
		jsonErr(w, 500, "storage_unavailable", "server_error", "Asset storage is not configured")
		return
	}
	path := s.deps.Assets.Path("asset", id)
	f, err := os.Open(path)
	if err != nil {
		// fall back to file store assets
		f2, err2 := os.Open(s.deps.Assets.Path("file", id))
		if err2 != nil {
			jsonErr(w, 404, "asset_not_found", "invalid_request_error", "Asset not found")
			return
		}
		f = f2
	}
	defer f.Close()
	info, _ := f.Stat()
	if info != nil {
		w.Header().Set("Content-Length", itoa(info.Size()))
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	w.Header().Set("Content-Type", detectContentType(path))
	w.WriteHeader(http.StatusOK)
	_, _ = copyBuffer(w, f)
}

func copyBuffer(w http.ResponseWriter, f *os.File) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			total += int64(n)
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
		}
		if err != nil {
			return total, nil
		}
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func detectContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(path, ".gif"):
		return "image/gif"
	case strings.HasSuffix(path, ".webp"):
		return "image/webp"
	case strings.HasSuffix(path, ".pdf"):
		return "application/pdf"
	}
	return "application/octet-stream"
}
