package scenario

import "github.com/epicai/epicai/backend/internal/tokenizer"

func canonicalTokenCount(s string) int { return tokenizer.Count(s) }
