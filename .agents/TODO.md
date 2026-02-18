# Magec - TODO

## High Priority

### Infinite Conversation (ContextGuard Middleware)

**Problem**: LLM context windows are finite. Long conversations silently degrade or fail when the context fills up.

**Solution**: A new `ContextGuard` middleware that monitors context usage and automatically rotates sessions when nearing the limit.

**How it works**:
1. Before each `/run`, estimate token count of session history (~4 chars ≈ 1 token), compare against model's `context_window`.
2. Below 80% → pass-through. At 80%+ → summarize current session, create new session with summary, re-send user message.
3. Return response with hidden HTML metadata: `<!--MAGEC_SESSION_CONTINUED:{...}:MAGEC_SESSION_CONTINUED-->`
4. ConversationStore links via `ParentID` (field already exists, unused).

**Context window lookup**: `server/contextwindow/` with embedded `models.json`. Source: [Charm Crush provider.json](https://github.com/charmbracelet/crush/blob/main/internal/agent/hyper/provider.json). Fallback: 128k.

**New files**: `server/contextwindow/`, `server/middleware/contextguard.go`
**Modify**: `server/main.go`, `server/clients/executor.go`

---

### TTS Real-Time Streaming Playback

**Problem**: Current TTS waits for all audio chunks before playback. Noticeable delay.

**Solution**: Incremental playback using Web Audio API — decode and schedule each chunk as it arrives.

**Modify**: `frontend/voice-ui/src/lib/audio/OpenAITTS.js`

---

### Embed admin-ui and voice-ui into Go binary

**Problem**: Release tarballs only contain the binary. The UIs are not embedded, so standalone binaries can't serve them (Docker works because Dockerfile copies `dist/`).

**Solution**: Use `//go:embed` for `admin-ui/dist/` and `voice-ui/dist/`. Replace `http.Dir(...)` with `http.FS(...)` in `main.go`. CI already runs Node.js before Go build.

**Modify**: `server/main.go`, `server/frontend/embed.go`

---

## Medium Priority

### Refactor MemoryCard to use Card component

`MemoryCard.vue` duplicates hover styles from `Card.vue`. Should wrap `<Card color="green">` instead.

**Modify**: `frontend/admin-ui/src/views/memory/MemoryCard.vue`

---

### Voice Activity Detection During TTS

On mobile, microphone picks up speaker output and triggers wake word during TTS playback. Options: mute mic during TTS, echo cancellation, or increase threshold temporarily.

---

### ~~Admin API Authentication~~ (v0.2.0 ✅)

~~Port 8081 has no auth. Anyone with network access can modify everything.~~ **Implemented**: `server.adminPassword` in config, `AdminAuth` middleware with constant-time comparison and rate limiting, login screen in Admin UI.

---

### Move `response_format` Out of Clients

TTS `response_format` (opus, mp3, wav) is hardcoded per client. Could be per-agent in `TTSRef`, per-client in config, or documented as client contract. **Decision**: TBD.

---

### Human-in-the-Loop Tool Confirmation

ADK v0.4.0 provides `toolconfirmation` — tools can request human approval. Currently blocked because all clients call `/run` synchronously. Needs SSE streaming switch, admin UI notification area, and Telegram inline keyboard.

See `.agents/ADK_TOOLS.md` for details.

---

### Evaluate Flow Subagent Invocation Model

Should clients target sub-agents within flows? Should flows support conditional routing? Should execution include per-step metadata? Should flows be composable (reference other flows)?

Design evaluation for when more complex workflows are needed.

---

### Evaluate Subagent-as-Tool Pattern

ADK supports agents as tools — orchestrator decides at runtime which specialists to call. More flexible than static flows but harder to represent in the UI. Design evaluation for when sequential/parallel model feels too rigid.

---

## Low Priority

### ~~Credential Management from Admin UI~~ (v0.2.0 ✅)

~~Credentials are in plain text in `data/store.json`.~~ **Implemented**: Secrets entity with CRUD API, AES-256-GCM encryption at rest (derived from `server.encryptionKey`, independent from `adminPassword`), env var injection before `ExpandEnv()`, Admin UI section for managing secrets.

---

### More TTS Voices Configuration UI

Voice selection is server-side only. Could add UI for preview and selection.

### Offline Mode

Cache TTS, service worker, local transcription model.

### Multi-Language Wake Words

Different models per language, auto-switch based on i18n selection.
