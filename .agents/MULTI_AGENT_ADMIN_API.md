# Multi-Agent Admin API

## Data Model

```
Store (in-memory + JSON persistence → data/store.json)
├── Backends[]          — reusable backend pools (global)
├── MemoryProviders[]   — memory providers (global, extensible)
│   ├── id, name, type, category (session | longterm)
│   ├── config: {connectionString, ...extras per type}
│   └── embedding: *BackendRef (longterm only, nil for session)
├── MCPServers[]        — reusable MCP servers (global)
│   ├── endpoint, type (http|stdio), headers, insecure
│   └── systemPrompt
├── Agents[]            — each agent is an independent unit
│   ├── id, name, description, outputKey
│   ├── systemPrompt
│   ├── llm: {backend, model}
│   ├── transcription: {backend, model}
│   ├── tts: {backend, model, voice, speed}
│   ├── memory: {session: "provider-id", longTerm: "provider-id"}
│   └── mcpServers: ["id1", "id2"]
├── Clients[]           — access points with token-based auth
│   ├── id, name, type, token, allowedAgents, enabled
│   └── config: {telegram?, cron?, webhook?}  — JSON Schema driven
├── Commands[]          — reusable prompts
│   ├── id, name, description, prompt
├── Flows[]             — multi-agent workflows
│   ├── id, name, description
│   └── root: FlowStep (recursive tree with responseAgent flag)
└── Settings            — global memory provider selection
    ├── sessionProvider, longTermProvider
```

### Resource Hierarchy

```
Backends (AI infra) → Memory (data infra) → MCPs (tools) → Agents (consumers) → Commands (prompts) → Clients (access + automation)
                                                                                                       └── Flows (workflows)
```

## API Reference

Base path: `/api/v1/admin`. All endpoints return JSON. Errors: `{"error": "message"}`.

### Backends

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/backends` | List / Create |
| GET/PUT/DELETE | `/backends/{id}` | Get / Update / Delete |

Types: `openai`, `anthropic`, `gemini`

### Memory Providers

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/memory` | List / Create |
| GET | `/memory/types` | Registered types with JSON Schema |
| GET/PUT/DELETE | `/memory/{id}` | Get / Update / Delete |
| GET | `/memory/{id}/health` | Real-time ping (5s timeout) |

### MCP Servers

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/mcps` | List / Create |
| GET/PUT/DELETE | `/mcps/{id}` | Get / Update / Delete |

Types: `http` (StreamableClientTransport), `stdio` (CommandTransport)

### Agents

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/agents` | List / Create |
| GET/PUT/DELETE | `/agents/{id}` | Get / Update / Delete |
| GET | `/agents/{id}/mcps` | List resolved MCPs |
| PUT/DELETE | `/agents/{id}/mcps/{mcpId}` | Link / Unlink MCP |

### Clients

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/clients` | List / Create (token auto-generated as `mgc_...`) |
| GET | `/clients/types` | Registered types with JSON Schema |
| GET/PUT/DELETE | `/clients/{id}` | Get / Update / Delete |
| POST | `/clients/{id}/regenerate-token` | Regenerate auth token |

Client types: `direct`, `telegram`, `cron`, `webhook`. See [CLIENT_DESIGN.md](CLIENT_DESIGN.md).

### Commands

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/commands` | List / Create |
| GET/PUT/DELETE | `/commands/{id}` | Get / Update / Delete |

### Flows

| Method | Path | Description |
|--------|------|-------------|
| GET/POST | `/flows` | List / Create |
| GET/PUT/DELETE | `/flows/{id}` | Get / Update / Delete |

### Webhook Endpoint (User API, port 8080)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/webhooks/{clientId}` | Fire a webhook — Bearer token auth, passthrough or fixed command |

## Persistence

- Store persists to `data/store.json` on each write
- If file doesn't exist at startup, store starts empty
- `config.yaml` only contains server infrastructure (ports, log, voice)

## Migration Chain (on store load)

All idempotent:
1. `devices → clients` (legacy)
2. `cronJobs → triggers` (legacy)
3. `triggers → clients` (cron/webhook types)
4. `device → direct` (type rename)
5. `migrateIDs` (UUID v4 generation)

## Future Work

- [ ] Admin API authentication
- [ ] Database persistence instead of JSON
- [ ] Multi-tenant (multiple users with their own agents)
