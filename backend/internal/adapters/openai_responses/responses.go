// Package openai_responses parses OpenAI Responses API requests into the
// canonical model and serializes canonical output back to Responses events.
package openai_responses

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/epicai/epicai/backend/internal/canonical"
)

// Request mirrors the OpenAI Responses API request body.
type Request struct {
	Model              string         `json:"model"`
	Input              any            `json:"input"`
	Stream             bool           `json:"stream"`
	Instructions       any            `json:"instructions"`
	MaxOutputTokens    *int           `json:"max_output_tokens"`
	Temperature        *float64       `json:"temperature"`
	TopP               *float64       `json:"top_p"`
	User               string         `json:"user"`
	PreviousResponseID string         `json:"previous_response_id"`
	Store              *bool          `json:"store"`
	Tools              []any          `json:"tools"`
	ToolChoice         any            `json:"tool_choice"`
	ParallelToolCalls  *bool          `json:"parallel_tool_calls"`
	Metadata           map[string]any `json:"metadata"`
	Text               map[string]any `json:"text"`
	Truncation         string         `json:"truncation"`
}

type InputItem struct {
	ID      string         `json:"id"`
	Role    string         `json:"role"`
	Type    string         `json:"type"`
	Status  string         `json:"status"`
	Content any            `json:"content"`
	Extra   map[string]any `json:"-"`
}

type InputPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Filename string `json:"filename,omitempty"`
	Detail   string `json:"detail,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// Parse converts a Responses body into the canonical conversation.
func Parse(body []byte) (*Request, *canonical.Conversation, error) {
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, err
	}
	conv := &canonical.Conversation{}
	switch in := req.Input.(type) {
	case string:
		conv.Messages = append(conv.Messages, canonical.Message{
			Role:  canonical.RoleUser,
			Parts: []canonical.ContentPart{{Type: canonical.PartText, Text: in}},
		})
	case []any:
		for _, raw := range in {
			b, _ := json.Marshal(raw)
			var item InputItem
			if err := json.Unmarshal(b, &item); err != nil {
				continue
			}
			var rawMap map[string]any
			_ = json.Unmarshal(b, &rawMap)
			msg := canonical.Message{Role: canonical.Role(item.Role), Raw: rawMap}
			if msg.Role == "" {
				msg.Role = canonical.RoleUser
			}
			switch c := item.Content.(type) {
			case string:
				msg.Parts = append(msg.Parts, canonical.ContentPart{Type: canonical.PartText, Text: c})
			case []any:
				for _, craw := range c {
					cb, _ := json.Marshal(craw)
					var p InputPart
					if err := json.Unmarshal(cb, &p); err != nil {
						continue
					}
					var pmap map[string]any
					_ = json.Unmarshal(cb, &pmap)
					msg.Parts = append(msg.Parts, convertPart(p, pmap))
				}
			case map[string]any:
				cb, _ := json.Marshal(c)
				var p InputPart
				if err := json.Unmarshal(cb, &p); err == nil {
					var pmap map[string]any
					_ = json.Unmarshal(cb, &pmap)
					msg.Parts = append(msg.Parts, convertPart(p, pmap))
				}
			}
			conv.Messages = append(conv.Messages, msg)
		}
	case map[string]any:
		b, _ := json.Marshal(in)
		var item InputItem
		if err := json.Unmarshal(b, &item); err == nil {
			msg := canonical.Message{Role: canonical.Role(item.Role)}
			if msg.Role == "" {
				msg.Role = canonical.RoleUser
			}
			if s, ok := item.Content.(string); ok {
				msg.Parts = append(msg.Parts, canonical.ContentPart{Type: canonical.PartText, Text: s})
			}
			conv.Messages = append(conv.Messages, msg)
		}
	case nil:
		// no input
	}
	if len(conv.Messages) == 0 {
		conv.Messages = append(conv.Messages, canonical.Message{
			Role:  canonical.RoleUser,
			Parts: []canonical.ContentPart{{Type: canonical.PartText, Text: ""}},
		})
	}
	return &req, conv, nil
}

func convertPart(p InputPart, raw map[string]any) canonical.ContentPart {
	switch p.Type {
	case "input_text", "text", "output_text", "":
		return canonical.ContentPart{Type: canonical.PartText, Text: p.Text, Raw: raw}
	case "input_image", "image_url", "image":
		part := canonical.ContentPart{Type: canonical.PartImage, ImageURL: p.ImageURL, Raw: raw}
		if p.MimeType != "" {
			part.MimeType = p.MimeType
		}
		if strings.HasPrefix(p.ImageURL, "data:") {
			if idx := strings.Index(p.ImageURL, ","); idx > 0 {
				meta := p.ImageURL[:idx]
				part.ImageBase64 = p.ImageURL[idx+1:]
				if i := strings.Index(meta, ";"); i > 5 {
					part.MimeType = meta[5:i]
				}
			}
		} else if part.MimeType == "" {
			part.MimeType = mimeFromURL(p.ImageURL)
		}
		return part
	case "input_file", "file":
		return canonical.ContentPart{Type: canonical.PartFile, FileID: p.FileID,
			FileData: p.FileData, FileName: p.Filename, Raw: raw}
	default:
		return canonical.ContentPart{Type: canonical.PartUnknown, Raw: raw,
			Meta: map[string]any{"declared_type": p.Type}}
	}
}

func mimeFromURL(u string) string {
	lower := strings.ToLower(u)
	switch {
	case strings.Contains(lower, ".png"):
		return "image/png"
	case strings.Contains(lower, ".jpg"), strings.Contains(lower, ".jpeg"):
		return "image/jpeg"
	case strings.Contains(lower, ".gif"):
		return "image/gif"
	case strings.Contains(lower, ".webp"):
		return "image/webp"
	case strings.Contains(lower, ".pdf"):
		return "application/pdf"
	}
	return ""
}

// ---------------- Response serialization ----------------

type ResponseObject struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	CreatedAt         int64          `json:"created_at"`
	Status            string         `json:"status,omitempty"`
	Model             string         `json:"model"`
	Output            []OutputItem   `json:"output"`
	OutputText        string         `json:"output_text,omitempty"`
	Usage             *RespUsage     `json:"usage,omitempty"`
	Error             map[string]any `json:"error,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	IncompleteDetails any            `json:"incomplete_details,omitempty"`
}

type OutputItem struct {
	ID      string       `json:"id"`
	Type    string       `json:"type"`
	Status  string       `json:"status"`
	Role    string       `json:"role,omitempty"`
	Content []OutputPart `json:"content,omitempty"`
	Text    string       `json:"text,omitempty"`
}

type OutputPart struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations,omitempty"`
}

type RespUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func NewResponse(id, model string, created int64, text string) ResponseObject {
	return ResponseObject{
		ID: id, Object: "response", CreatedAt: created, Status: "completed", Model: model,
		Output: []OutputItem{{
			ID: "msg_epic_" + id[len("resp_epic_"):], Type: "message", Status: "completed", Role: "assistant",
			Content: []OutputPart{{Type: "output_text", Text: text, Annotations: []any{}}},
		}},
		OutputText: text,
		Usage:      &RespUsage{},
	}
}

// Events emitted on the Responses SSE stream.
type Event struct {
	Type string `json:"type"`
	Data map[string]any
}

func Created(id, model string, created int64) map[string]any {
	return map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": id, "object": "response", "created_at": created, "model": model,
			"status": "in_progress", "output": []any{},
		},
	}
}

func InProgress(id, model string, created int64) map[string]any {
	return map[string]any{
		"type": "response.in_progress",
		"response": map[string]any{
			"id": id, "object": "response", "created_at": created, "model": model,
			"status": "in_progress", "output": []any{},
		},
	}
}

func OutputItemAdded(id, itemID string, outputIndex int) map[string]any {
	return map[string]any{
		"type":         "response.output_item.added",
		"output_index": outputIndex,
		"item": map[string]any{
			"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{},
		},
	}
}

func ContentPartAdded(id, itemID string, outputIndex, contentIndex int) map[string]any {
	return map[string]any{
		"type":    "response.content_part.added",
		"item_id": itemID, "output_index": outputIndex, "content_index": contentIndex,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	}
}

func TextDelta(id, itemID, delta string, outputIndex, contentIndex int) map[string]any {
	return map[string]any{
		"type":    "response.output_text.delta",
		"item_id": itemID, "output_index": outputIndex, "content_index": contentIndex,
		"delta": delta,
	}
}

func TextDone(id, itemID, text string, outputIndex, contentIndex int) map[string]any {
	return map[string]any{
		"type":    "response.output_text.done",
		"item_id": itemID, "output_index": outputIndex, "content_index": contentIndex,
		"text": text,
	}
}

func ContentPartDone(id, itemID, text string, outputIndex, contentIndex int) map[string]any {
	return map[string]any{
		"type":    "response.content_part.done",
		"item_id": itemID, "output_index": outputIndex, "content_index": contentIndex,
		"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
	}
}

func OutputItemDone(id, itemID, text string, outputIndex int) map[string]any {
	return map[string]any{
		"type":         "response.output_item.done",
		"output_index": outputIndex,
		"item": map[string]any{
			"id": itemID, "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		},
	}
}

func Completed(id, model string, created int64, text string, inTok, outTok int) map[string]any {
	return map[string]any{
		"type": "response.completed",
		"response": ResponseObject{
			ID: id, Object: "response", CreatedAt: created, Status: "completed", Model: model,
			Output: []OutputItem{{
				ID:   "msg_epic_" + strings.TrimPrefix(id, "resp_epic_"),
				Type: "message", Status: "completed", Role: "assistant",
				Content: []OutputPart{{Type: "output_text", Text: text, Annotations: []any{}}},
			}},
			OutputText: text,
			Usage:      &RespUsage{InputTokens: inTok, OutputTokens: outTok, TotalTokens: inTok + outTok},
		},
	}
}

func Failed(id, model string, created int64, code, msg string) map[string]any {
	return map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id": id, "object": "response", "created_at": created, "model": model,
			"status": "failed",
			"error":  map[string]any{"code": code, "message": msg},
		},
	}
}

func NowUnix() int64 { return time.Now().Unix() }
