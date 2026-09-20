package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/epicai/epicai/backend/internal/adapters/openai"
	ochat "github.com/epicai/epicai/backend/internal/adapters/openai_chat"
	"github.com/epicai/epicai/backend/internal/canonical"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/engine/echo"
	"github.com/epicai/epicai/backend/internal/engine/stream"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/epicai/epicai/backend/internal/tokenizer"
	"github.com/epicai/epicai/backend/internal/vlog"
)

// extractToolNames pulls the declared function-tool names out of a parsed
// OpenAI chat request. Anthropic-style tool entries (no "function" wrapper) are
// also tolerated.
func extractToolNames(raw []any) []string {
	var names []string
	for _, t := range raw {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if fn, ok := m["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				names = append(names, name)
			}
			continue
		}
		if name, ok := m["name"].(string); ok && name != "" {
			names = append(names, name)
		}
	}
	return names
}

// pickMaxTokens returns max_completion_tokens when set, otherwise max_tokens.
func pickMaxTokens(maxTokens, maxCompletionTokens *int) int {
	if maxCompletionTokens != nil && *maxCompletionTokens > 0 {
		return *maxCompletionTokens
	}
	if maxTokens != nil && *maxTokens > 0 {
		return *maxTokens
	}
	return 0
}

// HandleChatCompletions serves POST /v1/chat/completions.
func (s *Server) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	traceID := vlog.TraceID(r.Context())
	if openai.ApplyCORS(w, r) {
		vlog.MiddlewareCORS(traceID, r.Header.Get("Origin"), string(config.C().Runtime().CORSMode), true)
		return
	}
	if r.Method != http.MethodPost {
		vlog.RequestBlocked(traceID, "方法校验", "不支持的方法: "+r.Method, 405, "请求未进入业务逻辑", "请使用POST方法发起请求")
		jsonErr(w, 405, "method_not_allowed", "invalid_request_error", "Only POST is supported")
		return
	}
	fp, ok, key := s.resolveKey(w, r)
	if !ok {
		return
	}
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}

	req, conv, err := ochat.Parse(body)
	if err != nil {
		vlog.RequestBlocked(traceID, "请求JSON解析", err.Error(), 400, "请求体JSON格式错误，未进入业务逻辑", "请检查JSON格式")
		jsonErr(w, 400, "invalid_request_body", "invalid_request_error", "Malformed JSON request body")
		return
	}

	modelName := req.Model
	if modelName == "" {
		modelName = config.C().Static().DefaultModel
	}
	model, ok := s.resolveModel(w, modelName)
	if !ok {
		return
	}
	if key != nil && len(key.Models) > 0 && !contains(key.Models, modelName) {
		vlog.RequestBlocked(traceID, "API-Key权限", "Key无权调用模型: "+modelName, 403, "请求未进入业务逻辑", "请使用有权限的API Key")
		jsonErr(w, 403, "model_not_permitted", "invalid_request_error", "This API key is not permitted to use this model")
		return
	}

	ip := s.clientIP(r)
	keyMax := 0
	if key != nil {
		keyMax = key.MaxSessions
	}
	if !s.admit(w, r, ip, fp, keyMax, nil) {
		return
	}

	vlog.BizEntry(traceID, "chat_completions", "处理对话补全请求", fmt.Sprintf("model=%s stream=%v parts=%d", modelName, req.Stream, len(conv.LastUserParts())))

	// Immediate error behavior short-circuits before any session is created.
	if model.Behavior == storage.BehaviorImmediateError {
		s.deps.Manager.Unregister("") // no-op to keep counters balanced
		vlog.BizEntry(traceID, "模型即时错误", fmt.Sprintf("模型配置为ImmediateError: HTTP %d", orStatus(model.ErrorStatus)), model.ErrorMessage)
		openai.WriteSpec(w, &sessions.FaultSpec{
			HTTPStatus: orStatus(model.ErrorStatus), Code: model.ErrorCode,
			Type: model.ErrorType, Message: model.ErrorMessage, Mode: "http_error",
		})
		return
	}

	sid := s.deps.Manager.NewID()
	rid := traceID
	kill := func() { /* wired after the writer exists */ }

	// Persist any base64 images into the asset store
	s.persistMultimodalAssets(conv, sid, traceID)

	ses := s.deps.Manager.Create(sessions.Options{
		ID: sid, RequestID: rid, Protocol: string(canonical.ProtocolChatCompletions),
		Model: model, Streaming: req.Stream, ClientIP: ip,
		UserAgent: r.UserAgent(), KeyFP: fp, Conv: conv,
		RateLimit: orRate(model.TokenRate), EchoContentMode: model.EchoContentMode,
	})
	ses.SetRequestMeta(extractToolNames(req.Tools), pickMaxTokens(req.MaxTokens, req.MaxCompletionTokens))

	ses.SetRawRequest(&storage.RawRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Headers: sanitizeHeaders(r.Header), Body: string(body),
	}, int64(len(body)))
	vlog.DBAudit(traceID, "创建会话记录", "session_id="+sid+" model="+model.ModelID, "成功", 1)

	s.logInput(ses, conv.Text(), int(tokenizer.Count(conv.Text())))

	w.Header().Set("X-Request-Id", rid)
	w.Header().Set("X-Epic-Session-Id", sid)

	id := openai.ChatID()
	created := time.Now().Unix()

	if !req.Stream {
		s.handleChatNonStream(w, r, ses, model, conv, id, created, traceID)
		return
	}
	s.handleChatStream(w, r, ses, model, conv, id, created, kill, traceID)
}

func (s *Server) handleChatNonStream(w http.ResponseWriter, r *http.Request, ses *sessions.Session,
	model *storage.Model, conv *canonical.Conversation, id string, created int64, traceID string) {
	defer func() {
		ses.Close(storage.StateEnded, "non_stream_complete")
		s.deps.Manager.Unregister(ses.ID)
	}()

	// Chaos: hold the connection open forever without responding.
	if config.C().Runtime().HoldConnectionForever {
		ses.SetState(storage.StateEchoing)
		<-r.Context().Done()
		ses.Close(storage.StateClientDisconnected, "client_disconnected")
		return
	}

	// Agent Echo behavior in non-streaming mode. Only active when the client
	// actually declared tools — a request without tools (e.g. opencode's context
	// summarizer) must never receive tool calls.
	availableTools := ses.RequestTools()
	if model.EnableAgent && len(availableTools) > 0 {
		subCount := model.SubagentCount
		if subCount <= 0 {
			subCount = 1
		}
		blockTokens := 0
		if model.Behavior == storage.BehaviorInfiniteEchoMax {
			blockTokens = model.MaxTokenChunk
			if blockTokens <= 0 {
				blockTokens = 2048
			}
			if mt := ses.RequestMaxTokens(); mt > 0 {
				if budget := mt / (1 + subCount); budget >= 1 && blockTokens > budget {
					blockTokens = budget
				}
			}
		}

		var textContent string
		if model.Behavior == storage.BehaviorInfiniteEchoMax {
			textContent = echo.BuildInfiniteMaxText(conv.LastUserText(), blockTokens)
		} else {
			textContent = conv.LastUserText()
		}
		spec := echo.BuildAgentEchoCall(availableTools, textContent)

		var tcs []any
		for i := 0; i < subCount; i++ {
			callID := fmt.Sprintf("call_epic_%s_%d", id[len(id)-8:], i)
			tcs = append(tcs, map[string]any{
				"id":   callID,
				"type": "function",
				"function": map[string]any{
					"name":      spec.ToolName,
					"arguments": spec.Arguments,
				},
			})
		}

		outTokens := tokenizer.Count(textContent)
		ses.AddOutputTokens(outTokens)
		ses.AddEcho()
		s.logOutput(ses, textContent, outTokens)

		reason := "tool_calls"
		usage := &ochat.Usage{
			PromptTokens:     int(ses.SnapshotTokens().Input),
			CompletionTokens: int(outTokens) + 20*subCount,
			TotalTokens:      int(ses.SnapshotTokens().Input) + int(outTokens) + 20*subCount,
		}
		resp := ochat.FullResponse{
			ID: id, Object: "chat.completion", Created: created, Model: model.ModelID,
			Choices: []ochat.Choice{{
				Index: 0,
				Message: &ochat.RespMsg{
					Role:      "assistant",
					Content:   &textContent,
					ToolCalls: tcs,
				},
				FinishReason: &reason,
			}},
			Usage: usage,
		}
		vlog.BizEntry(traceID, "chat_non_stream_agent_echo", "下发Agent工具调用与吐字", fmt.Sprintf("%s x %d, text_len: %d", spec.ToolName, subCount, len(textContent)))
		writeJSON(w, 200, resp)
		return
	}

	// Infinite Echo MAX behavior in non-streaming mode
	if model.Behavior == storage.BehaviorInfiniteEchoMax {
		maxTok := model.MaxTokenChunk
		if maxTok <= 0 {
			maxTok = 2048
		}
		text := echo.BuildInfiniteMaxText(conv.LastUserText(), maxTok)
		tokens := tokenizer.Count(text)
		ses.AddOutputTokens(tokens)
		ses.AddEcho()
		s.logOutput(ses, text, tokens)

		usage := &ochat.Usage{
			PromptTokens:     int(ses.SnapshotTokens().Input),
			CompletionTokens: int(tokens),
			TotalTokens:      int(ses.SnapshotTokens().Input) + int(tokens),
		}
		resp := ochat.NewFullResponse(id, model.ModelID, created, text, conv.LastUserParts(), usage)
		vlog.ResponseSent(traceID, "chat_non_stream_echo_max", 200, "tokens="+fmt.Sprintf("%d", tokens), "", "成功", "非流式无限回显MAX完成")
		writeJSON(w, 200, resp)
		return
	}

	text := conv.LastUserText()
	tokens := tokenizer.Count(text)
	ses.AddOutputTokens(tokens)
	ses.AddEcho()
	s.logOutput(ses, text, tokens)

	usage := &ochat.Usage{
		PromptTokens:     int(ses.SnapshotTokens().Input),
		CompletionTokens: int(tokens),
		TotalTokens:      int(ses.SnapshotTokens().Input) + int(tokens),
	}
	resp := ochat.NewFullResponse(id, model.ModelID, created, text, conv.LastUserParts(), usage)
	vlog.ResponseSent(traceID, "chat_non_stream", 200, "tokens="+fmt.Sprintf("%d", tokens), "", "成功", "非流式对话补全成功完成")
	writeJSON(w, 200, resp)
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request, ses *sessions.Session,
	model *storage.Model, conv *canonical.Conversation, id string, created int64, kill func(), traceID string) {

	sw, err := stream.NewWriter(w, ses)
	if err != nil {
		vlog.ErrorOccurred(traceID, "建立SSE流", "streaming_unsupported", err.Error(), 500, "无法向客户端推送流式事件", true)
		jsonErr(w, 500, "streaming_unsupported", "server_error", "Streaming is not supported by this connection")
		return
	}
	sw.SetDoneFrame("data: [DONE]\n\n")
	s.streams.Store(ses.ID, sw)

	var first = &atomicBool{}
	sw.Configure(func(delta string) error {
		c := ochat.NewChunk(id, model.ModelID, created, delta, "")
		if first != nil && !first.Get() {
			c.Choices[0].Delta.Role = "assistant"
			first.Set(true)
		}
		b, _ := json.Marshal(c)
		return sw.WriteFrame(string(b))
	}, func(reason string) error {
		c := ochat.FinalChunk(id, model.ModelID, created, reason)
		b, _ := json.Marshal(c)
		return sw.WriteFrame(string(b))
	})
	sw.SetToolCallWriter(func(tcIndex int, tcID, fnName, fnArgs string) error {
		role := ""
		if first != nil && !first.Get() {
			role = "assistant"
			first.Set(true)
		}
		c := ochat.NewToolCallChunk(id, model.ModelID, created, tcIndex, tcID, fnName, fnArgs, role)
		b, _ := json.Marshal(c)
		return sw.WriteFrame(string(b))
	})

	sw.Start()
	vlog.BizEntry(traceID, "chat_stream_start", "已建立SSE流式连接并开始回显", "session_id="+ses.ID)

	// Keep-alive comments preserve idle connections through proxies.
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if sw.IsClosed() {
					return
				}
				_ = sw.WriteComment("keep-alive")
			case <-ses.Done():
				return
			case <-r.Context().Done():
				return
			}
		}
	}()

	runner := s.newRunner(ses, sw, conv,
		func(ctx context.Context, delta string) error { return sw.WriteText(delta) },
		func(ctx context.Context, reason string) error { return sw.Finish(ctx, reason) })
	runner.Run(r.Context(), model)
}

type atomicBool struct{ v int32 }

func (b *atomicBool) Get() bool { return atomic.LoadInt32(&b.v) == 1 }
func (b *atomicBool) Set(v bool) {
	var n int32
	if v {
		n = 1
	}
	atomic.StoreInt32(&b.v, n)
}

func orStatus(n int) int {
	if n <= 0 {
		return 500
	}
	return n
}

func orRate(n int64) int64 { return n }

func contains(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) || v == "*" {
			return true
		}
	}
	return false
}

func jsonErr(w http.ResponseWriter, status int, code, typ, msg string) {
	openai.WriteError(w, status, code, typ, msg)
}
