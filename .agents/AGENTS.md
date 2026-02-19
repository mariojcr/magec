# AGENTS.md - Magec

Self-hosted multi-agent AI platform with voice, visual workflows, and tool integration.

## Project Overview

**Magec** is a multi-agent AI platform that runs on your server. Named after the Guanche god of the Sun (/maˈxek/), it provides:

- **Multi-agent system**: Per-agent LLM, memory, voice, and tools. Hot-reload from the Admin UI.
- **Agentic Flows**: Visual drag-and-drop editor. Sequential, parallel, loop, nested.
- **Any LLM backend**: OpenAI, Anthropic, Gemini, Ollama, or any OpenAI-compatible API.
- **MCP tools**: Home Assistant, GitHub, databases, and hundreds more via Model Context Protocol. HTTP headers and TLS skip supported.
- **Memory**: Session (Redis) + long-term semantic (PostgreSQL/pgvector).
- **Voice**: Wake word, VAD, STT, TTS. All server-side via ONNX Runtime. Privacy-first.
- **Clients**: Voice UI (PWA), Admin UI, Telegram, webhooks, cron, REST API.

### Clients

| Client | Type | Description |
|--------|------|-------------|
| **Voice UI** | `direct` | Vue 3 PWA with voice/text chat, wake word detection, audio visualizer |
| **Telegram** | `telegram` | Text and voice messages |
| **Slack** | `slack` | Socket Mode (WebSocket, no public URL). DMs and @mentions. See `.agents/SLACK_CLIENT.md` |
| **Webhook** | `webhook` | HTTP endpoint for external integrations (fixed command or passthrough prompt) |
| **Cron** | `cron` | Scheduled task that fires a command against agents on a schedule |

## Architecture

```
magec/
├── server/                     # Go backend
│   ├── main.go                 # HTTP server (:8080 user + :8081 admin), routing, middleware
│   ├── agent/
│   │   ├── agent.go            # Multi-agent ADK setup, MCP transport, memory tools
│   │   ├── flow.go             # Flow→ADK workflow agent builder (sequential/parallel/loop)
│   │   └── base_toolset.go     # Base tools
│   ├── api/
│   │   ├── admin/              # Admin REST API (CRUD for all resources)
│   │   │   ├── handler.go      # Router + helpers
│   │   │   ├── agents.go       # Agent CRUD + MCP linking
│   │   │   ├── backends.go     # Backend CRUD
│   │   │   ├── clients.go      # Client CRUD + /types (JSON Schema) + token regen
│   │   │   ├── commands.go     # Command CRUD
│   │   │   ├── skills.go       # Skill CRUD + reference file upload/download/delete
│   │   │   ├── memory.go       # Memory provider CRUD + health check + /types
│   │   │   ├── flows.go        # Flow CRUD + recursive validation
│   │   │   ├── conversations.go # Conversation audit (list/get/delete/clear/stats/summary)
│   │   │   └── docs/           # Generated swagger
│   │   └── user/               # User-facing REST API
│   │       ├── handlers.go     # Health, ClientInfo, Voice, Webhook swagger types
│   │       ├── doc.go          # Swagger metadata
│   │       └── docs/           # Generated swagger (userapi)
│   ├── middleware/
│   │   ├── middleware.go       # AccessLog (httpsnoop), CORS, ClientAuth
│   │   ├── recorder.go         # ConversationRecorder (captures /run + /run_sse)
│   │   └── flowfilter.go       # Flow response filtering by responseAgent
│   ├── clients/                # Client type registry + runtime
│   │   ├── provider.go         # Provider interface: Type(), DisplayName(), ConfigSchema()
│   │   ├── registry.go         # Register(), ValidateConfig() with oneOf support
│   │   ├── executor.go         # RunClient() — executes commands against all allowedAgents
│   │   ├── direct/spec.go      # Direct provider (empty schema)
│   │   ├── telegram/           # Telegram bot (spec.go + bot.go)
│   │   ├── slack/              # Slack Socket Mode bot (spec.go + bot.go)
│   │   ├── webhook/            # Webhook handler (spec.go + handler.go)
│   │   └── cron/               # Cron scheduler (spec.go + cron.go + scheduler.go)
│   ├── memory/                 # Extensible memory provider registry
│   │   ├── provider.go         # Provider interface, Category, HealthResult
│   │   ├── registry.go         # Register(), Get(), All(), ValidTypeForCategory()
│   │   ├── redis/redis.go      # Redis provider (session)
│   │   └── postgres/postgres.go # Postgres provider (longterm, pgvector)
│   ├── store/                  # In-memory store + JSON persistence
│   │   ├── store.go            # Load/Save, CRUD, migration chain, OnChange()
│   │   ├── types.go            # All entity types (MCPServer includes Headers + Insecure)
│   │   └── conversations.go    # ConversationStore (data/conversations.json)
│   ├── schema/validate.go      # JSON Schema validation (google/jsonschema-go)
│   ├── config/config.go        # YAML config parsing (server + voice + log)
│   ├── logging/logging.go      # Structured logging (slog)
│   ├── voice/                  # Server-side voice detection (ONNX)
│   │   ├── detector.go         # OpenWakeWord inference
│   │   ├── vad.go              # Silero VAD inference
│   │   ├── handler.go          # WebSocket handler for audio streaming
│   │   └── resampler.go        # Audio resampling to 16kHz
│   ├── frontend/               # Embedded UI dist files (//go:embed)
│   │   ├── embed.go
│   │   ├── admin-ui/           # Built admin UI (copied by Makefile)
│   │   └── voice-ui/           # Built voice UI (copied by Makefile)
│   └── models/                 # Embedded ONNX models
│       ├── embed.go
│       ├── wakeword/           # Wake word models
│       └── auxiliary/          # mel-spec, VAD, embeddings
├── frontend/
│   ├── admin-ui/               # Admin UI (Vue 3 + Vite + Tailwind v4 + Pinia)
│   │   ├── src/
│   │   │   ├── main.js         # Vue app entry with Pinia
│   │   │   ├── App.vue         # Layout, sidebar, global ConfirmDialog/Toast/SearchPalette
│   │   │   ├── style.css       # Tailwind v4 @theme (piedra/atlantico/lava/sol/arena)
│   │   │   ├── lib/api/        # Fetch wrapper + CRUD per resource
│   │   │   ├── lib/stores/data.js # Pinia central store
│   │   │   ├── components/     # Shared: AppDialog, Card, Badge, FormInput, Icon, Toast, etc.
│   │   │   └── views/          # Entity views (backends/, memory/, mcps/, agents/, skills/,
│   │   │                       #   clients/, commands/, flows/, conversations/)
│   │   ├── vite.config.js      # Vue + Tailwind plugin + dev proxy to :8081
│   │   └── package.json        # vue, pinia, vuedraggable, marked, tailwindcss v4
│   └── voice-ui/               # Voice UI (Vue 3 + Vite + Tailwind v4 + Pinia)
│       ├── src/
│       │   ├── main.js         # Vue app entry
│       │   ├── App.vue         # Main app shell
│       │   ├── style.css       # Tailwind v4 @theme
│       │   ├── lib/            # config, audio/, api/, i18n/, session/, settings/, stores/
│       │   └── components/     # 14 components: AgentSwitcher, CentellaOrb, ChatMessage, etc.
│       └── package.json        # vue, pinia, tailwindcss v4
├── models/                     # Source ONNX models (copied to server/ at build time)
│   ├── wakeword/               # Wake word models + wakewords.yaml
│   └── auxiliary/              # Downloaded by scripts/download-model.go
├── scripts/
│   ├── download-model.go       # Model downloader
│   └── install.sh              # One-line installer (downloads docker-compose, --gpu flag)
├── docker/
│   ├── build/Dockerfile        # Multi-stage: frontend → models → ffmpeg → onnx → go build → distroless
│   └── compose/
│       ├── docker-compose.yaml # Single self-contained file (all local services)
│       └── config.yaml         # Default config (also embedded in Docker image)
├── website/                    # Documentation site (Hugo)
│   ├── hugo.toml               # Site config + sidebar navigation
│   ├── content/docs/           # Markdown docs (getting-started, install-*, configuration, etc.)
│   └── themes/magec/           # Custom Hugo theme (layouts, css, js, shortcodes)
├── config.example.yaml
├── RELEASE_NOTES.md
├── Makefile
└── README.md
```

## HTTP Endpoints

### Main Server (port 8080) — User API

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/api/v1/agent/*` | ADK REST API (sessions, run, events) |
| POST | `/api/v1/agent/run` | Run agent (blocking) |
| POST | `/api/v1/agent/run_sse` | Run agent (SSE streaming) |
| POST | `/api/v1/webhooks/{clientId}` | Webhook endpoint — Bearer token auth |
| POST | `/api/v1/voice/{agentId}/speech` | TTS proxy (per-agent backend) |
| POST | `/api/v1/voice/{agentId}/transcription` | STT proxy (per-agent backend) |
| WebSocket | `/api/v1/voice/events` | Voice events stream (wake word + VAD) |
| GET | `/api/v1/client/info` | Client info (paired status, allowed agents with type/nested agents) |
| GET | `/api/v1/health` | Health check |
| GET | `/api/v1/swagger/` | Swagger UI |
| GET | `/` | Voice UI static files |

### Admin Server (port 8081) — Admin API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Admin UI static files |
| GET | `/api/v1/admin/overview` | Dashboard: counts + agent summaries |
| | **Backends** | CRUD: `/backends`, `/backends/{id}` |
| | **Memory** | CRUD: `/memory`, `/memory/{id}`, `/memory/types`, `/memory/{id}/health` |
| | **MCP Servers** | CRUD: `/mcps`, `/mcps/{id}` |
| | **Skills** | CRUD: `/skills`, `/skills/{id}` + references: `/skills/{id}/references`, `/skills/{id}/references/{filename}` |
| | **Agents** | CRUD: `/agents`, `/agents/{id}`, `/agents/{id}/mcps`, `/agents/{id}/mcps/{mcpId}` |
| | **Clients** | CRUD: `/clients`, `/clients/{id}`, `/clients/types`, `/clients/{id}/regenerate-token` |
| | **Commands** | CRUD: `/commands`, `/commands/{id}` |
| | **Flows** | CRUD: `/flows`, `/flows/{id}` |
| | **Conversations** | `/conversations`, `/conversations/{id}`, `/conversations/stats`, etc. |

## Configuration

**Split model**:
- **`config.yaml`** — Server infrastructure only (ports, logging, voice/ONNX). Read at startup.
- **Admin API + Store** — All resources managed via Admin UI at `:8081`. Persisted to `data/store.json`.

```yaml
server:
  host: 0.0.0.0
  port: 8080
  adminPort: 8081
  # adminPassword: ""  # Admin API auth (Bearer token)
  # encryptionKey: ""  # Encrypt secrets at rest (AES-256-GCM, independent from adminPassword)

voice:
  ui:
    enabled: true
  onnxLibraryPath: ""   # default: /usr/lib/libonnxruntime.so

log:
  level: info           # debug, info, warn, error
  format: console       # console, json
```

## Code Patterns

### Go Conventions

- **Store-based resources**: All entities managed via admin API, persisted to `data/store.json`
- **On first run**: Store starts empty. Configure via Admin UI at `:8081`
- **Multi-agent ADK**: `agent.New()` accepts agents + flows, `NewMultiLoader` routes by `appName`
- **Immutable UUID v4 IDs**: All entities use `google/uuid`. Cross-references store IDs, not names
- **Client type registry**: JSON Schema based. Each provider declares `ConfigSchema()`. Validation via `ValidateConfig()` with recursive `oneOf`/`required`/`properties`
- **Memory provider registry**: Same pattern as clients — `init()` + blank imports in `main.go`
- **Hot-reload**: Store `OnChange()` channel → `agentRouterHandler` rebuilds with 500ms debounce
- **MCP transports**: HTTP (`StreamableClientTransport` with optional headers + TLS skip) and stdio (`CommandTransport`). Stdio spawns subprocesses — works best with binary installs, not Docker
- **Migration chain** (on load): `devices→clients` → `cronJobs→triggers` → `triggers→clients` → `device→direct` → `migrateIDs`. All idempotent
- **Webhook auth**: Separate from `clientAuthMiddleware`. Webhook handler validates Bearer token against client's `cl.Token`
- **Flow execution**: `FlowDefinition` recursive tree maps 1:1 to ADK workflow agents. `responseAgent` flag on `FlowStep` filters output
- **Voice endpoints**: `/api/v1/voice/{agentId}/speech` and `/transcription` resolve backends dynamically per agent
- **MCP headers/TLS**: `MCPServer` struct has `Headers map[string]string` and `Insecure bool`. `httpClientForMCP()` creates transport with optional `InsecureSkipVerify`
- **Skill injection**: Skills are injected into the agent system prompt at build time. Instructions appended as `--- Skill: {name} ---`, reference file contents appended as `[Reference: {filename}]`. Files read from `data/skills/{skillId}/`
- **Encryption key**: `server.encryptionKey` in config.yaml. Independent from `adminPassword`. Used to encrypt secrets at rest (AES-256-GCM, PBKDF2-derived)

### Frontend Conventions (admin-ui)

- **Vue 3 Composition API**: `<script setup>` everywhere, no Options API
- **Pinia**: Single store (`data.js`) with `init()` + `refresh()`
- **No Vue Router**: Tab navigation via `activeTab` ref + `location.hash`
- **Dialog pattern**: `defineExpose({ open })`, parents call `ref.value?.open(data)`. Native `<dialog>` + `showModal()`
- **JSON Schema form renderer**: `ClientDialog.vue` renders forms dynamically from `ConfigSchema()`
- **Flow editor**: `FlowCanvas.vue` (pan/zoom/toolbar) + `FlowBlock.vue` (recursive, vuedraggable)
- **Tailwind v4**: `@tailwindcss/vite` plugin, `@theme` directive for custom colors
- **9 active tabs**: backends, memory, mcps, agents, skills, flows, commands, clients, conversations

### Frontend Conventions (voice-ui)

- **Vue 3 + Vite + Tailwind v4 + Pinia**: 14 components, single Pinia store
- **Audio pipeline**: Plain JS classes (AudioCapture, AudioRecorder, OpenAITTS, VoiceEventsClient)
- **Spokesperson**: User picks among `responseAgent`s for TTS/STT. Persisted per flow in localStorage
- **i18n**: Spanish (default) and English
- **PWA**: Installable, service worker

## Build Commands

```bash
make build              # Build frontends + models + Go binary → bin/magec-server
make dev                # Build all + start server (CONFIG=config.yaml)
make build-admin        # Build admin UI only
make build-voice        # Build voice UI only
make dev-admin          # Admin UI Vite dev server (port 5173)
make dev-voice          # Voice UI Vite dev server (port 5174)
make swagger            # Regenerate Swagger docs (admin + user)
make download-model     # Download wake word + auxiliary models
make clean              # Remove build artifacts

make docker-build       # Single-arch Docker build
make docker-buildx      # Multi-arch (amd64 + arm64)
make docker-push        # Multi-arch + push to GHCR

make infra              # Start PostgreSQL + Redis
make ollama             # Start Ollama with qwen3:8b + nomic-embed-text
make infra-stop         # Stop PostgreSQL + Redis
make infra-clean        # Remove all containers + volumes
```

## Docker Compose

Single `docker-compose.yaml` in `docker/compose/`. Self-contained with all local services:

- **magec** — Main server (:8080) + Admin UI (:8081)
- **redis** — Session storage
- **postgres** — Long-term memory (pgvector)
- **ollama** + **ollama-setup** — LLM (qwen3:8b) + embeddings (nomic-embed-text)
- **parakeet** — Speech-to-text (URL: `http://parakeet:8888`, no `/v1`)
- **tts** — Text-to-speech via openai-edge-tts (URL: `http://tts:5050`, no `/v1`, `REQUIRE_API_KEY=False`)

GPU section commented out by default. Users who want cloud providers create different backends in Admin UI.

## Dependencies

**Go backend:**
- `google.golang.org/adk` — Agent Development Kit (v0.4.0)
- `github.com/achetronic/adk-utils-go` — ADK utilities (v0.2.0): providers, session, memory tools
- `github.com/modelcontextprotocol/go-sdk` — MCP client
- `github.com/yalue/onnxruntime_go` — ONNX runtime for voice models
- `gopkg.in/yaml.v3` — YAML config parsing
- `github.com/felixge/httpsnoop` — Middleware metrics

**Frontends:**
- Vue 3, Vite 7.3, Tailwind CSS 4.1, Pinia 3
- vuedraggable (admin-ui flow editor)
- marked (admin-ui markdown rendering in conversations)

## Gotchas

1. **Both UIs use Vite**: `cd frontend/admin-ui && npx vite build` and `cd frontend/voice-ui && npx vite build`.
2. **Voice detection is server-side**: All ONNX inference (wake word + VAD) via WebSocket.
3. **Memory is optional**: Without Redis/PostgreSQL, sessions are in-memory and long-term memory is disabled.
4. **PWA over HTTP**: Requires Chrome flag for non-localhost addresses.
5. **Telegram voice**: Requires TTS backend configured. ffmpeg required in container.
6. **Parakeet/Edge TTS URLs**: Do NOT include `/v1` — Magec auto-appends it.
7. **Edge TTS auth**: Use `REQUIRE_API_KEY=False` env var, no API key needed in backend config.
8. **JSON Schema extensions**: `x-entity` (entity select), `x-format: password`, `x-placeholder`. Frontend renders dynamically.
9. **OutputKey on AgentDefinition, not FlowStep**: ADK's `OutputKey` is set on the agent.
10. **`responseAgent` is per-flow-step**: Lives on `FlowStep`. Executor resolves via `flow.ResponseAgentIDs()`. If none marked, all events returned.
11. **Cron supports shorthands**: `@daily`, `@hourly`, `@weekly`, etc. expand to 5-field expressions.
12. **Docker image includes default config.yaml**: Baked in at `/app/config.yaml`. Override with `-v`.
13. **Git branch is `master`**, not `main`. All raw GitHub URLs use `master`.
14. **Go 1.25+, Node 22+, Hugo v0.155+**.

## Testing

```bash
make infra              # Start PostgreSQL + Redis
make dev                # Build and run
# Open http://localhost:8081 → configure backends, agents, clients
# Open http://localhost:8080 → voice/text chat
```

## Related Resources

- [Google ADK](https://google.github.io/adk-docs/)
- [Model Context Protocol](https://modelcontextprotocol.io/)
- [OpenWakeWord](https://github.com/dscripka/openWakeWord)
- [pgvector](https://github.com/pgvector/pgvector)
- [Parakeet](https://github.com/achetronic/parakeet)
- [openai-edge-tts](https://github.com/travisvn/openai-edge-tts)
- [hass-mcp](https://github.com/achetronic/hass-mcp)
