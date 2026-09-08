// Package canonical defines the unified internal message/event model that all
// OpenAI-style protocols are parsed into before reaching the engines.
package canonical

import (
	"strings"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleDeveloper Role = "developer"
)

type PartType string

const (
	PartText    PartType = "text"
	PartImage   PartType = "image"
	PartFile    PartType = "file"
	PartAudio   PartType = "audio"
	PartUnknown PartType = "unknown"
)

// ContentPart is a single piece of multimodal content. The original raw form is
// always preserved so nothing is lost when parsing protocol specific payloads.
type ContentPart struct {
	Type PartType `json:"type"`

	// Text
	Text string `json:"text,omitempty"`

	// Image
	ImageURL    string `json:"image_url,omitempty"`    // http(s) or data: URL
	ImageBase64 string `json:"image_base64,omitempty"` // raw base64 without prefix
	MimeType    string `json:"mime_type,omitempty"`

	// File
	FileID   string `json:"file_id,omitempty"`
	FileName string `json:"file_name,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	FileData string `json:"file_data,omitempty"` // base64 payload

	// Resolved asset (EpicAI Asset Store) for binary payloads.
	AssetID string `json:"asset_id,omitempty"`

	// Raw keeps the original protocol JSON for lossless inspection.
	Raw map[string]any `json:"raw,omitempty"`

	// Metadata captured at parse time.
	Meta map[string]any `json:"meta,omitempty"`
}

// Message is a protocol independent conversation turn.
type Message struct {
	Role  Role           `json:"role"`
	Parts []ContentPart  `json:"parts"`
	Name  string         `json:"name,omitempty"`
	Raw   map[string]any `json:"raw,omitempty"`
}

// Conversation is the canonical input of one request.
type Conversation struct {
	Messages []Message `json:"messages"`
}

// Text concatenates every text part with a newline separator.
func (c *Conversation) Text() string {
	if c == nil {
		return ""
	}
	var out []string
	for _, m := range c.Messages {
		for _, p := range m.Parts {
			if p.Type == PartText && p.Text != "" {
				out = append(out, p.Text)
			}
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n")
}

// LastUserText returns the most recent user text, which is what echo repeats.
func (c *Conversation) LastUserText() string {
	if c == nil {
		return ""
	}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role != RoleUser && m.Role != RoleSystem && m.Role != RoleDeveloper {
			continue
		}
		var texts []string
		for _, p := range m.Parts {
			if p.Type == PartText && p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	return c.Text()
}

// Parts returns the flattened content parts of the last user message, preserving
// the exact ordering (TEXT/IMAGE/FILE/IMAGE/TEXT ...).
func (c *Conversation) LastUserParts() []ContentPart {
	if c == nil {
		return nil
	}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == RoleUser {
			return c.Messages[i].Parts
		}
	}
	if len(c.Messages) > 0 {
		return c.Messages[len(c.Messages)-1].Parts
	}
	return nil
}

// AllParts flattens every part of the conversation in order.
func (c *Conversation) AllParts() []ContentPart {
	if c == nil {
		return nil
	}
	var out []ContentPart
	for _, m := range c.Messages {
		out = append(out, m.Parts...)
	}
	return out
}

type Protocol string

const (
	ProtocolChatCompletions Protocol = "chat.completions"
	ProtocolResponses       Protocol = "responses"
	ProtocolLegacy          Protocol = "completions"
)

// OutgoingEvent is produced by engines and consumed by protocol adapters.
type OutgoingEvent struct {
	Type string // text | image | file | finish | error | raw
	Text string
	Part *ContentPart
	Raw  map[string]any
	At   time.Time
}
