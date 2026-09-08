// Package openai_chat parses OpenAI Chat Completions requests into the
// canonical model and serializes canonical output back to Chat Completions.
package openai_chat

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/epicai/epicai/backend/internal/canonical"
)

// Request mirrors the OpenAI Chat Completions request body. Unknown fields are
// accepted and ignored on purpose.
type Request struct {
	Model               string         `json:"model"`
	Messages            []Message      `json:"messages"`
	Stream              bool           `json:"stream"`
	Temperature         *float64       `json:"temperature"`
	TopP                *float64       `json:"top_p"`
	MaxTokens           *int           `json:"max_tokens"`
	MaxCompletionTokens *int           `json:"max_completion_tokens"`
	Stop                any            `json:"stop"`
	N                   *int           `json:"n"`
	User                string         `json:"user"`
	StreamOptions       map[string]any `json:"stream_options"`
	// accepted-and-ignored extras
	PresencePenalty  *float64       `json:"presence_penalty"`
	FrequencyPenalty *float64       `json:"frequency_penalty"`
	LogitBias        map[string]any `json:"logit_bias"`
	Seed             *int           `json:"seed"`
	Tools            []any          `json:"tools"`
	ToolChoice       any            `json:"tool_choice"`
	ResponseFormat   map[string]any `json:"response_format"`
	Extra            map[string]any `json:"-"`
}

type Message struct {
	Role         string         `json:"role"`
	Content      any            `json:"content"`
	Name         string         `json:"name,omitempty"`
	ToolCalls    []any          `json:"tool_calls,omitempty"`
	Refusal      string         `json:"refusal,omitempty"`
	FunctionCall map[string]any `json:"function_call,omitempty"`
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL    string `json:"url"`
		Detail string `json:"detail,omitempty"`
	} `json:"image_url,omitempty"`
	File *struct {
		FileID   string `json:"file_id"`
		FileData string `json:"file_data,omitempty"`
		Filename string `json:"filename,omitempty"`
	} `json:"file,omitempty"`
	InputAudio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio,omitempty"`
}

// Parse converts a Chat Completions body into the canonical conversation.
func Parse(body []byte) (*Request, *canonical.Conversation, error) {
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, nil, err
	}
	conv := &canonical.Conversation{}
	for _, m := range req.Messages {
		cm := canonical.Message{Role: canonical.Role(m.Role), Name: m.Name}
		switch c := m.Content.(type) {
		case string:
			cm.Parts = append(cm.Parts, canonical.ContentPart{Type: canonical.PartText, Text: c})
		case []any:
			for _, raw := range c {
				b, _ := json.Marshal(raw)
				var p ContentPart
				if err := json.Unmarshal(b, &p); err != nil {
					continue
				}
				cm.Parts = append(cm.Parts, convertPart(p, b))
			}
		case map[string]any:
			b, _ := json.Marshal(c)
			var p ContentPart
			if err := json.Unmarshal(b, &p); err == nil {
				cm.Parts = append(cm.Parts, convertPart(p, b))
			}
		case nil:
			// empty content is legal
		}
		conv.Messages = append(conv.Messages, cm)
	}
	if len(conv.Messages) == 0 {
		conv.Messages = append(conv.Messages, canonical.Message{
			Role:  canonical.RoleUser,
			Parts: []canonical.ContentPart{{Type: canonical.PartText, Text: ""}},
		})
	}
	return &req, conv, nil
}

func convertPart(p ContentPart, raw []byte) canonical.ContentPart {
	var rawMap map[string]any
	_ = json.Unmarshal(raw, &rawMap)
	switch p.Type {
	case "text", "input_text", "":
		return canonical.ContentPart{Type: canonical.PartText, Text: p.Text, Raw: rawMap}
	case "image_url", "input_image", "image":
		url := ""
		if p.ImageURL != nil {
			url = p.ImageURL.URL
		}
		if url == "" {
			if u, ok := rawMap["image_url"].(string); ok {
				url = u
			} else if m, ok := rawMap["image_url"].(map[string]any); ok {
				if u2, ok := m["url"].(string); ok {
					url = u2
				}
			}
		}
		part := canonical.ContentPart{Type: canonical.PartImage, ImageURL: url, Raw: rawMap}
		if strings.HasPrefix(url, "data:") {
			if idx := strings.Index(url, ","); idx > 0 {
				meta := url[:idx]
				part.ImageBase64 = url[idx+1:]
				if strings.Contains(meta, "image/png") {
					part.MimeType = "image/png"
				} else if strings.Contains(meta, "image/jpeg") {
					part.MimeType = "image/jpeg"
				} else if strings.Contains(meta, "image/gif") {
					part.MimeType = "image/gif"
				} else if strings.Contains(meta, "image/webp") {
					part.MimeType = "image/webp"
				}
				if i := strings.Index(meta, ";"); i > 5 {
					part.MimeType = meta[5:i]
				}
			}
		} else {
			part.MimeType = mimeFromURL(url)
		}
		return part
	case "file", "input_file", "document":
		part := canonical.ContentPart{Type: canonical.PartFile, Raw: rawMap}
		if p.File != nil {
			part.FileID = p.File.FileID
			part.FileData = p.File.FileData
			part.FileName = p.File.Filename
		}
		if part.FileID == "" {
			if fid, ok := rawMap["file_id"].(string); ok {
				part.FileID = fid
			}
		}
		if part.FileName == "" {
			if fn, ok := rawMap["filename"].(string); ok {
				part.FileName = fn
			}
		}
		return part
	case "input_audio", "audio":
		part := canonical.ContentPart{Type: canonical.PartAudio, Raw: rawMap}
		if p.InputAudio != nil {
			part.FileData = p.InputAudio.Data
			part.MimeType = "audio/" + p.InputAudio.Format
		}
		return part
	default:
		return canonical.ContentPart{Type: canonical.PartUnknown, Raw: rawMap,
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
	case strings.Contains(lower, ".txt"):
		return "text/plain"
	}
	return ""
}

// ---------------- Response serialization ----------------

type Chunk struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
}

type Choice struct {
	Index        int      `json:"index"`
	Delta        *Delta   `json:"delta,omitempty"`
	Message      *RespMsg `json:"message,omitempty"`
	FinishReason *string  `json:"finish_reason"`
	LogProbs     any      `json:"logprobs,omitempty"`
}

type Delta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type RespMsg struct {
	Role      string `json:"role"`
	Content   any    `json:"content"`
	Refusal   any    `json:"refusal,omitempty"`
	ToolCalls []any  `json:"tool_calls,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func NewChunk(id, model string, created int64, content string, role string) Chunk {
	c := Chunk{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []Choice{{Index: 0, Delta: &Delta{Content: content}}},
	}
	if role != "" {
		c.Choices[0].Delta.Role = role
	}
	return c
}

func FinalChunk(id, model string, created int64, reason string) Chunk {
	return Chunk{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []Choice{{Index: 0, Delta: &Delta{}, FinishReason: ptr(reason)}},
	}
}

type FullResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage"`
}

func NewFullResponse(id, model string, created int64, content string, parts []canonical.ContentPart, usage *Usage) FullResponse {
	hasNonText := false
	for _, p := range parts {
		if p.Type != canonical.PartText {
			hasNonText = true
			break
		}
	}
	msgContent := any(content)
	if hasNonText {
		arr := make([]map[string]any, 0, len(parts))
		for _, p := range parts {
			switch p.Type {
			case canonical.PartText:
				arr = append(arr, map[string]any{"type": "text", "text": p.Text})
			case canonical.PartImage:
				url := p.ImageURL
				if url == "" && p.AssetID != "" {
					url = "/epic-assets/" + p.AssetID
				}
				arr = append(arr, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			case canonical.PartFile:
				txt := "[file]"
				if p.FileName != "" {
					txt = "[file: " + p.FileName + "]"
				}
				arr = append(arr, map[string]any{"type": "text", "text": txt})
			}
		}
		if len(arr) > 0 {
			msgContent = arr
		}
	}
	return FullResponse{
		ID: id, Object: "chat.completion", Created: created, Model: model,
		Choices: []Choice{{Index: 0, Message: &RespMsg{Role: "assistant", Content: msgContent}, FinishReason: ptr("stop")}},
		Usage:   usage,
	}
}

func ptr(s string) *string { return &s }

func NowUnix() int64 { return time.Now().Unix() }

var _ = base64.StdEncoding
