// Package vlog implements Vibe Coding white-box audit logging.
// It records every request lifecycle stage to disk under logs/:
//   logs/audit-dev-YYYY-MM-DD.log
//   logs/app-dev-YYYY-MM-DD.log
//   logs/frontend-dev-YYYY-MM-DD.log
//   logs/error-YYYY-MM-DD.log
// Non-technical reviewers can trace any request from arrival to exit using TraceID.
package vlog

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type contextKey string

const traceIDKey contextKey = "epic_trace_id"

type Logger struct {
	mu      sync.Mutex
	dir     string
	curDay  string
	auditF  *os.File
	appF    *os.File
	frontF  *os.File
	errF    *os.File
}

var (
	instance *Logger
	once     sync.Once
)

// Init initializes the disk logger in the target logs directory.
func Init(targetDir string) *Logger {
	once.Do(func() {
		if targetDir == "" {
			targetDir = "logs"
		}
		// If running from backend/ or project root, resolve cleanly
		if !filepath.IsAbs(targetDir) {
			if _, err := os.Stat("../logs"); err == nil {
				targetDir = "../logs"
			}
		}
		_ = os.MkdirAll(targetDir, 0o755)
		instance = &Logger{dir: targetDir}
		instance.rotateLocked(time.Now().Format("2006-01-02"))
	})
	return instance
}

func L() *Logger {
	if instance == nil {
		instance = Init("logs")
	}
	return instance
}

func (l *Logger) rotateLocked(day string) {
	if l.curDay == day && l.auditF != nil {
		return
	}
	if l.auditF != nil {
		_ = l.auditF.Close()
		_ = l.appF.Close()
		_ = l.frontF.Close()
		_ = l.errF.Close()
	}
	l.curDay = day
	_ = os.MkdirAll(l.dir, 0o755)
	l.auditF, _ = os.OpenFile(filepath.Join(l.dir, fmt.Sprintf("audit-dev-%s.log", day)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	l.appF, _ = os.OpenFile(filepath.Join(l.dir, fmt.Sprintf("app-dev-%s.log", day)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	l.frontF, _ = os.OpenFile(filepath.Join(l.dir, fmt.Sprintf("frontend-dev-%s.log", day)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	l.errF, _ = os.OpenFile(filepath.Join(l.dir, fmt.Sprintf("error-%s.log", day)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func (l *Logger) writeAudit(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.rotateLocked(now.Format("2006-01-02"))
	if l.auditF != nil {
		ts := now.Format("2006-01-02 15:04:05.000")
		_, _ = fmt.Fprintf(l.auditF, "%s %s\n", ts, line)
	}
}

func (l *Logger) writeApp(level, msg string, kvs ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.rotateLocked(now.Format("2006-01-02"))
	if l.appF != nil {
		ts := now.Format(time.RFC3339)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("time=%s level=%s msg=%q", ts, level, msg))
		for i := 0; i < len(kvs)-1; i += 2 {
			b.WriteString(fmt.Sprintf(" %v=%v", kvs[i], kvs[i+1]))
		}
		b.WriteString("\n")
		_, _ = l.appF.WriteString(b.String())
	}
}

func (l *Logger) writeFrontend(data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.rotateLocked(now.Format("2006-01-02"))
	if l.frontF != nil {
		ts := now.Format("2006-01-02 15:04:05.000")
		_, _ = fmt.Fprintf(l.frontF, "%s %s\n", ts, string(data))
	}
}

func (l *Logger) writeError(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.rotateLocked(now.Format("2006-01-02"))
	if l.errF != nil {
		ts := now.Format("2006-01-02 15:04:05.000")
		_, _ = fmt.Fprintf(l.errF, "%s %s\n", ts, line)
	}
}

// ---------------- Context TraceID ----------------

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

func TraceID(ctx context.Context) string {
	if ctx == nil {
		return "tr_" + shortID()
	}
	if v, ok := ctx.Value(traceIDKey).(string); ok && v != "" {
		return v
	}
	return "tr_" + shortID()
}

func TraceIDFromRequest(r *http.Request) string {
	if r == nil {
		return "tr_" + shortID()
	}
	if tid := r.Header.Get("X-Request-Id"); tid != "" {
		return tid
	}
	if tid := r.Header.Get("X-Trace-Id"); tid != "" {
		return tid
	}
	if tp := r.Header.Get("traceparent"); tp != "" {
		parts := strings.Split(tp, "-")
		if len(parts) >= 2 && parts[1] != "" {
			return parts[1]
		}
	}
	return "req_epic_" + shortID()
}

func shortID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}

// ---------------- 白话审计链路各阶段记录 ----------------

// 阶段一：请求到达
func RequestArrived(traceID, method, path, ip, client, paramsSummary string) {
	line := fmt.Sprintf("[请求到达] traceId=%s 入口=%s %s 来源=%s/%s 关键参数=%s 说明=请求已进入EpicAI系统，准备进入中间件校验",
		traceID, method, path, ip, client, sanitize(paramsSummary))
	L().writeAudit(line)
}

// 阶段二：中间件 / 拦截器检查
func MiddlewareAuth(traceID, layer string, pass bool, reason string) {
	res := "通过"
	if !pass {
		res = "拒绝"
	}
	line := fmt.Sprintf("[中间件-鉴权] traceId=%s 层=%s 结果=%s 原因=%s", traceID, layer, res, reason)
	L().writeAudit(line)
	if !pass {
		L().writeError(line)
	}
}

func MiddlewareRateLimit(traceID, layer string, active, limit int, pass bool, reason string) {
	res := "通过"
	if !pass {
		res = "拒绝"
	}
	line := fmt.Sprintf("[中间件-限流] traceId=%s 层=%s 当前活跃=%d 阈值=%d 结果=%s 原因=%s",
		traceID, layer, active, limit, res, reason)
	L().writeAudit(line)
	if !pass {
		L().writeError(line)
	}
}

func MiddlewareCORS(traceID, origin, mode string, pass bool) {
	res := "放行"
	if !pass {
		res = "拦截"
	}
	line := fmt.Sprintf("[中间件-CORS] traceId=%s 来源域=%s 模式=%s 结果=%s", traceID, origin, mode, res)
	L().writeAudit(line)
}

// 阶段三：被拦截 / 被拒绝（未进入业务方法，重点）
func RequestBlocked(traceID, layer, reason string, status int, businessImpact, suggestion string) {
	line := fmt.Sprintf("[请求被拦截] traceId=%s 拦截层=%s 结果=拒绝 原因=%s 返回状态码=%d 业务影响=%s 建议=%s 明确标注=请求未进入业务逻辑",
		traceID, layer, reason, status, businessImpact, suggestion)
	L().writeAudit(line)
	L().writeError(line)
}

// 阶段四：业务方法执行
func BizEntry(traceID, step, desc, input string) {
	line := fmt.Sprintf("[业务入口] traceId=%s step=%s 说明=%s 关键输入=%s",
		traceID, step, desc, sanitize(input))
	L().writeAudit(line)
}

// 数据库白话审计
func DBAudit(traceID, intent, condition, result string, affectedRows int64) {
	line := fmt.Sprintf("[数据库操作] traceId=%s 意图=%s 条件=%s 结果=%s 影响行数=%d",
		traceID, intent, condition, result, affectedRows)
	L().writeAudit(line)
}

// 阶段五：响应返回
func ResponseSent(traceID, step string, status int, dataSummary, duration, result, businessImpact string) {
	line := fmt.Sprintf("[响应返回] traceId=%s step=%s 状态码=%d 响应数据=%s 耗时=%s 结果=%s 业务影响=%s",
		traceID, step, status, sanitize(dataSummary), duration, result, businessImpact)
	L().writeAudit(line)
}

// 阶段六：异常与错误处理
func ErrorOccurred(traceID, stage, errType, reason string, status int, businessImpact string, handled bool) {
	handledStr := "否"
	if handled {
		handledStr = "是（已向调用方返回友好提示）"
	}
	line := fmt.Sprintf("[异常] traceId=%s 发生阶段=%s 异常类型=%s 原因=%s 返回状态码=%d 业务影响=%s 是否已处理=%s",
		traceID, stage, errType, reason, status, businessImpact, handledStr)
	L().writeAudit(line)
	L().writeError(line)
}

// 前端上报落盘
func RecordFrontendLog(raw []byte) {
	L().writeFrontend(raw)
}

func AppLog(level, msg string, kvs ...any) {
	L().writeApp(level, msg, kvs...)
}

func sanitize(s string) string {
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// ReadRecentAudit returns the most recent n lines from today's audit log.
func ReadRecentAudit(n int) ([]string, error) {
	if n <= 0 {
		n = 200
	}
	day := time.Now().Format("2006-01-02")
	path := filepath.Join(L().dir, fmt.Sprintf("audit-dev-%s.log", day))
	return tailLines(path, n)
}

// ReadRecentFrontend returns the most recent n lines from today's frontend log.
func ReadRecentFrontend(n int) ([]string, error) {
	if n <= 0 {
		n = 200
	}
	day := time.Now().Format("2006-01-02")
	path := filepath.Join(L().dir, fmt.Sprintf("frontend-dev-%s.log", day))
	return tailLines(path, n)
}

func tailLines(path string, n int) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	rawLines := strings.Split(string(data), "\n")
	var lines []string
	for _, l := range rawLines {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
