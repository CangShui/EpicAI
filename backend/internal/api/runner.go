// Package api contains the OpenAI compatible HTTP surface of EpicAI.
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/epicai/epicai/backend/internal/adapters/openai"
	ochat "github.com/epicai/epicai/backend/internal/adapters/openai_chat"
	ored "github.com/epicai/epicai/backend/internal/adapters/openai_responses"
	"github.com/epicai/epicai/backend/internal/auth"
	"github.com/epicai/epicai/backend/internal/canonical"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/engine/echo"
	"github.com/epicai/epicai/backend/internal/engine/fault"
	"github.com/epicai/epicai/backend/internal/engine/scenario"
	"github.com/epicai/epicai/backend/internal/engine/stream"
	"github.com/epicai/epicai/backend/internal/events"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/epicai/epicai/backend/internal/tokenizer"
	"github.com/epicai/epicai/backend/internal/vlog"
)

// Deps wires the API layer to shared services.
type Deps struct {
	Store   storage.Store
	Manager *sessions.Manager
	Auth    *auth.Manager
	Bus     *events.Bus
	Log     *slog.Logger
	Assets  AssetStore
}

type AssetStore interface {
	Put(kind, filename, mimeType string, r io.Reader, maxBytes int64, sessionID string) (*AssetPutResult, error)
	Open(kind, id string) (io.ReadCloser, error)
	Path(kind, id string) string
	Root() string
	TotalBytes() int64
}

type AssetPutResult struct {
	ID       string
	Path     string
	Size     int64
	SHA256   string
	MimeType string
}

type Server struct {
	deps Deps
	// captured SSE streams for the admin raw inspector
	streams sync.Map // session_id -> *stream.Writer
}

func NewServer(d Deps) *Server {
	return &Server{deps: d}
}

func (s *Server) log() *slog.Logger { return s.deps.Log }

func (s *Server) StreamWriter(sid string) *stream.Writer {
	if v, ok := s.streams.Load(sid); ok {
		return v.(*stream.Writer)
	}
	return nil
}

// ---------------- request context ----------------

type reqCtx struct {
	Session   *sessions.Session
	Protocol  string
	Streaming bool
	Body      []byte
}

func (s *Server) clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		parts := strings.Split(xf, ",")
		return strings.TrimSpace(parts[0])
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return xr
	}
	if h, _, err := splitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func splitHostPort(s string) (string, string, error) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:], nil
		}
	}
	return s, "", errors.New("no port")
}

// sanitizeHeaders removes secrets before they are stored or displayed.
func sanitizeHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		lk := strings.ToLower(k)
		switch lk {
		case "authorization", "api-key", "x-api-key", "openai-api-key":
			if len(v) > 0 {
				out[k] = auth.Mask(v[0])
			}
		case "cookie", "set-cookie":
			out[k] = "***"
		default:
			out[k] = strings.Join(v, ", ")
		}
	}
	return out
}

// resolveKey authenticates the request and returns the key fingerprint.
func (s *Server) resolveKey(w http.ResponseWriter, r *http.Request) (string, bool, *storage.APIKey) {
	traceID := vlog.TraceID(r.Context())
	res := s.deps.Auth.Check(r.Context(), r.Header.Get("Authorization"))
	vlog.MiddlewareAuth(traceID, "API-Key鉴权", res.Allowed, res.Reason)

	if !res.Allowed {
		vlog.RequestBlocked(traceID, "API-Key鉴权", "API Key无效或缺失: "+res.Reason, res.Status, "请求被拦截，未进入业务逻辑", "请提供正确的Authorization: Bearer <key>请求头")
		openai.WriteError(w, res.Status, res.Reason, "invalid_request_error", "Invalid or missing API key")
		return "", false, nil
	}
	fp := ""
	var key *storage.APIKey
	if res.Key != nil {
		fp = res.Key.Fingerprint
		key = res.Key
		go func() {
			_ = s.deps.Store.TouchKey(context.Background(), res.Key.ID)
			vlog.DBAudit(traceID, "更新APIKey使用计数", "key_id="+res.Key.ID, "成功", 1)
		}()
	} else {
		raw := strings.TrimSpace(r.Header.Get("Authorization"))
		raw = strings.TrimPrefix(raw, "Bearer ")
		raw = strings.TrimPrefix(raw, "bearer ")
		if raw != "" {
			fp = auth.Fingerprint(raw)
		}
	}
	return fp, true, key
}

// resolveModel loads and validates the requested model.
func (s *Server) resolveModel(w http.ResponseWriter, name string) (*storage.Model, bool) {
	traceID := vlog.TraceID(context.Background())
	if name == "" {
		name = config.C().Static().DefaultModel
	}
	m, err := s.deps.Store.GetModel(context.Background(), name)
	vlog.DBAudit(traceID, "查询模型元数据", "model_id="+name, fmt.Sprintf("找到: %v", m != nil), 1)

	if err != nil || m == nil {
		if config.C().Runtime().UnknownModelBehavior == "fallback" {
			if fb, ferr := s.deps.Store.GetModel(context.Background(), config.C().Static().DefaultModel); ferr == nil {
				return fb, true
			}
		}
		vlog.RequestBlocked(traceID, "模型校验", "模型不存在: "+name, 404, "请求未进入业务逻辑", "请通过/v1/models查询支持的模型列表")
		openai.WriteSpec(w, ptrSpec(fault.ModelNotFound(name)))
		return nil, false
	}
	if !m.Enabled {
		vlog.RequestBlocked(traceID, "模型校验", "模型已禁用: "+name, 404, "请求未进入业务逻辑", "请在管理后台启用该模型或更换模型")
		openai.WriteSpec(w, ptrSpec(fault.ModelDisabled(name)))
		return nil, false
	}
	return m, true
}

func ptrSpec(f sessions.FaultSpec) *sessions.FaultSpec { return &f }

// admit enforces concurrency limits with the configured overload behavior.
func (s *Server) admit(w http.ResponseWriter, r *http.Request, ip, fp string, keyMax int, kill func()) bool {
	traceID := vlog.TraceID(r.Context())
	res := s.deps.Manager.Admit(ip, fp, keyMax)
	vlog.MiddlewareRateLimit(traceID, "并发限制", s.deps.Manager.ActiveCount(), config.C().Runtime().MaxActiveSessions, res.Allowed, res.Reason)

	if res.Allowed {
		return true
	}
	vlog.RequestBlocked(traceID, "并发控制", "并发限制超出: "+res.Reason, res.Status, "请求被限流拒绝，未进入业务逻辑", "请稍后重试或调大后台最大并发配置")

	switch res.Behavior {
	case config.OverloadQueue:
		// Wait for a free slot, up to 60 seconds, then fall through to reject.
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if s.deps.Manager.Admit(ip, fp, keyMax).Allowed {
				return true
			}
			select {
			case <-r.Context().Done():
				return false
			case <-time.After(200 * time.Millisecond):
			}
		}
		openai.WriteError(w, 429, "concurrency_limit_exceeded", "rate_limit_error", "EpicAI concurrent session limit exceeded (queue timeout)")
		return false
	case config.OverloadHang:
		<-r.Context().Done()
		return false
	case config.OverloadCustomError:
		body := config.C().Runtime().OverloadCustomBody
		code := config.C().Runtime().OverloadCustomCode
		if code <= 0 {
			code = 429
		}
		if body == "" {
			body = `{"error":{"message":"EpicAI overload","type":"rate_limit_error","code":"overload"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
		return false
	default: // reject
		openai.WriteError(w, 429, res.Code, "rate_limit_error", res.Message)
		return false
	}
}

// readBody enforces the configured max payload size (413 on overflow).
func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	traceID := vlog.TraceID(r.Context())
	maxBytes := config.C().Runtime().MaxFileSize
	if maxBytes <= 0 {
		maxBytes = 20 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) || strings.Contains(err.Error(), "too large") {
			vlog.RequestBlocked(traceID, "请求体大小校验", fmt.Sprintf("请求体积超过限制(%d字节)", maxBytes), 413, "请求体过大被拒，未进入业务逻辑", "请减小请求体或调大后台MaxFileSize配置")
			openai.WriteSpec(w, ptrSpec(fault.PayloadTooLarge(maxBytes)))
			return nil, false
		}
		vlog.RequestBlocked(traceID, "请求体读取", err.Error(), 400, "读取请求体失败，未进入业务逻辑", "请检查HTTP请求体格式")
		openai.WriteError(w, 400, "invalid_request_body", "invalid_request_error", "Could not read request body")
		return nil, false
	}
	if int64(len(body)) > maxBytes {
		vlog.RequestBlocked(traceID, "请求体大小校验", fmt.Sprintf("请求体积(%d)超过限制(%d)", len(body), maxBytes), 413, "请求体过大被拒，未进入业务逻辑", "请减小请求体或调大后台MaxFileSize配置")
		openai.WriteSpec(w, ptrSpec(fault.PayloadTooLarge(maxBytes)))
		return nil, false
	}
	return body, true
}

// persistMultimodalAssets extracts and saves any embedded base64 assets into the AssetStore.
func (s *Server) persistMultimodalAssets(conv *canonical.Conversation, sid, traceID string) {
	if s.deps.Assets == nil || conv == nil {
		return
	}
	maxBytes := config.C().Runtime().MaxFileSize
	if maxBytes <= 0 {
		maxBytes = 20 << 20
	}
	for i := range conv.Messages {
		for j := range conv.Messages[i].Parts {
			p := &conv.Messages[i].Parts[j]
			if p.Type == canonical.PartImage && p.ImageBase64 != "" && p.AssetID == "" {
				raw := strings.TrimSpace(p.ImageBase64)
				raw = strings.ReplaceAll(raw, " ", "")
				raw = strings.ReplaceAll(raw, "\n", "")
				raw = strings.ReplaceAll(raw, "\r", "")
				dec, err := base64.StdEncoding.DecodeString(raw)
				if err != nil {
					dec, err = base64.RawStdEncoding.DecodeString(raw)
				}
				if err == nil && len(dec) > 0 {
					ext := "png"
					if strings.Contains(p.MimeType, "jpeg") || strings.Contains(p.MimeType, "jpg") {
						ext = "jpg"
					} else if strings.Contains(p.MimeType, "webp") {
						ext = "webp"
					} else if strings.Contains(p.MimeType, "gif") {
						ext = "gif"
					}
					fn := fmt.Sprintf("image_%d.%s", time.Now().UnixNano(), ext)
					res, err := s.deps.Assets.Put("asset", fn, p.MimeType, bytes.NewReader(dec), maxBytes, sid)
					if err == nil && res != nil {
						p.AssetID = res.ID
						p.ImageURL = "/epic-assets/" + res.ID
						vlog.BizEntry(traceID, "保存多模态图片资源", "解析Base64图片并落盘AssetStore", "asset_id="+res.ID+fmt.Sprintf(" size=%d", res.Size))
						vlog.DBAudit(traceID, "新增Asset记录", "id="+res.ID+" session_id="+sid, "成功", 1)
					}
				}
			}
		}
	}
}

// logInput stores the incoming conversation in the event log.
func (s *Server) logInput(ses *sessions.Session, text string, tokens int) {
	_ = s.deps.Store.AppendEvents(context.Background(), []storage.Event{{
		SessionID: ses.ID,
		Seq:       ses.NextSeq(),
		Kind:      storage.EventInput,
		Role:      "user",
		Content:   text,
		Bytes:     len(text),
		Tokens:    tokens,
		CreatedAt: time.Now(),
	}})
	vlog.DBAudit(ses.RequestID, "写入用户输入事件", "session_id="+ses.ID, "成功", 1)
	s.deps.Bus.Publish(events.Message{
		Type: events.InputReceived, Session: ses.ID,
		Data: map[string]any{"content": truncate(text, 500), "tokens": tokens},
	})
}

// logOutput stores an outgoing assistant message.
func (s *Server) logOutput(ses *sessions.Session, text string, tokens int) {
	_ = s.deps.Store.AppendEvents(context.Background(), []storage.Event{{
		SessionID: ses.ID,
		Seq:       ses.NextSeq(),
		Kind:      storage.EventOutput,
		Role:      "assistant",
		Content:   text,
		Bytes:     len(text),
		Tokens:    tokens,
		CreatedAt: time.Now(),
	}})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------------- streaming orchestration ----------------

// streamRunner is the shared loop: it handles admin commands, echo generation,
// manual messages and fault injection for any protocol.
type streamRunner struct {
	srv  *Server
	ses  *sessions.Session
	sw   *stream.Writer
	conv *canonical.Conversation
	// emit writes one text delta in the protocol's framing
	emit func(ctx context.Context, delta string) error
	// finish writes the protocol's clean ending
	finish func(ctx context.Context, reason string) error
}

func (s *Server) newRunner(ses *sessions.Session, sw *stream.Writer, conv *canonical.Conversation,
	emit func(context.Context, string) error, finish func(context.Context, string) error) *streamRunner {
	return &streamRunner{srv: s, ses: ses, sw: sw, conv: conv, emit: emit, finish: finish}
}

// Run executes the session until it ends.
func (r *streamRunner) Run(ctx context.Context, model *storage.Model) {
	ses := r.ses
	defer func() {
		r.srv.streams.Delete(ses.ID)
		r.srv.deps.Manager.Unregister(ses.ID)
		if ses.State() != storage.StateEnded && ses.State() != storage.StateClientDisconnected {
			ses.Close(storage.StateEnded, ses.EndReason())
		}
		ses.Persist()
		vlog.BizEntry(ses.RequestID, "会话生命周期结束", "会话已关闭并注销", "session_id="+ses.ID+" reason="+ses.EndReason())
	}()

	// Publish stats periodically so the admin UI updates without polling storms.
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				ses.PublishState()
			case <-ses.Done():
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	switch model.Behavior {
	case storage.BehaviorStaticResponse:
		r.runStatic(ctx, model)
		return
	case storage.BehaviorHangForever:
		ses.SetState(storage.StateEchoing)
		<-ctx.Done()
		ses.Close(storage.StateClientDisconnected, "client_disconnected")
		return
	case storage.BehaviorConnectionDrop:
		ses.SetState(storage.StateEchoing)
		time.Sleep(50 * time.Millisecond)
		_ = r.sw.Disconnect()
		ses.Close(storage.StateEnded, "connection_drop_behavior")
		return
	case storage.BehaviorManualOnly:
		ses.SetState(storage.StateManual)
		ses.SetMode(storage.ModeManual)
		ses.PublishState()
		r.runManualOnly(ctx)
		return
	case storage.BehaviorScenario:
		r.runScenario(ctx, model)
		return
	case storage.BehaviorFiniteEcho:
		r.runEcho(ctx, model, true)
		return
	default:
		r.runEcho(ctx, model, false)
	}
}

func (r *streamRunner) runStatic(ctx context.Context, model *storage.Model) {
	text := model.StaticResponse
	if text == "" {
		text = r.conv.LastUserText()
	}
	_ = r.emit(ctx, text)
	r.ses.AddEcho()
	r.srv.logOutput(r.ses, text, tokenizer.Count(text))
	if r.ses.Streaming {
		_ = r.finish(ctx, "stop")
	}
	r.ses.Close(storage.StateEnded, "static_response")
}

func (r *streamRunner) runManualOnly(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			r.ses.Close(storage.StateClientDisconnected, "client_disconnected")
			return
		case <-r.ses.Done():
			return
		case cmd := <-r.ses.Commands():
			if done := r.handleCommand(ctx, cmd); done {
				return
			}
		}
	}
}

func (r *streamRunner) runScenario(ctx context.Context, model *storage.Model) {
	sc, err := r.srv.deps.Store.GetScenario(context.Background(), model.ScenarioID)
	if err != nil || sc == nil {
		r.runEcho(ctx, model, false)
		return
	}
	r.ses.SetState(storage.StateEchoing)
	r.ses.SetMode(storage.ModeScenario)
	runner := scenario.New(r.ses, r.sw, sc.Steps, r.conv)
	_ = runner.Run(ctx)
	r.ses.Close(storage.StateEnded, "scenario_complete")
}

// runEcho is the infinite (or finite) echo loop: the core EpicAI behavior.
func (r *streamRunner) runEcho(ctx context.Context, model *storage.Model, finite bool) {
	ses := r.ses
	ses.SetMode(storage.ModeEcho)
	ses.SetState(storage.StateEchoing)
	ses.PublishState()

	var n int64
	initInterval := echoInterval(ses)
	if initInterval <= 0 {
		initInterval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(initInterval)
	defer ticker.Stop()

	for {
		// 1. Admin commands have priority.
		select {
		case cmd := <-ses.Commands():
			if done := r.handleCommand(ctx, cmd); done {
				return
			}
			if ses.State() == storage.StateManual {
				if done := r.runManualPhase(ctx); done {
					return
				}
				if iv := echoInterval(ses); iv > 0 {
					ticker.Reset(iv)
				}
			}
			continue
		default:
		}

		// 2. Wait while paused (connection stays open, rate = 0).
		if ses.State() == storage.StatePaused {
			select {
			case cmd := <-ses.Commands():
				if done := r.handleCommand(ctx, cmd); done {
					return
				}
			case <-ctx.Done():
				ses.Close(storage.StateClientDisconnected, "client_disconnected")
				return
			case <-ses.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		// 3. Emit one echo unit.
		unit := echo.Streaming(r.conv, ses.EchoMode(), n)
		delta := unit.Prefix + unit.Text
		if delta != "" {
			tokens := tokenizer.Count(delta)
			if b := ses.Bucket(); b != nil {
				if !b.Wait(ctx, tokens) {
					ses.Close(storage.StateClientDisconnected, "client_disconnected")
					return
				}
			}
			ses.AddOutputTokens(tokens)
			if err := r.emit(ctx, delta); err != nil {
				ses.Close(storage.StateClientDisconnected, "client_disconnected")
				return
			}
			r.srv.logOutput(ses, unit.Text, tokens)
			r.srv.deps.Bus.Publish(events.Message{
				Type: events.ChunkSent, Session: ses.ID,
				Data: map[string]any{"echo_count": n + 1, "tokens": tokens},
			})
		}
		ses.AddEcho()
		n++

		// 4. Finite echo ends normally after maxEcho.
		if finite && model.MaxEchoCount > 0 && n >= int64(model.MaxEchoCount) {
			if ses.Streaming {
				_ = r.finish(ctx, "stop")
			}
			ses.Close(storage.StateEnded, "finite_echo_complete")
			return
		}

		// 5. Chunk-count based fault injection.
		if f := ses.PendingFault(); f != nil && f.AfterChunks > 0 {
			if n >= int64(f.AfterChunks) {
				vlog.BizEntry(ses.RequestID, "故障注入触发", fmt.Sprintf("达到指定Chunk数(%d)，注入故障", f.AfterChunks), f.Message)
				_ = r.sw.Fail(ctx, f)
				ses.Close(storage.StateEnded, "fault_injected")
				return
			}
		}

		// Resource limit protection (MaxEventsPerSession)
		maxEv := config.C().Runtime().MaxEventsPerSession
		if maxEv > 0 && n >= int64(maxEv) {
			action := config.C().Runtime().ResourceAction
			switch action {
			case config.ResourceDisconnect:
				_ = r.sw.Disconnect()
				ses.Close(storage.StateEnded, "resource_limit_exceeded")
				return
			case config.ResourceError:
				_ = r.sw.Fail(ctx, &sessions.FaultSpec{
					HTTPStatus: 429, Code: "resource_limit_exceeded", Type: "rate_limit_error",
					Message: "Session reached maximum allowed event count", Mode: "sse_error",
				})
				ses.Close(storage.StateEnded, "resource_limit_exceeded")
				return
			case config.ResourcePause:
				ses.SetState(storage.StatePaused)
				ses.PublishState()
				continue
			}
		}

		// 6. Pace the loop. In Unlimited mode the interval still applies as the
		// echo cadence, but a zero interval means "emit as fast as possible".
		iv := echoInterval(ses)
		if iv <= 0 {
			runtime.Gosched()
			if rt := config.C().Runtime(); rt.MaxEchoRate > 0 {
				minInterval := time.Duration(float64(time.Second) / rt.MaxEchoRate)
				select {
				case <-time.After(minInterval):
				case cmd := <-ses.Commands():
					if done := r.handleCommand(ctx, cmd); done {
						return
					}
					if ses.State() == storage.StateManual {
						if done := r.runManualPhase(ctx); done {
							return
						}
					}
				case <-ctx.Done():
					ses.Close(storage.StateClientDisconnected, "client_disconnected")
					return
				}
			} else {
				select {
				case cmd := <-ses.Commands():
					if done := r.handleCommand(ctx, cmd); done {
						return
					}
					if ses.State() == storage.StateManual {
						if done := r.runManualPhase(ctx); done {
							return
						}
					}
				default:
					time.Sleep(100 * time.Microsecond)
				}
			}
			continue
		}
		ticker.Reset(iv)
		select {
		case cmd := <-ses.Commands():
			if done := r.handleCommand(ctx, cmd); done {
				return
			}
			if ses.State() == storage.StateManual {
				if done := r.runManualPhase(ctx); done {
					return
				}
			}
		case <-ctx.Done():
			ses.Close(storage.StateClientDisconnected, "client_disconnected")
			return
		case <-ses.Done():
			return
		case <-ticker.C:
		}
	}
}

// runManualPhase runs while the session is under administrator control.
func (r *streamRunner) runManualPhase(ctx context.Context) bool {
	ses := r.ses
	for {
		select {
		case <-ctx.Done():
			ses.Close(storage.StateClientDisconnected, "client_disconnected")
			return true
		case <-ses.Done():
			return true
		case cmd := <-ses.Commands():
			if done := r.handleCommand(ctx, cmd); done {
				return true
			}
			if ses.State() != storage.StateManual {
				return false
			}
		case text := <-ses.ManualInput():
			r.deliverManual(ctx, text)
		}
	}
}

func (r *streamRunner) deliverManual(ctx context.Context, text string) {
	ses := r.ses
	rt := config.C().Runtime()
	cps := rt.ManualCharsPerSecond
	vlog.BizEntry(ses.RequestID, "人工消息下发", "向客户端推送人工接管消息", "text="+truncate(text, 100))

	if cps <= 0 {
		// single-shot full delivery
		_ = r.sw.EmitText(ctx, text)
	} else {
		// simulated typing at characters_per_second
		for _, ch := range text {
			if ses.State() != storage.StateManual {
				break
			}
			_ = r.sw.EmitText(ctx, string(ch))
			time.Sleep(time.Duration(float64(time.Second) / float64(cps)))
		}
	}
	ses.AddEcho()
	r.srv.logOutput(ses, text, tokenizer.Count(text))
	r.srv.deps.Bus.Publish(events.Message{
		Type: events.ManualMessage, Session: ses.ID,
		Data: map[string]any{"content": truncate(text, 500)},
	})
	if ses.Streaming {
		// keep the stream open for the next message
		_ = r.sw.WriteComment("manual message delivered")
	}
}

// handleCommand applies an admin control command. Returns true when the session
// must terminate.
func (r *streamRunner) handleCommand(ctx context.Context, cmd sessions.Command) bool {
	ses := r.ses
	vlog.BizEntry(ses.RequestID, "执行管理员指令", "控制指令="+cmd.Type, "")
	switch cmd.Type {
	case "pause":
		ses.Pause()
	case "resume":
		ses.Resume()
	case "takeover":
		ses.TakeOver()
		r.srv.deps.Bus.Publish(events.Message{
			Type: events.ManualTakeover, Session: ses.ID,
			Data: map[string]any{"state": "MANUAL"},
		})
	case "return":
		ses.ReturnToEcho()
	case "finish":
		if ses.Streaming {
			_ = r.finish(ctx, "stop")
		}
		ses.Close(storage.StateEnded, "finish_normally")
		return true
	case "drop":
		_ = r.sw.Disconnect()
		ses.Close(storage.StateEnded, "drop_connection")
		return true
	case "inject":
		if cmd.Fault != nil {
			vlog.BizEntry(ses.RequestID, "流中故障注入", fmt.Sprintf("HTTP %d, 模式: %s", cmd.Fault.HTTPStatus, cmd.Fault.Mode), cmd.Fault.Message)
			_ = r.sw.Fail(ctx, cmd.Fault)
			ses.Close(storage.StateEnded, "fault_injected")
			return true
		}
	case "rate":
		ses.SetRate(cmd.Rate, cmd.Mode, config.C().Runtime().SessionBurstSeconds, cmd.ChunkSize)
	case "interval":
		ses.SetInterval(cmd.IntervalMS)
	case "mode":
		ses.SetEchoMode(cmd.Mode)
	}
	return false
}

func echoInterval(ses *sessions.Session) time.Duration {
	return time.Duration(ses.IntervalMS()) * time.Millisecond
}

var _ = ochat.NowUnix
var _ = ored.NowUnix
var _ = json.Marshal
