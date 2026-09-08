// Package echo generates the repeated output content for a session.
package echo

import (
	"strings"

	"github.com/epicai/epicai/backend/internal/canonical"
)

// Echo content modes.
const (
	ModeExact   = "exact"   // ABCABCABC... continuous concatenation
	ModeLine    = "line"    // one repetition per line
	ModeMessage = "message" // one repetition per message/chunk (default)
	ModeBlock   = "block"   // repetitions grouped in blocks
)

func Normalize(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case ModeExact, ModeLine, ModeBlock:
		return m
	default:
		return ModeMessage
	}
}

// Unit is one emitted repetition.
type Unit struct {
	Text   string
	Parts  []canonical.ContentPart
	Prefix string // separator emitted before the content for line/block modes
}

// Streaming returns the delta to emit for the nth repetition (0-based) while
// streaming. Only the new text is produced so clients see a growing stream.
func Streaming(conv *canonical.Conversation, mode string, n int64) Unit {
	mode = Normalize(mode)
	text := conv.LastUserText()
	parts := conv.LastUserParts()
	if len(parts) == 0 && text != "" {
		parts = []canonical.ContentPart{{Type: canonical.PartText, Text: text}}
	}
	u := Unit{Text: text, Parts: parts}
	switch mode {
	case ModeLine, ModeBlock:
		if n > 0 {
			u.Prefix = "\n"
		}
	}
	return u
}

// NonStreaming returns the complete content for a single-shot response.
func NonStreaming(conv *canonical.Conversation, mode string, count int64) Unit {
	mode = Normalize(mode)
	text := conv.LastUserText()
	parts := conv.LastUserParts()
	if len(parts) == 0 && text != "" {
		parts = []canonical.ContentPart{{Type: canonical.PartText, Text: text}}
	}
	u := Unit{Parts: parts}
	if count < 1 {
		count = 1
	}
	switch mode {
	case ModeExact:
		u.Text = strings.Repeat(text, int(count))
	case ModeLine:
		var sb strings.Builder
		for i := int64(0); i < count; i++ {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(text)
		}
		u.Text = sb.String()
	case ModeBlock:
		var sb strings.Builder
		for i := int64(0); i < count; i++ {
			sb.WriteString(text)
			sb.WriteString("\n")
		}
		u.Text = strings.TrimRight(sb.String(), "\n")
	default:
		var sb strings.Builder
		for i := int64(0); i < count; i++ {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(text)
		}
		u.Text = sb.String()
	}
	return u
}

// Accumulate is the full text after n streaming repetitions, used for
// non-streaming fallbacks and for the admin conversation view.
func Accumulate(mode string, n int64, text string) string {
	mode = Normalize(mode)
	switch mode {
	case ModeExact:
		return strings.Repeat(text, int(n))
	default:
		var sb strings.Builder
		for i := int64(0); i < n; i++ {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(text)
		}
		return sb.String()
	}
}
