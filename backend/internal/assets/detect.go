package assets

import (
	"bytes"
	"net/http"
)

func httpDetect(buf []byte) string {
	if len(buf) == 0 {
		return "application/octet-stream"
	}
	if bytes.HasPrefix(buf, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png"
	}
	if bytes.HasPrefix(buf, []byte("\xFF\xD8\xFF")) {
		return "image/jpeg"
	}
	if bytes.HasPrefix(buf, []byte("GIF87a")) || bytes.HasPrefix(buf, []byte("GIF89a")) {
		return "image/gif"
	}
	if bytes.HasPrefix(buf, []byte("RIFF")) && bytes.Contains(buf[:12], []byte("WEBP")) {
		return "image/webp"
	}
	if bytes.HasPrefix(buf, []byte("%PDF")) {
		return "application/pdf"
	}
	if t := http.DetectContentType(buf); t != "" {
		return t
	}
	return "application/octet-stream"
}
