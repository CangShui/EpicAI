// Package tokenizer implements the EpicAI Canonical Tokenizer.
//
// Token accounting is deterministic: the same input always yields the same
// token count on every deployment. It approximates a BPE-style English
// tokenizer with special handling for CJK characters, emoji and whitespace.
package tokenizer

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const Name = "EpicAI Canonical Tokenizer"

var global = New()

func New() *Tokenizer { return &Tokenizer{} }

type Tokenizer struct{}

// Count returns the canonical token count of s.
func (t *Tokenizer) Count(s string) int {
	return Count(s)
}

func Count(s string) int { return countString(s) }

func countString(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch {
		case r == '\n':
			// newline usually merges with preceding token; count as 1 only when alone
			n++
			i++
		case unicode.IsSpace(r):
			// collapse runs of spaces/tabs: run of k -> ceil(k/4) roughly, min 1
			j := i
			for j < len(runes) && runes[j] != '\n' && unicode.IsSpace(runes[j]) {
				j++
			}
			k := j - i
			n += (k + 3) / 4
			if n == 0 {
				n = 1
			}
			i = j
		case isCJK(r):
			// CJK characters are typically 1-2 tokens each; use 1 per char.
			n++
			i++
		case r > 0xFFFF:
			// emoji / astral plane: usually multiple tokens
			n += 2
			i++
		case unicode.IsDigit(r):
			// digits group up to 3 per token like GPT tokenizers
			j := i
			for j < len(runes) && unicode.IsDigit(runes[j]) && j-i < 3 {
				j++
			}
			n++
			i = j
		case isWordRune(r):
			// latin word: ~4 chars per token
			j := i
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
			k := j - i
			n += (k + 3) / 4
			i = j
		default:
			// punctuation: usually 1 token
			n++
			i++
		}
	}
	return n
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) && r <= 0xFFFF && !isCJK(r)
}

func isCJK(r rune) bool {
	if r < 0x1100 {
		return false
	}
	switch {
	case r >= 0x1100 && r <= 0x11FF: // Hangul Jamo
		return true
	case r >= 0x2E80 && r <= 0x2EFF: // CJK Radicals
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK Symbols and Punctuation
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0x3130 && r <= 0x318F: // Hangul Compatibility Jamo
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Ext A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified
		return true
	case r >= 0xA960 && r <= 0xA97F: // Hangul Jamo Ext A
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul Syllables
		return true
	case r >= 0xD7B0 && r <= 0xD7FF: // Hangul Jamo Ext B
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility
		return true
	case r >= 0xFF00 && r <= 0xFF60: // Fullwidth forms
		return true
	case r >= 0xFFE0 && r <= 0xFFE6:
		return true
	case r >= 0x1B000 && r <= 0x1B0FF: // Kana supplement
		return true
	case r >= 0x20000 && r <= 0x2A6DF: // CJK Ext B
		return true
	case r >= 0x2F800 && r <= 0x2FA1F: // CJK Compatibility Supplement
		return true
	}
	return false
}

// CountParts counts tokens across multimodal parts. Non-text parts are charged
// a fixed overhead so throughput accounting stays comparable.
func CountParts(parts []PartLike) int {
	n := 0
	for _, p := range parts {
		switch p.Kind() {
		case "text":
			n += Count(p.TextValue())
		case "image":
			n += 85 // image reference overhead
		case "file":
			n += 32 // file reference overhead
		default:
			n += 8
		}
	}
	return n
}

type PartLike interface {
	Kind() string
	TextValue() string
}

// CountBytesJSON estimates tokens for a serialized JSON payload, used when the
// adapter needs to account for whole serialized chunks.
func EstimateFromJSON(b []byte) int {
	return Count(string(b))
}

var _ = utf8.RuneLen
var _ = strings.TrimSpace
