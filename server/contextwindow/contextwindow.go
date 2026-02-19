// Package contextwindow maintains an in-memory registry of LLM model metadata
// (context window sizes, costs, etc.) fetched from a remote JSON source.
// It is used by the ContextGuard plugin to know how many tokens each model
// supports so it can decide when to summarize the conversation.
package contextwindow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	// sourceURL points to the Crush provider.json that lists all supported
	// models with their context windows, pricing, and other metadata.
	sourceURL       = "https://raw.githubusercontent.com/charmbracelet/crush/main/internal/agent/hyper/provider.json"
	refreshInterval = 6 * time.Hour
	fetchTimeout    = 15 * time.Second

	// DefaultContextWindow is the fallback value (128k tokens) used when
	// a model ID is not found in the registry.
	DefaultContextWindow = 128000
)

// ModelInfo holds the metadata for a single LLM model as read from the
// remote provider.json file.
type ModelInfo struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	ContextWindow    int     `json:"context_window"`
	DefaultMaxTokens int     `json:"default_max_tokens"`
	CostPerMIn       float64 `json:"cost_per_1m_in"`
	CostPerMOut      float64 `json:"cost_per_1m_out"`
}

// providerJSON mirrors the top-level structure of the Crush provider.json
// file so we can unmarshal it directly.
type providerJSON struct {
	Models []ModelInfo `json:"models"`
}

// Registry is a thread-safe cache of model metadata. It fetches the data
// once on Start and refreshes it periodically in a background goroutine.
type Registry struct {
	mu     sync.RWMutex
	models map[string]ModelInfo
	cancel context.CancelFunc
}

// NewRegistry creates an empty Registry. Call Start to populate it and
// begin the periodic refresh loop.
func NewRegistry() *Registry {
	return &Registry{
		models: make(map[string]ModelInfo),
	}
}

// Start performs the initial fetch of model data and spawns a background
// goroutine that refreshes it every refreshInterval (6 hours).
func (r *Registry) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	r.fetch()

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.fetch()
			}
		}
	}()
}

// Stop cancels the background refresh goroutine.
func (r *Registry) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

// ContextWindow returns the context window size (in tokens) for the given
// model ID. If the model is not in the registry, DefaultContextWindow is
// returned.
func (r *Registry) ContextWindow(modelID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if info, ok := r.models[modelID]; ok && info.ContextWindow > 0 {
		return info.ContextWindow
	}
	return DefaultContextWindow
}

// Get returns full model metadata for the given ID and whether it was found.
func (r *Registry) Get(modelID string) (ModelInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.models[modelID]
	return info, ok
}

// fetch downloads the provider.json, parses it, and atomically replaces
// the in-memory model map. Any error is logged and silently ignored so
// the registry keeps serving stale data rather than failing.
func (r *Registry) fetch() {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		slog.Warn("Context window registry: failed to create request", "error", err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("Context window registry: fetch failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("Context window registry: unexpected status", "status", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		slog.Warn("Context window registry: read failed", "error", err)
		return
	}

	var provider providerJSON
	if err := json.Unmarshal(body, &provider); err != nil {
		slog.Warn("Context window registry: parse failed", "error", err)
		return
	}

	models := make(map[string]ModelInfo, len(provider.Models))
	for _, m := range provider.Models {
		models[m.ID] = m
	}

	r.mu.Lock()
	r.models = models
	r.mu.Unlock()

	slog.Info(fmt.Sprintf("Context window registry: loaded %d models", len(models)))
}
