# Technical Decisions

Technical decisions made by the project owner. Any AI tool working on this repository
**must respect these decisions** and not revert them without explicit approval.

---

## Middlewares in their own package with httpsnoop

**Date**: 2026-02-13
**Status**: Implemented

HTTP middlewares (`AccessLog`, `CORS`, `ClientAuth`) live in `server/middleware/`,
**not** in `main.go`.

To capture status code and bytes written in the access log, we use
[`httpsnoop.CaptureMetrics`](https://github.com/felixge/httpsnoop) instead of a custom
wrapper over `http.ResponseWriter`. httpsnoop correctly handles `Hijacker`, `Flusher`,
`CloseNotifier`, `Pusher` and any other optional interface without additional code.

**Do not use**: custom `responseRecorder` or manual wrappers over `ResponseWriter`.

---

## Unified clients in server/clients/

**Date**: 2026-02-13
**Status**: Implemented

All client types live under `server/clients/`. Each subtype has its own
subdirectory with `spec.go` (JSON Schema) alongside its runtime:

```
server/clients/
├── provider.go          ← Provider interface + Schema type
├── registry.go          ← Register(), ValidateConfig(), All()
├── executor.go          ← shared logic (webhook + cron)
├── direct/
│   └── spec.go          ← Schema (no runtime)
├── telegram/
│   ├── spec.go
│   └── bot.go
├── webhook/
│   ├── spec.go
│   └── handler.go
└── cron/
    ├── spec.go
    ├── cron.go
    └── scheduler.go
```

The `server/client/` (singular) package was absorbed. The previous separation
(schemas in `client/`, runtime in `clients/`) was not consistent.
The `server/trigger/` package was removed. Webhook and cron are clients just like
Telegram — the previous separation was not consistent with the domain.

---

## JSON Schema validation with google/jsonschema-go

**Date**: 2026-02-13
**Status**: Implemented

Client type config validation uses
[`google/jsonschema-go`](https://github.com/google/jsonschema-go) instead of manual
logic. The library:

- Is from Google, no external dependencies (stdlib only)
- Supports draft-07 and 2020-12 fully (`oneOf`, `const`, `required`, `enum`,
  `pattern`, `minLength`, `if/then/else`...)
- Validates directly on `map[string]any`
- Includes `ApplyDefaults` to populate default values

**Do not use**: manual validators for `required`/`oneOf` or helpers like `matchOneOf`
or `jsonEqual`. Always delegate to the library.

---

## Voice configuration as an independent block

**Date**: 2026-02-14
**Status**: Implemented

Voice-related configuration (UI, ONNX runtime) lives in its own `voice` block
in the YAML, **not** inside `server`. ONNX Runtime is used for voice models of
different types (wake word, VAD, embeddings), so it belongs to the voice domain,
not the HTTP infrastructure domain.

```yaml
voice:
  ui:
    enabled: true          # Enable/disable Voice UI, routes and static files
  onnxLibraryPath: ""      # Path to libonnxruntime.so (default: /usr/lib/libonnxruntime.so)
```

The Go struct uses sub-structs: `Config.Voice.UI.Enabled` (*bool, default true)
and `Config.Voice.OnnxLibraryPath` (string).

**Do not put**: voice fields inside `Server` — that block is for network/ports only.

---

## Documentation website (Hugo)

**Date**: 2026-02-14
**Status**: Implemented

Project documentation lives in `website/` as a Hugo static site, deployed to
GitHub Pages. Uses a custom `magec` theme with the project palette: piedra,
atlántico, lava, sol, arena. Dark mode only.

```
website/
├── hugo.toml               ← Site config + sidebar navigation
├── content/docs/           ← Markdown docs (getting-started, install-*, configuration, etc.)
└── themes/magec/           ← Custom theme (layouts, css, js, shortcodes)
```

Build: `cd website && hugo`. Dev: `cd website && hugo server`.

The README.md is simplified — highlights and quick start, pointing to the website
for detailed documentation.

---

## Admin UI never accesses the User API

**Date**: 2026-02-14
**Status**: Implemented

The Admin UI (port 8081) **must never** access the User API (port 8080) to
perform operations. All logic must go directly through internal access
(Go structs, services, stores).

Example: to delete an ADK session, the admin handler calls
`sessionService.Delete()` directly — it does not make HTTP calls to port 8080. To list conversations,
it reads from `ConversationStore` — it does not call REST endpoints.

**Reason**: The admin is an internal component with privileged access. It must not
depend on client authentication (`clientAuthMiddleware`) or the User API's
availability. If the User API is down or misconfigured,
the admin must continue working.

**Do not**: `http.Get("http://127.0.0.1:8080/api/v1/...")` from the admin handler.
Always pass direct references to internal services (session, memory, store).

---

## Centralized memory in the launcher

**Date**: 2026-02-14
**Status**: Implemented

Session and long-term memory configuration is **global**, not per agent.
The ADK launcher accepts a single `session.Service` and a single `memory.Service`,
so configuring memory individually per agent is an illusion — in practice
they all use the same one.

Global config lives in `StoreData.Settings`:

```go
type Settings struct {
    SessionProvider  string `json:"sessionProvider,omitempty"`
    LongTermProvider string `json:"longTermProvider,omitempty"`
}
```

The `AgentDefinition.Memory.Session` and `AgentDefinition.Memory.LongTerm`
fields are kept in the struct for backwards compatibility but are ignored. The UI no
longer shows them in the agent form.

**Do not**: Configure session/longterm memory at the individual agent level.
If ADK improves the launcher to support multiple session services in the future,
it can be decentralized.

---

## Voice config is an agent capability, not a flow property

**Date**: 2026-02-15
**Status**: Implemented

TTS and STT are configured **per agent** (`AgentDefinition.TTS`, `AgentDefinition.Transcription`).
Flows do not and should not have their own voice configuration. A flow orchestrates agents,
and each agent has its own "voice" (TTS) and "ear" (STT), like a person.

Analogy: in a meeting, everyone speaks and understands. The flow decides who participates and in
what order. What voice each one has is intrinsic to the agent, not the flow.

**Do not**: Add voice fields to `FlowDefinition` or `FlowStep`. Do not create a
`voiceAgent` boolean in steps. If an agent participates in a flow and needs voice,
configure it on the agent.

---

## Spokesperson is a consumer (voice-ui) decision, not an admin one

**Date**: 2026-02-15
**Status**: Implemented

When the voice-ui works with a flow, multiple agents can be marked as
`responseAgent` (they speak publicly). The voice-ui needs to know which agent to send
audio to for STT and which agent's voice to use for TTS. This choice is called
**spokesperson** and is a **user preference** of the voice-ui, not an admin
configuration.

Layers of responsibility:

| Layer | Responsibility | Where |
|-------|---------------|-------|
| Voice capability | TTS/STT config per agent | Admin (AgentDefinition) |
| Who responds publicly | `responseAgent` per flow step | Admin (FlowStep) |
| Who is spokesperson | Selector among responseAgents | Voice-ui (user chooses) |

The spokesperson is persisted in localStorage by flow ID (`SettingsManager`). If there is no
saved spokesperson, the first `responseAgent` of the flow is used as fallback. If there
is no `responseAgent` marked, the first agent of the flow is used.

**Do not**: Put spokesperson selection in the admin UI. Do not add
`voiceAgent` or `spokesperson` fields to the server data model.

---

## /client/info exposes type and internal agents of flows

**Date**: 2026-02-15
**Status**: Implemented

The `GET /api/v1/client/info` endpoint returns `allowedAgents` with enriched
information so that clients (voice-ui, future ones) can distinguish agents
from flows and know the internal composition:

```json
{
  "allowedAgents": [
    { "id": "...", "name": "Magec", "type": "agent" },
    {
      "id": "...", "name": "Software Factory", "type": "flow",
      "agents": [
        { "id": "...", "name": "Architect", "type": "agent", "responseAgent": true },
        { "id": "...", "name": "Developer", "type": "agent", "responseAgent": true },
        { "id": "...", "name": "Planner", "type": "agent" }
      ]
    }
  ]
}
```

`AgentSummary` fields:
- `type`: `"agent"` or `"flow"` — previously indistinguishable
- `agents`: only in flows, list of unique agents from the tree (via `FlowDefinition.AgentIDs()`)
- `responseAgent`: only in nested agents of a flow, indicates if they are marked as `responseAgent` in some step

**Do not**: Expose TTS/STT config in this endpoint. The voice-ui does not need
to know the technical details — it only needs the agent ID to pass it to the
voice endpoints (`/voice/{agentId}/speech`, `/voice/{agentId}/transcription`).

---

## Voice errors as notifications, not blockers

**Date**: 2026-02-15
**Status**: Implemented

When a spokesperson does not have TTS or STT configured, the voice-ui shows a
friendly notification instead of failing silently or blocking the UI:

- STT fails → "It seems the agent can't understand you. Check that it has transcription configured."
- TTS fails → "It seems the agent can't speak. Check that it has text-to-speech configured."

**Do not**: Filter spokespersons by whether they have voice configured (couples the endpoint
to the concept of voice). Do not block the selection — the user can choose any
responseAgent and receive feedback if it doesn't work.

---

## Admin password authentication (v0.2.0)

**Date**: 2026-02-14
**Status**: Implemented

Admin API (port 8081) is protected by a password configured in `server.adminPassword`.
Authentication uses `Authorization: Bearer <password>` header with constant-time
comparison (`crypto/subtle.ConstantTimeCompare`) and per-IP rate limiting (5 attempts/minute).

If no password is set, the admin remains open (backwards compatible) with a warning log.

The middleware bypasses auth for `OPTIONS` preflight and static files (non-`/api/` paths).
A dedicated `/api/v1/admin/auth/check` endpoint allows the UI to verify credentials
without hitting a real resource.

The Admin UI shows a login screen when auth is required. The password is stored in
memory only (not localStorage) — closing the tab requires re-authentication.

**Do not**: Use cookies or sessions. Do not store the password in localStorage.
Do not use `X-Admin-Password` custom header — use standard `Authorization: Bearer`.

---

## Secrets with env var injection and encryption at rest (v0.2.0)

**Date**: 2026-02-14
**Status**: Implemented

Secrets are key-value pairs stored in `StoreData.Secrets`. Each secret has:
`{id, name, key, value, description}` where `key` is the env var name (e.g. `OPENAI_API_KEY`).

**Injection flow**: On store load, secrets are extracted first (raw unmarshal), decrypted
if encrypted, injected via `os.Setenv()`, then the full store is expanded via `os.ExpandEnv()`.
This allows `${OPENAI_API_KEY}` in any store field (backend URLs, API keys, bot tokens).

**Encryption**: When `adminPassword` is configured, secret values are encrypted with
AES-256-GCM, key derived via PBKDF2 (100k iterations, SHA-256). Stored as `enc:v1:<base64>`.
Without admin password, secrets are stored in cleartext with a warning log.

**API**: Secrets CRUD at `/api/v1/admin/secrets`. GET responses never include the `value`
field — values are write-only from the API perspective. Updates with empty `value` preserve
the existing value.

**Recovery**: If admin password is lost, encrypted secrets are unrecoverable. Delete them
and recreate. Non-secret entities remain intact.

**Do not**: Return secret values in GET responses. Do not store secrets in config.yaml.
Do not use a separate encryption key — derive from admin password.
