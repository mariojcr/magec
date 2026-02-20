package contextguard

import (
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"

	"github.com/achetronic/magec/server/contextwindow"
)

// slidingWindowStrategy implements turn-count-based compaction. When the
// number of Content entries in the request exceeds maxTurns, the oldest
// entries beyond the limit are summarized using the agent's LLM and
// replaced with a single summary message.
//
// This is a preventive strategy: it fires periodically based on turn count
// rather than waiting for the context window to fill up. The buffer for
// dynamic word limits is still derived from the model's context window
// using the same two-tier logic as the threshold strategy.
type slidingWindowStrategy struct {
	registry *contextwindow.Registry
	llm      model.LLM
	maxTurns int
	mu       sync.Mutex
}

// newSlidingWindowStrategy creates a sliding window strategy for a single agent.
func newSlidingWindowStrategy(registry *contextwindow.Registry, llm model.LLM, maxTurns int) *slidingWindowStrategy {
	return &slidingWindowStrategy{
		registry: registry,
		llm:      llm,
		maxTurns: maxTurns,
	}
}

// Name returns the strategy identifier for logging.
func (s *slidingWindowStrategy) Name() string {
	return StrategySlidingWindow
}

// Compact checks whether the number of Content entries exceeds maxTurns.
// If so, it splits the conversation: everything beyond maxTurns from the
// end is summarized, and req.Contents is rewritten as
// [summary + last maxTurns entries].
func (s *slidingWindowStrategy) Compact(ctx agent.CallbackContext, req *model.LLMRequest) error {
	existingSummary := loadSummary(ctx)
	if existingSummary != "" {
		injectSummary(req, existingSummary)
	}

	if len(req.Contents) <= s.maxTurns {
		return nil
	}

	slog.Info("ContextGuard [sliding_window]: turn limit exceeded, summarizing",
		"agent", ctx.AgentName(),
		"session", ctx.SessionID(),
		"turns", len(req.Contents),
		"maxTurns", s.maxTurns,
	)

	s.mu.Lock()
	defer s.mu.Unlock()

	splitIdx := safeSplitIndex(req.Contents, len(req.Contents)-s.maxTurns)
	oldContents := req.Contents[:splitIdx]
	recentContents := req.Contents[splitIdx:]

	contextWindow := s.registry.ContextWindow(req.Model)
	buffer := computeBuffer(contextWindow)

	summary, err := summarize(ctx, s.llm, oldContents, existingSummary, buffer)
	if err != nil {
		return fmt.Errorf("summarization failed: %w", err)
	}

	tokenEstimate := estimateContentTokens(oldContents)
	persistSummary(ctx, summary, tokenEstimate)
	replaceSummary(req, summary, recentContents)

	slog.Info("ContextGuard [sliding_window]: conversation compressed",
		"agent", ctx.AgentName(),
		"session", ctx.SessionID(),
		"oldMessages", len(oldContents),
		"recentMessages", len(recentContents),
		"newTokenEstimate", estimateTokens(req),
	)

	return nil
}
