package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adka2a"
	"google.golang.org/adk/session"

	"github.com/achetronic/magec/server/store"
)

const protocolVersion = "0.2.5"

type Handler struct {
	mu        sync.RWMutex
	handlers  map[string]http.Handler // agentID → JSON-RPC handler
	cards     map[string]*a2a.AgentCard
	publicURL string
}

func NewHandler(publicURL string) *Handler {
	return &Handler{
		handlers:  make(map[string]http.Handler),
		cards:     make(map[string]*a2a.AgentCard),
		publicURL: strings.TrimRight(publicURL, "/"),
	}
}

func (h *Handler) Rebuild(agents []store.AgentDefinition, adkAgents map[string]agent.Agent, sessionSvc session.Service, memorySvc memory.Service) {
	handlers := make(map[string]http.Handler)
	cards := make(map[string]*a2a.AgentCard)

	for _, agentDef := range agents {
		if agentDef.A2A == nil || !agentDef.A2A.Enabled {
			continue
		}
		adkAgent, ok := adkAgents[agentDef.ID]
		if !ok {
			slog.Warn("A2A: agent not found in ADK map", "agent", agentDef.ID)
			continue
		}

		invokeURL := fmt.Sprintf("%s/api/v1/a2a/%s", h.publicURL, agentDef.ID)

		card := &a2a.AgentCard{
			Name:               agentDef.Name,
			Description:        agentDef.Description,
			URL:                invokeURL,
			Version:            "1.0.0",
			ProtocolVersion:    protocolVersion,
			PreferredTransport: a2a.TransportProtocolJSONRPC,
			DefaultInputModes:  []string{"text/plain"},
			DefaultOutputModes: []string{"text/plain"},
			Capabilities: a2a.AgentCapabilities{
				Streaming: true,
			},
			Skills: adka2a.BuildAgentSkills(adkAgent),
			SecuritySchemes: a2a.NamedSecuritySchemes{
				"bearer": a2a.HTTPAuthSecurityScheme{
					Scheme:      "bearer",
					Description: "Magec client token",
				},
			},
			Security: []a2a.SecurityRequirements{
				{"bearer": a2a.SecuritySchemeScopes{}},
			},
		}
		cards[agentDef.ID] = card

		execCfg := adka2a.ExecutorConfig{
			RunnerConfig: runner.Config{
				AppName:        agentDef.ID,
				Agent:          adkAgent,
				SessionService: sessionSvc,
				MemoryService:  memorySvc,
			},
		}
		executor := adka2a.NewExecutor(execCfg)
		reqHandler := a2asrv.NewHandler(executor, a2asrv.WithLogger(slog.Default()))
		handlers[agentDef.ID] = a2asrv.NewJSONRPCHandler(reqHandler)

		slog.Info("A2A endpoint enabled", "agent", agentDef.ID, "name", agentDef.Name)
	}

	h.mu.Lock()
	h.handlers = handlers
	h.cards = cards
	h.mu.Unlock()
}

func (h *Handler) ServeAgentCard(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent")

	h.mu.RLock()
	cards := h.cards
	h.mu.RUnlock()

	if agentID != "" {
		card, ok := cards[agentID]
		if !ok {
			http.Error(w, `{"error":"agent not found or A2A not enabled"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(card)
		return
	}

	cardList := make([]*a2a.AgentCard, 0, len(cards))
	for _, card := range cards {
		cardList = append(cardList, card)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(cardList)
}

func (h *Handler) ServePerAgentCard(w http.ResponseWriter, r *http.Request) {
	agentID := extractAgentID(r.URL.Path, "/api/v1/a2a/", "/.well-known/agent-card.json")

	h.mu.RLock()
	card, ok := h.cards[agentID]
	h.mu.RUnlock()

	if !ok {
		http.Error(w, `{"error":"agent not found or A2A not enabled"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(card)
}

func (h *Handler) ServeJSONRPC(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/.well-known/agent-card.json") {
		h.ServePerAgentCard(w, r)
		return
	}

	agentID := extractAgentID(r.URL.Path, "/api/v1/a2a/", "")

	h.mu.RLock()
	handler, ok := h.handlers[agentID]
	h.mu.RUnlock()

	if !ok {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"agent not found or A2A not enabled"}`, http.StatusNotFound)
		return
	}

	handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentIDKey, agentID)))
}

type contextKey string

const agentIDKey contextKey = "a2a-agent-id"

func extractAgentID(path, prefix, suffix string) string {
	path = strings.TrimPrefix(path, prefix)
	if suffix != "" {
		path = strings.TrimSuffix(path, suffix)
	}
	path = strings.TrimSuffix(path, "/")
	if idx := strings.Index(path, "/"); idx >= 0 {
		path = path[:idx]
	}
	return path
}
