package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/epicai/epicai/backend/internal/adapters/openai"
	ored "github.com/epicai/epicai/backend/internal/adapters/openai_responses"
	"github.com/epicai/epicai/backend/internal/canonical"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/engine/stream"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/epicai/epicai/backend/internal/tokenizer"
	"github.com/epicai/epicai/backend/internal/vlog"
)

// HandleResponses serves POST /v1/responses.
func (s *Server) HandleResponses(w http.ResponseWriter, r *http.Request) {
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

	req, conv, err := ored.Parse(body)
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

	vlog.BizEntry(traceID, "responses", "处理Responses请求", fmt.Sprintf("model=%s stream=%v parts=%d", modelName, req.Stream, len(conv.LastUserParts())))

	if model.Behavior == storage.BehaviorImmediateError {
		s.deps.Manager.Unregister("")
		vlog.BizEntry(traceID, "模型即时错误", fmt.Sprintf("模型配置为ImmediateError: HTTP %d", orStatus(model.ErrorStatus)), model.ErrorMessage)
		openai.WriteSpec(w, &sessions.FaultSpec{
			HTTPStatus: orStatus(model.ErrorStatus), Code: model.ErrorCode,
			Type: model.ErrorType, Message: model.ErrorMessage, Mode: "http_error",
		})
		return
	}

	sid := s.deps.Manager.NewID()
	rid := traceID

	// Persist any base64 images into the asset store
	s.persistMultimodalAssets(conv, sid, traceID)

	ses := s.deps.Manager.Create(sessions.Options{
		ID: sid, RequestID: rid, Protocol: string(canonical.ProtocolResponses),
		Model: model, Streaming: req.Stream, ClientIP: ip,
		UserAgent: r.UserAgent(), KeyFP: fp, Conv: conv,
		RateLimit: model.TokenRate, EchoContentMode: model.EchoContentMode,
	})
	ses.SetRequestMeta(extractToolNames(req.Tools), pickMaxTokens(req.MaxOutputTokens, nil))
	ses.SetRawRequest(&storage.RawRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Headers: sanitizeHeaders(r.Header), Body: string(body),
	}, int64(len(body)))
	vlog.DBAudit(traceID, "创建会话记录", "session_id="+sid+" model="+model.ModelID, "成功", 1)

	s.logInput(ses, conv.Text(), int(tokenizer.Count(conv.Text())))
	w.Header().Set("X-Request-Id", rid)
	w.Header().Set("X-Epic-Session-Id", sid)

	id := openai.RespID()
	created := time.Now().Unix()

	if !req.Stream {
		s.handleResponsesNonStream(w, r, ses, model, conv, id, created, traceID)
		return
	}
	s.handleResponsesStream(w, r, ses, model, conv, id, created, traceID)
}

func (s *Server) handleResponsesNonStream(w http.ResponseWriter, r *http.Request, ses *sessions.Session,
	model *storage.Model, conv *canonical.Conversation, id string, created int64, traceID string) {
	defer func() {
		ses.Close(storage.StateEnded, "non_stream_complete")
		s.deps.Manager.Unregister(ses.ID)
	}()

	if config.C().Runtime().HoldConnectionForever {
		ses.SetState(storage.StateEchoing)
		<-r.Context().Done()
		ses.Close(storage.StateClientDisconnected, "client_disconnected")
		return
	}

	text := conv.LastUserText()
	tokens := tokenizer.Count(text)
	ses.AddOutputTokens(tokens)
	ses.AddEcho()
	s.logOutput(ses, text, tokens)

	inTok := int(tokenizer.Count(conv.Text()))
	resp := ored.NewResponse(id, model.ModelID, created, text)
	resp.Usage = &ored.RespUsage{InputTokens: inTok, OutputTokens: tokens, TotalTokens: inTok + tokens}
	vlog.ResponseSent(traceID, "responses_non_stream", 200, "tokens="+fmt.Sprintf("%d", tokens), "", "成功", "非流式Responses补全成功完成")
	writeJSON(w, 200, resp)
}

func (s *Server) handleResponsesStream(w http.ResponseWriter, r *http.Request, ses *sessions.Session,
	model *storage.Model, conv *canonical.Conversation, id string, created int64, traceID string) {

	sw, err := stream.NewWriter(w, ses)
	if err != nil {
		vlog.ErrorOccurred(traceID, "建立SSE流", "streaming_unsupported", err.Error(), 500, "无法向客户端推送流式事件", true)
		jsonErr(w, 500, "streaming_unsupported", "server_error", "Streaming is not supported by this connection")
		return
	}
	// Responses API has no [DONE]; completion is expressed with an event.
	sw.SetDoneFrame("")
	s.streams.Store(ses.ID, sw)

	itemID := openai.MsgID()
	var started bool
	sw.Configure(func(delta string) error {
		if !started {
			started = true
			emitAll(sw, ored.Created(id, model.ModelID, created),
				ored.InProgress(id, model.ModelID, created),
				ored.OutputItemAdded(id, itemID, 0),
				ored.ContentPartAdded(id, itemID, 0, 0))
		}
		b, _ := json.Marshal(ored.TextDelta(id, itemID, delta, 0, 0))
		return sw.WriteRaw("event: response.output_text.delta\ndata: " + string(b) + "\n\n")
	}, func(reason string) error {
		text := conv.LastUserText()
		inTok := int(tokenizer.Count(conv.Text()))
		outTok := int(ses.SnapshotTokens().Output)
		emitAll(sw,
			ored.TextDone(id, itemID, text, 0, 0),
			ored.ContentPartDone(id, itemID, text, 0, 0),
			ored.OutputItemDone(id, itemID, text, 0),
			ored.Completed(id, model.ModelID, created, text, inTok, outTok))
		return nil
	})

	sw.Start()
	vlog.BizEntry(traceID, "responses_stream_start", "已建立Responses SSE流式连接并开始回显", "session_id="+ses.ID)

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

func emitAll(sw *stream.Writer, events ...map[string]any) {
	for _, e := range events {
		b, _ := json.Marshal(e)
		typ, _ := e["type"].(string)
		if typ != "" {
			_ = sw.WriteRaw("event: " + typ + "\ndata: " + string(b) + "\n\n")
		} else {
			_ = sw.WriteFrame(string(b))
		}
	}
}
