// Package echo generates the repeated output content for a session.
package echo

import (
	"strings"

	"github.com/epicai/epicai/backend/internal/canonical"
	"github.com/epicai/epicai/backend/internal/tokenizer"
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

// AgentEchoSpec defines a synthesized tool call for agent test loops.
type AgentEchoSpec struct {
	ToolName  string
	Arguments string
	Subagent  bool
}

// BuildInfiniteMaxText produces repeated text aggregated into a single echo
// response whose token count targets the configured maxTokenChunk. Token
// accounting uses the EpicAI Canonical Tokenizer so the target holds for both
// ASCII and CJK input (CJK is ~1 token/char, not 4 chars/token).
func BuildInfiniteMaxText(text string, maxTokens int) string {
	if text == "" {
		text = "hello"
	}
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	per := tokenizer.Count(text)
	if per <= 0 {
		per = 1
	}
	repeats := maxTokens / per
	if repeats < 1 {
		repeats = 1
	}
	// Hard safety cap to avoid an accidental OOM from an absurd target.
	const maxRepeats = 500000
	if repeats > maxRepeats {
		repeats = maxRepeats
	}
	var sb strings.Builder
	sb.Grow(per * repeats * 4)
	for i := 0; i < repeats; i++ {
		sb.WriteString(text)
	}
	return sb.String()
}
// BuildAgentEchoCall analyzes available tool names and generates a tool call
// that commands a subagent or shell to echo the user's input.
func BuildAgentEchoCall(availableTools []string, userInput string) AgentEchoSpec {
	if strings.TrimSpace(userInput) == "" {
		userInput = "hello"
	}
	cleanInput := strings.ReplaceAll(userInput, `"`, `\"`)
	cleanInput = strings.ReplaceAll(cleanInput, "\n", " ")

	hasTool := func(name string) bool {
		for _, t := range availableTools {
			if strings.EqualFold(t, name) {
				return true
			}
		}
		return false
	}

	// 1. If opencode or codex subagent tool is declared:
	// Codex uses: "collaboration__spawn_agent" or "spawn_agent"
	// OpenCode uses: "task" or "subagent"
	if hasTool("collaboration__spawn_agent") || hasTool("spawn_agent") {
		tool := "collaboration__spawn_agent"
		if !hasTool("collaboration__spawn_agent") {
			tool = "spawn_agent"
		}
		args := `{"task_name":"echo_subtask","message":"Please execute shell command: echo \"` + cleanInput + `\""}`
		return AgentEchoSpec{ToolName: tool, Arguments: args, Subagent: true}
	}
	if hasTool("task") || hasTool("subagent") {
		tool := "task"
		if !hasTool("task") && hasTool("subagent") {
			tool = "subagent"
		}
		args := `{"description":"Echo subagent task","prompt":"Please execute shell command: echo \"` + cleanInput + `\"","subagent_type":"general"}`
		return AgentEchoSpec{ToolName: tool, Arguments: args, Subagent: true}
	}

	// 2. If shell/terminal tools are declared:
	if hasTool("exec_command") {
		args := `{"cmd":"echo \"` + cleanInput + `\""}`
		return AgentEchoSpec{ToolName: "exec_command", Arguments: args, Subagent: false}
	}
	if hasTool("powershell") {
		args := `{"command":"echo \"` + cleanInput + `\""}`
		return AgentEchoSpec{ToolName: "powershell", Arguments: args, Subagent: false}
	}
	if hasTool("bash") {
		args := `{"command":"echo \"` + cleanInput + `\""}`
		return AgentEchoSpec{ToolName: "bash", Arguments: args, Subagent: false}
	}
	if hasTool("execute_command") {
		args := `{"command":"echo \"` + cleanInput + `\""}`
		return AgentEchoSpec{ToolName: "execute_command", Arguments: args, Subagent: false}
	}
	if hasTool("terminal") {
		args := `{"command":"echo \"` + cleanInput + `\""}`
		return AgentEchoSpec{ToolName: "terminal", Arguments: args, Subagent: false}
	}

	// Default fallback: if any tool is present, pick the first one, else "bash"
	chosen := "bash"
	if len(availableTools) > 0 {
		chosen = availableTools[0]
	}
	args := `{"command":"echo \"` + cleanInput + `\""}`
	if chosen == "exec_command" {
		args = `{"cmd":"echo \"` + cleanInput + `\""}`
	}
	return AgentEchoSpec{ToolName: chosen, Arguments: args, Subagent: false}
}

// SubagentEchoMarker identifies the task prompt EpicAI sends to a subagent. It
// lets the server recognise a subagent's own model call and run a single shell
// echo instead of spawning yet more subagents (which would recurse infinitely).
const SubagentEchoMarker = "Please execute shell command: echo"

// IsSubagentEchoRequest reports whether userText is the prompt EpicAI sent to a
// subagent (as opposed to an end-user conversation message).
func IsSubagentEchoRequest(userText string) bool {
	return strings.Contains(userText, SubagentEchoMarker)
}

// ExtractEchoPayload returns the text the subagent was asked to echo, i.e. the
// content inside: Please execute shell command: echo "<payload>"
func ExtractEchoPayload(prompt string) string {
	i := strings.Index(prompt, "echo \"")
	if i < 0 {
		return prompt
	}
	rest := prompt[i+len("echo \""):]
	if j := strings.LastIndex(rest, "\""); j >= 0 {
		rest = rest[:j]
	}
	rest = strings.ReplaceAll(rest, `\"`, `"`)
	return rest
}

// PickShellTool chooses a shell/command tool from the declared tools, never a
// subagent tool (to avoid recursion). Returns "bash" or "exec_command" appropriately.
func PickShellTool(availableTools []string) (toolName string, argKey string) {
	preferred := []struct {
		name string
		arg  string
	}{
		{"exec_command", "cmd"},
		{"bash", "command"},
		{"powershell", "command"},
		{"execute_command", "command"},
		{"terminal", "command"},
		{"shell", "command"},
		{"run_command", "command"},
	}
	for _, p := range preferred {
		for _, t := range availableTools {
			if strings.EqualFold(t, p.name) {
				return t, p.arg
			}
		}
	}
	for _, t := range availableTools {
		if !strings.EqualFold(t, "task") &&
			!strings.EqualFold(t, "subagent") &&
			!strings.EqualFold(t, "spawn_agent") &&
			!strings.EqualFold(t, "collaboration__spawn_agent") &&
			!strings.EqualFold(t, "collaboration__followup_task") {
			return t, "command"
		}
	}
	return "bash", "command"
}
