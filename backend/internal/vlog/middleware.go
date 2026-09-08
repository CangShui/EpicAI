package vlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("hijack unsupported")
}

// HTTPMiddleware wraps the root router to provide end-to-end trace logging.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		traceID := TraceIDFromRequest(r)

		// Set header on outgoing response
		w.Header().Set("X-Request-Id", traceID)
		w.Header().Set("X-Trace-Id", traceID)

		ctx := WithTraceID(r.Context(), traceID)
		r = r.WithContext(ctx)

		// Handle log reporting from frontend directly
		if r.Method == http.MethodPost && (r.URL.Path == "/admin/api/logs/report" || r.URL.Path == "/api/logs/report") {
			handleFrontendReport(w, r, traceID)
			return
		}

		clientIP := r.Header.Get("X-Forwarded-For")
		if clientIP == "" {
			clientIP = r.RemoteAddr
		}
		clientIP = strings.Split(clientIP, ",")[0]
		clientIP = strings.TrimSpace(clientIP)

		paramsSummary := "query=" + r.URL.RawQuery
		if r.URL.RawQuery == "" {
			paramsSummary = "none"
		}
		if r.ContentLength > 0 {
			paramsSummary += fmt.Sprintf(",content_len=%d", r.ContentLength)
		}

		RequestArrived(traceID, r.Method, r.URL.Path, clientIP, r.UserAgent(), paramsSummary)

		rec := &responseRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)

		dur := time.Since(start).String()
		resStr := "成功"
		impact := "请求已正常完成"
		if rec.status >= 400 {
			resStr = "异常/失败"
			impact = fmt.Sprintf("调用端收到HTTP %d状态码，未能正常完成业务", rec.status)
		}

		ResponseSent(traceID, r.URL.Path, rec.status, fmt.Sprintf("bytes=%d", rec.bytes), dur, resStr, impact)
	})
}

func handleFrontendReport(w http.ResponseWriter, r *http.Request, traceID string) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, `{"error":"read_failed"}`, 400)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err == nil {
		if _, ok := payload["traceId"]; !ok || payload["traceId"] == "" {
			payload["traceId"] = traceID
		}
		if _, ok := payload["serverTime"]; !ok {
			payload["serverTime"] = time.Now().Format(time.RFC3339)
		}
		if enriched, err := json.Marshal(payload); err == nil {
			b = enriched
		}
	}
	RecordFrontendLog(b)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","logged":true}`))
}
