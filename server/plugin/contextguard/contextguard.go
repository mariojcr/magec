// Package contextguard implements an ADK plugin that prevents conversations
// from exceeding the LLM's context window. Before every model call it
// delegates to a configurable Strategy that decides whether and how to
// compact the conversation history.
//
// Two strategies are provided out of the box:
//
//   - ThresholdStrategy: estimates token count and summarizes when the
//     remaining capacity drops below a safety buffer (two-tier: fixed 20k
//     for large windows, 20% for small ones). This is a reactive guard.
//
//   - SlidingWindowStrategy: compacts when the number of Content entries
//     exceeds a configured maximum, regardless of token count. This is a
//     preventive, periodic compaction based on turn count.
//
// Both strategies use the agent's own LLM for summarization and share the
// same structured system prompt, state keys, and helper functions.
package contextguard

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"

	"github.com/achetronic/magec/server/contextwindow"
)

const (
	// StrategyThreshold selects the token-threshold strategy: summarization
	// fires when estimated token usage approaches the model's context window.
	StrategyThreshold = "threshold"

	// StrategySlidingWindow selects the sliding-window strategy: summarization
	// fires when the number of Content entries exceeds a configured limit.
	StrategySlidingWindow = "sliding_window"
)

const (
	// stateKeyPrefixSummary is the prefix for the per-agent session state key
	// where the running conversation summary is stored between requests.
	stateKeyPrefixSummary = "__context_guard_summary_"
	// stateKeyPrefixSummarizedAt is the prefix for the per-agent diagnostic
	// key recording the token count at which summarization was triggered.
	stateKeyPrefixSummarizedAt = "__context_guard_summarized_at_"
	// stateKeyPrefixContentsAtCompaction is the prefix for the per-agent key
	// that records the total number of Content entries at last compaction.
	stateKeyPrefixContentsAtCompaction = "__context_guard_contents_at_compaction_"

	// largeContextWindowThreshold is the boundary (in tokens) between
	// "small" and "large" context windows, matching Crush's constant.
	largeContextWindowThreshold = 200_000
	// largeContextWindowBuffer is the fixed token buffer reserved on
	// models with large context windows (>200k). Summarization fires
	// when remaining capacity drops to this level.
	largeContextWindowBuffer = 20_000
	// smallContextWindowRatio is the fraction of the context window
	// reserved as buffer on smaller models (<= 200k).
	smallContextWindowRatio = 0.20

	// recentWindowRatio is the fraction of the context window reserved
	// for recent messages that are kept verbatim after summarization.
	recentWindowRatio = 0.20
)

// summarizeSystemPrompt is the system instruction given to the LLM when it
// is asked to produce a conversation summary. It is domain-agnostic (works
// for any kind of agent) but structurally rigorous — inspired by Crush's
// summary template — so the resulting summary is self-contained enough to
// continue the conversation with zero prior context.
const summarizeSystemPrompt = `You are summarizing a conversation to preserve context for continuing later.

Critical: This summary will be the ONLY context available when the conversation resumes. Assume all previous messages will be lost. Be thorough.

Required sections:

## Current State

- What was being discussed or worked on (exact user request if applicable)
- Current progress and what has been completed
- What was being addressed right now (incomplete work or open thread)
- What remains to be done or answered (specific, not vague)

## Key Information

- Facts, data, and specific details mentioned (names, dates, numbers, URLs, identifiers)
- User preferences, instructions, and constraints stated during the conversation
- Definitions, terminology, or domain knowledge established
- Any external resources, references, or sources mentioned

## Context & Decisions

- Decisions made during the conversation and why
- Alternatives that were considered and discarded (and why)
- Assumptions made
- Important clarifications or corrections that occurred
- Any blockers, risks, or open questions identified

## Exact Next Steps

Be specific. Don't write "continue with the task" — write exactly what should happen next, with enough detail that someone reading only this summary can pick up without asking questions.

Tone: Write as if briefing a colleague taking over mid-conversation. Include everything they would need to continue without asking questions. Write in the same language as the conversation.

Length: A dynamic word limit will be appended to this prompt at runtime based on the model's buffer size. Within that limit, err on the side of too much detail rather than too little. Critical context is worth the tokens.`

// defaultMaxTurns is the default Content entry limit used by the sliding
// window strategy when no per-agent value is configured.
const defaultMaxTurns = 20

// Strategy defines how a compaction algorithm decides whether and how to
// compact conversation history before an LLM call.
type Strategy interface {
	// Name returns a human-readable identifier for logging.
	Name() string

	// Compact inspects the request and, if compaction is needed, rewrites
	// req.Contents in place. It returns nil on success (whether or not
	// compaction was performed). Returning an error signals that compaction
	// was attempted but failed; the caller should pass the request through
	// unchanged.
	Compact(ctx agent.CallbackContext, req *model.LLMRequest) error
}

// Config holds everything the plugin needs: the context window registry
// to look up model limits, a map of agent-ID → LLM so each agent
// can summarize with its own model, and per-agent strategy selection.
type Config struct {
	Registry   *contextwindow.Registry
	Models     map[string]model.LLM
	Strategies map[string]string // agent ID → StrategyThreshold | StrategySlidingWindow
	MaxTurns   map[string]int    // agent ID → max Content entries (sliding window only)
}

// NewPluginConfig creates a runner.PluginConfig ready to be passed to the
// ADK launcher. It wraps a single "context_guard" plugin whose
// BeforeModelCallback intercepts every LLM call and delegates to the
// per-agent strategy.
func NewPluginConfig(cfg Config) runner.PluginConfig {
	strategies := make(map[string]Strategy, len(cfg.Models))
	for agentID, llm := range cfg.Models {
		strategyName := cfg.Strategies[agentID]
		switch strategyName {
		case StrategySlidingWindow:
			maxTurns := cfg.MaxTurns[agentID]
			if maxTurns <= 0 {
				maxTurns = defaultMaxTurns
			}
			strategies[agentID] = newSlidingWindowStrategy(cfg.Registry, llm, maxTurns)
		default:
			strategies[agentID] = newThresholdStrategy(cfg.Registry, llm)
		}
		slog.Info("ContextGuard: strategy configured",
			"agent", agentID,
			"strategy", strategies[agentID].Name(),
		)
	}

	guard := &contextGuard{strategies: strategies}

	p, _ := plugin.New(plugin.Config{
		Name:                "context_guard",
		BeforeModelCallback: llmagent.BeforeModelCallback(guard.beforeModel),
	})

	return runner.PluginConfig{
		Plugins: []*plugin.Plugin{p},
	}
}

// contextGuard is the internal state of the plugin, shared across all
// callback invocations. It holds per-agent strategies.
type contextGuard struct {
	strategies map[string]Strategy
}

// beforeModel is the BeforeModelCallback invoked by ADK before every LLM
// call. It looks up the agent's strategy and delegates compaction to it.
// If the strategy returns an error the request is passed through unchanged.
func (g *contextGuard) beforeModel(ctx agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil || len(req.Contents) == 0 {
		return nil, nil
	}

	strategy, ok := g.strategies[ctx.AgentName()]
	if !ok {
		return nil, nil
	}

	if err := strategy.Compact(ctx, req); err != nil {
		slog.Warn("ContextGuard: compaction failed, passing through",
			"agent", ctx.AgentName(),
			"strategy", strategy.Name(),
			"error", err,
		)
	}

	return nil, nil
}

// --- Shared helpers used by both strategies ---

// loadSummary reads the running conversation summary from session state.
// Returns an empty string if no summary has been stored yet.
func loadSummary(ctx agent.CallbackContext) string {
	key := stateKeyPrefixSummary + ctx.AgentName()
	val, err := ctx.State().Get(key)
	if err != nil {
		return ""
	}
	s, _ := val.(string)
	return s
}

// persistSummary writes the summary and a diagnostic token count to session
// state. Errors are logged but not propagated — failing to persist should
// not block the request.
func persistSummary(ctx agent.CallbackContext, summary string, tokenCount int) {
	keySummary := stateKeyPrefixSummary + ctx.AgentName()
	keySummarizedAt := stateKeyPrefixSummarizedAt + ctx.AgentName()
	if err := ctx.State().Set(keySummary, summary); err != nil {
		slog.Warn("ContextGuard: failed to persist summary", "error", err)
	}
	if err := ctx.State().Set(keySummarizedAt, tokenCount); err != nil {
		slog.Warn("ContextGuard: failed to persist token count", "error", err)
	}
}

// loadContentsAtCompaction reads the Content count recorded at the last
// compaction. Returns 0 if no compaction has happened yet.
func loadContentsAtCompaction(ctx agent.CallbackContext) int {
	key := stateKeyPrefixContentsAtCompaction + ctx.AgentName()
	val, err := ctx.State().Get(key)
	if err != nil {
		return 0
	}
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

// persistContentsAtCompaction records the total Content count at which
// compaction was performed, so the next call can compute turns since then.
func persistContentsAtCompaction(ctx agent.CallbackContext, count int) {
	key := stateKeyPrefixContentsAtCompaction + ctx.AgentName()
	if err := ctx.State().Set(key, count); err != nil {
		slog.Warn("ContextGuard: failed to persist contents count", "error", err)
	}
}

// summarize calls the given LLM to produce a concise summary of the provided
// conversation contents. The bufferTokens parameter controls the dynamic word
// limit: the summary may use up to 50% of the buffer, converted to words at
// a 0.75 words-per-token ratio. MaxOutputTokens is also set as a hard safety
// net. If the LLM returns an empty response, a mechanical fallback summary
// (truncated excerpts) is used instead.
func summarize(ctx context.Context, llm model.LLM, contents []*genai.Content, previousSummary string, bufferTokens int) (string, error) {
	maxOutputTokens := int32(float64(bufferTokens) * 0.50)
	maxWords := int(float64(maxOutputTokens) * 0.75)

	systemPrompt := summarizeSystemPrompt + fmt.Sprintf("\n\nKeep the summary under %d words.", maxWords)
	userPrompt := buildSummarizePrompt(contents, previousSummary)

	req := &model.LLMRequest{
		Model: llm.Name(),
		Contents: []*genai.Content{
			{
				Role:  "user",
				Parts: []*genai.Part{{Text: userPrompt}},
			},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: systemPrompt}},
			},
			MaxOutputTokens: maxOutputTokens,
		},
	}

	var result string
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", fmt.Errorf("summarization LLM call failed: %w", err)
		}
		if resp != nil && resp.Content != nil {
			for _, part := range resp.Content.Parts {
				if part != nil && part.Text != "" {
					result += part.Text
				}
			}
		}
	}

	if result == "" {
		return buildFallbackSummary(contents, previousSummary), nil
	}

	return result, nil
}

// buildSummarizePrompt assembles the user-facing prompt sent to the LLM for
// summarization: a request to summarize, any previous summary for continuity,
// and a transcript of the conversation contents.
func buildSummarizePrompt(contents []*genai.Content, previousSummary string) string {
	var sb strings.Builder
	sb.WriteString("Provide a detailed summary of the following conversation.")
	sb.WriteString("\n\n")

	if previousSummary != "" {
		sb.WriteString("[Previous summary for context]\n")
		sb.WriteString(previousSummary)
		sb.WriteString("\n[End previous summary]\n\n")
		sb.WriteString("Incorporate the previous summary into your new summary, updating any information that has changed.\n\n")
	}

	sb.WriteString("[Conversation to summarize]\n")

	for _, content := range contents {
		if content == nil {
			continue
		}
		role := content.Role
		if role == "" {
			role = "unknown"
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				sb.WriteString(role)
				sb.WriteString(": ")
				sb.WriteString(part.Text)
				sb.WriteString("\n")
			}
			if part.FunctionCall != nil {
				sb.WriteString(role)
				sb.WriteString(": [called tool: ")
				sb.WriteString(part.FunctionCall.Name)
				sb.WriteString("]\n")
			}
			if part.FunctionResponse != nil {
				sb.WriteString(role)
				sb.WriteString(": [tool ")
				sb.WriteString(part.FunctionResponse.Name)
				sb.WriteString(" returned a result]\n")
			}
		}
	}
	sb.WriteString("[End of conversation]\n")

	return sb.String()
}

// buildFallbackSummary creates a best-effort summary without an LLM by
// concatenating the first 200 characters of each message. Used when the
// real summarization call fails or returns empty.
func buildFallbackSummary(contents []*genai.Content, previousSummary string) string {
	var sb strings.Builder
	if previousSummary != "" {
		sb.WriteString(previousSummary)
		sb.WriteString("\n\n---\n\n")
	}
	for _, content := range contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part != nil && part.Text != "" {
				role := content.Role
				if role == "" {
					role = "unknown"
				}
				sb.WriteString(role)
				sb.WriteString(": ")
				if len(part.Text) > 200 {
					sb.WriteString(part.Text[:200])
					sb.WriteString("...")
				} else {
					sb.WriteString(part.Text)
				}
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}

// estimateTokens returns a rough token count for the entire LLM request
// (contents + system instruction) using the ~4 chars ≈ 1 token heuristic.
func estimateTokens(req *model.LLMRequest) int {
	total := 0
	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				total += len(part.Text) / 4
			}
			if part.FunctionCall != nil {
				total += len(part.FunctionCall.Name) / 4
				for k, v := range part.FunctionCall.Args {
					total += len(k) / 4
					total += len(fmt.Sprintf("%v", v)) / 4
				}
			}
			if part.FunctionResponse != nil {
				total += len(part.FunctionResponse.Name) / 4
				total += len(fmt.Sprintf("%v", part.FunctionResponse.Response)) / 4
			}
		}
	}
	if req.Config != nil && req.Config.SystemInstruction != nil {
		for _, part := range req.Config.SystemInstruction.Parts {
			if part != nil && part.Text != "" {
				total += len(part.Text) / 4
			}
		}
	}
	return total
}

// estimateContentTokens returns a rough token count for a slice of Content
// entries using the ~4 chars ≈ 1 token heuristic.
func estimateContentTokens(contents []*genai.Content) int {
	total := 0
	for _, content := range contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				total += len(part.Text) / 4
			}
		}
	}
	return total
}

// findSplitIndex determines where to split Contents into "old" (to be
// summarized) and "recent" (to keep verbatim). It walks backwards from
// the end of the slice, accumulating tokens until recentBudget is reached,
// and returns the index that separates old from recent.
func findSplitIndex(contents []*genai.Content, recentBudget int) int {
	tokens := 0
	for i := len(contents) - 1; i >= 0; i-- {
		if contents[i] == nil {
			continue
		}
		for _, part := range contents[i].Parts {
			if part != nil && part.Text != "" {
				tokens += len(part.Text) / 4
			}
		}
		if tokens >= recentBudget {
			if i < len(contents)-2 {
				return safeSplitIndex(contents, i+1)
			}
			return safeSplitIndex(contents, len(contents)-2)
		}
	}
	if len(contents) > 2 {
		return safeSplitIndex(contents, len(contents)/2)
	}
	return safeSplitIndex(contents, 1)
}

// safeSplitIndex adjusts a candidate split index so it never lands right
// after an assistant message containing tool_use blocks. If the first
// "recent" message (at idx) is a user message with tool_result parts, the
// split is moved back to include the preceding assistant tool_use message
// in the recent window. This prevents orphaned tool_result blocks that
// Anthropic rejects with "unexpected tool_use_id in tool_result".
func safeSplitIndex(contents []*genai.Content, idx int) int {
	if idx <= 0 || idx >= len(contents) {
		return idx
	}
	c := contents[idx]
	if c == nil || c.Role != "user" {
		return idx
	}
	for _, part := range c.Parts {
		if part != nil && part.FunctionResponse != nil {
			if idx > 0 {
				return idx - 1
			}
			return idx
		}
	}
	return idx
}

// injectSummary prepends a summary content block to the request if one
// doesn't already exist. It checks the first message to avoid injecting
// a duplicate when the summary was already loaded from state.
func injectSummary(req *model.LLMRequest, summary string) {
	summaryText := fmt.Sprintf("[Previous conversation summary]\n%s\n[End of summary — conversation continues below]", summary)

	if len(req.Contents) > 0 && req.Contents[0] != nil &&
		req.Contents[0].Role == "user" && len(req.Contents[0].Parts) > 0 {
		first := req.Contents[0]
		if first.Parts[0] != nil && first.Parts[0].Text != "" &&
			strings.HasPrefix(first.Parts[0].Text, "[Previous conversation summary]") {
			return
		}
	}

	summaryContent := &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			{Text: summaryText},
		},
	}
	req.Contents = append([]*genai.Content{summaryContent}, req.Contents...)
}

// replaceSummary rewrites req.Contents to [summary message + recent messages].
func replaceSummary(req *model.LLMRequest, summary string, recentContents []*genai.Content) {
	summaryContent := &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			{Text: fmt.Sprintf("[Previous conversation summary]\n%s\n[End of summary — conversation continues below]", summary)},
		},
	}
	req.Contents = append([]*genai.Content{summaryContent}, recentContents...)
}

// computeBuffer returns the token buffer for a given context window using
// the same two-tier strategy as Crush:
//   - Large windows (>200k): fixed 20k buffer
//   - Small windows (≤200k): 20% of the window
func computeBuffer(contextWindow int) int {
	if contextWindow > largeContextWindowThreshold {
		return largeContextWindowBuffer
	}
	return int(float64(contextWindow) * smallContextWindowRatio)
}
