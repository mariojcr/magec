# Magec - TODO

## ~~Large Message Handling in Telegram and Slack~~ ✅

Implemented. See `server/clients/msgutil/` package.

---

## High Priority

### Multimodal File/Image Support in Clients

**Problem**: Telegram, Slack, and Discord clients only handle text and voice messages. Users sending images, documents, PDFs, or other files get silently ignored.

**Solution**: Download files from Telegram/Slack, encode as base64, and send as `inlineData` parts alongside text in the ADK `/run` request. The ADK already supports `genai.Part{InlineData: &Blob{Data, MIMEType}}` — zero backend changes needed.

**Adapter support (adk-utils-go v0.3.1)**:
- **Gemini**: passes all `InlineData` transparently to the API. Unsupported types are rejected by Google's API.
- **OpenAI**: translates images (JPEG, PNG, GIF, WebP), audio (WAV, MP3, MPEG, WebM), and files (PDF, text/*). Unsupported types return an error.
- **Anthropic**: translates images (JPEG, PNG, GIF, WebP), PDFs, and text documents (text/*). Unsupported types return an error.
- All three adapters behave the same: if a MIME type can't be translated, the request fails. No silent drops.

**File size limits**: 5MB per file, 10MB total per message, max 10 files per message. Validated client-side before download.

**Supported types (denominator común)**: JPEG, PNG, GIF, WebP. PDF and text/* work on Gemini + Anthropic. Audio works on Gemini + OpenAI.

**Telegram** (`server/clients/telegram/bot.go`):
- Current state: only `Voice` (dedicated handler) and `Text` (predicate at ~line 171 requires `Text != ""` and `Voice == nil`). Everything else is silently dropped.
- Add handler for `Document`, `Photo`, `Video`, `Audio`, `Animation`, `VideoNote`, `Sticker`. All have `FileID` → `bot.GetFile()` → download bytes.
- `Photo` is `[]PhotoSize` — use last element (highest resolution) for its `FileID`.
- `Caption` field accompanies media — include as `{"text": caption}` part alongside `{"inlineData": {...}}`.
- `callAgent()` (~line 803): change `"parts": []map[string]string{{"text": ...}}` to `[]interface{}` to support both text and inlineData parts.

**Slack** (`server/clients/slack/bot.go`):
- Current state: `handleAudioClip()` (~line 213) processes `ev.Message.Files` but only `audio/*` mimetype. Other mimetypes silently skipped.
- Add handling for `image/*`, `application/pdf`, and generic fallback for other types.
- Files are on `ev.Message.Files` (type `[]slack.File`) with `Mimetype`, `Size`, `URLPrivateDownload`/`URLPrivate`.
- Reuse existing `downloadSlackFile()` (~line 658) for all file types.
- `processMessage()` (~line 477): same parts change as Telegram.
- For `handleAppMention`: verify if files arrive via `AppMentionEvent` or need separate handling.

**ADK payload format**:
```json
{
  "parts": [
    {"text": "<!--MAGEC_META:...-->\nCaption or question about the file"},
    {"inlineData": {"mimeType": "image/png", "data": "<base64>"}}
  ]
}
```

**File size validation**: 5MB per file, 10MB total per message, max 10 files. Reject oversized files with user-friendly message.

**LLM limitations**: GPT-4o/Claude/Gemini handle images and PDFs natively. For Word/Excel/CSV, the model may not support them — the user gets a natural "I can't process this format" response from the LLM itself.

**A2A (future, non-blocking)**: `server/a2a/handler.go` declares `DefaultInputModes: []string{"text/plain"}`. When A2A file support is needed, add `"image/*"`, `"application/pdf"`, etc. The A2A handler converts `FilePart` → `genai.Part{InlineData}` before passing to ADK.

**Discord** (`server/clients/discord/bot.go`):
- Same approach as Telegram/Slack: detect non-audio attachments via `m.Attachments`, download via `att.URL`, encode base64, send as `inlineData` parts.
- Check `att.Size` < 5MB before downloading.
- `m.Content` alongside attachments becomes the caption/text part.

**Modify**: `server/clients/telegram/bot.go`, `server/clients/slack/bot.go`, `server/clients/discord/bot.go`
**No changes needed**: `server/agent/agent.go`, `server/api/user/handlers.go`, ADK library

---

### Improve Drag-and-Drop UX in Visual Flow Editor

The visual flow editor's drag-and-drop experience needs polish. Improve feedback, snapping, reordering smoothness, and overall usability when building flows visually.

**Modify**: `frontend/admin-ui/` (flow editor components)

---

### Line Breaks in Voice UI Text Chat

**Problem**: The text input in the Voice UI doesn't support multi-line messages. Pressing Enter sends the message immediately with no way to insert a line break.

**Solution**: Support Shift+Enter (or similar) for inserting line breaks. Switch input from `<input>` to `<textarea>` (or equivalent) with auto-resize behavior. Enter sends, Shift+Enter adds newline.

**Modify**: `frontend/voice-ui/`

---

### Tool Execution Visibility in Clients

**Problem**: When the agent executes tools during a conversation, the user has no visibility into what's happening behind the scenes. This creates a black-box experience that erodes trust.

**Solution**: Show tool calls and their results as collapsible/summarized blocks so users can see what the agent did without cluttering the main conversation flow. Each client adapts to its platform's formatting capabilities.

**Platform collapsible support**:

| Client | Collapsible nativo | Mecanismo |
|--------|-------------------|-----------|
| **Telegram** | **Yes** | `<blockquote expandable>...</blockquote>` (HTML parse mode) — collapsed by default, user taps to expand |
| **Slack** | **No** | No collapsible blocks in mrkdwn or Block Kit. Show a short summary line like `🔧 Tool: search_memory (completed)` without full details |
| **Voice UI** | **Yes** | Custom Vue component — `<details>/<summary>` or click/tap collapsible block |

**Implementation per client**:

**Telegram** (`server/clients/telegram/bot.go`):
- Switch from `Markdown` parse mode to `HTML` parse mode in `sendResponse()`
- Extract tool call events from ADK response (already available as `functionCall`/`functionResponse` parts in the events array)
- Before the main text response, send tool execution info wrapped in `<blockquote expandable>🔧 tool_name\n\nInput: ...\nOutput: ...</blockquote>`
- Collapsed by default — user taps to see full tool details
- If multiple tools were called, group them in a single expandable blockquote or send one per tool

**Slack** (`server/clients/slack/bot.go`):
- No native collapsible support — use a compact summary format
- Before or above the main response text, add a line per tool: `🔧 *tool_name* — completed` (mrkdwn bold)
- Optionally use a Slack `context` block (smaller, muted text) for tool summaries if switching to Block Kit messaging
- Full tool input/output not shown (Slack has no way to hide it behind a toggle)

**Voice UI** (`frontend/voice-ui/src/components/ChatMessage.vue`):
- Add a new message type or section for tool calls in the chat timeline
- Render as a collapsible block: header shows `🔧 tool_name`, body (hidden by default) shows input args and output
- Style: muted colors (`bg-piedra-800`, `text-arena-500`), click to expand/collapse
- Tool events are already present in the ADK `/run` response as `functionCall` and `functionResponse` parts — extract them in `AgentClient.js` `_extractResponses()`

**ADK response events structure** (tool calls are already in the response):
```json
[
  {
    "author": "agent_name",
    "content": {
      "parts": [
        {"functionCall": {"name": "search_memory", "args": {"query": "..."}}}
      ]
    }
  },
  {
    "author": "agent_name",
    "content": {
      "parts": [
        {"functionResponse": {"name": "search_memory", "response": {"result": "..."}}}
      ]
    }
  },
  {
    "author": "agent_name",
    "content": {
      "parts": [
        {"text": "Here is the final answer..."}
      ]
    }
  }
]
```

**Key decisions**:
- Tool visibility is **per-client** — each client renders what its platform allows
- Telegram and Voice UI get full collapsible details; Slack gets a compact summary
- Tool info is sent **alongside** the response, not as a separate message (except Telegram where it may be a preceding message with expandable blockquote)
- No server changes needed — tool events are already in the ADK `/run` response; clients just need to extract and render them

**Discord** (`server/clients/discord/bot.go`):
- Same as Telegram: use `<blockquote expandable>` equivalent if Discord supports it, otherwise compact summary like Slack.
- Discord supports markdown but no native collapsible blocks — use a compact summary line per tool: `🔧 **tool_name** — completed`.

**Modify**: `server/clients/telegram/bot.go`, `server/clients/slack/bot.go`, `server/clients/discord/bot.go`, `frontend/voice-ui/src/components/ChatMessage.vue`, `frontend/voice-ui/src/lib/api/AgentClient.js`

---

### File Upload Support in Voice UI Text Chat

**Problem**: Users can only send text and voice from the Voice UI. There's no way to attach files (images, PDFs, etc.) to a message from the web chat.

**Solution**: Add a file attachment button to the text input area. Upload files, encode as base64, and send as `inlineData` parts alongside text in the `/run` request (same format as the Telegram/Slack multimodal support). Show file previews/thumbnails in the chat.

**Modify**: `frontend/voice-ui/`, `server/api/user/handlers.go` (if multipart upload needed)

---

### Human-in-the-Loop Tool Confirmation

**Problem**: MCP tools can perform sensitive actions (delete data, send emails, execute code). There's no way to require human approval before execution.

**Solution**: Use ADK v0.4.0 `toolconfirmation` protocol. Wrap selected tools with `ctx.RequestConfirmation()` so they pause and ask the user before executing.

**Design decisions**:
- **Confirmation list lives on the agent, not on the MCP server**. A tool may be dangerous for a public-facing agent but fine for an internal one. The MCP is a shared resource — marking tools there would force the same policy on all agents.
- **Agent config**: new field `toolConfirmation: ["delete_record", "send_email", "execute_*"]` — list of tool names/globs that require confirmation for this agent.
- **Wrapper in `buildToolsets()`**: after loading MCP tools, wrap those matching `toolConfirmation` patterns with a confirmation layer that calls `ctx.RequestConfirmation(hint, payload)` before delegating to the real tool.
- **Admin UI**: agent form gains a "Tools requiring confirmation" section — multi-select or free-text with glob support.

**Client changes (all must migrate from `/run` to `/run_sse`)**:
- The server already serves `/run_sse` via `adkrest.NewHandler`, and middleware (recorder, flow filter) already supports SSE.
- **Telegram**: listen for `adk_request_confirmation` SSE events, show inline keyboard (Approve/Reject), send `FunctionResponse` back.
- **Slack**: show interactive block with buttons, handle callback.
- **Voice UI**: show collapsible confirmation card in chat timeline with Approve/Reject buttons.
- **Executor** (`server/clients/executor.go`): auto-approve or skip (cron/webhook triggers can't wait for a human).

**Protocol flow**:
1. Tool's `Run()` calls `ctx.RequestConfirmation(hint, payload)` → returns nil, pausing execution
2. ADK emits `FunctionCall` event with name `adk_request_confirmation` via SSE
3. Client shows confirmation prompt to user (tool name, hint, args)
4. User approves/rejects → client sends `FunctionResponse` with `{confirmed: true/false}`
5. Tool reads `ctx.ToolConfirmation().Confirmed` and proceeds or aborts

See `.agents/ADK_TOOLS.md` for protocol details.

**Modify**: `server/agent/agent.go` (wrapper in `buildToolsets`), `server/store/types.go` (agent config field), `server/clients/telegram/bot.go`, `server/clients/slack/bot.go`, `server/clients/executor.go`, `frontend/voice-ui/`, `frontend/admin-ui/`

---

### ~~Artifact Management Toolset~~ ✅

Implemented. See `server/agent/tools/artifacts/toolset.go` — provides `save_artifact`, `load_artifact`, and `list_artifacts` tools via `functiontool.New`. Supports text and base64 binary content. Wired into `base_toolset.go` so all agents get it. Filesystem-backed via `adk-utils-go/artifact/filesystem` (persists across restarts). Clients (Telegram, Slack, and Discord) auto-deliver new artifacts as file attachments after each `/run` response using before/after diff of the artifact list REST endpoint.

---

### TTS Real-Time Streaming Playback

**Problem**: Current TTS waits for all audio chunks before playback. Noticeable delay.

**Solution**: Incremental playback using Web Audio API — decode and schedule each chunk as it arrives.

**Modify**: `frontend/voice-ui/src/lib/audio/OpenAITTS.js`

---

## Medium Priority

### Composable Flows (flow-as-step)

**Problem**: Flows can only reference agents in their steps. To build complex pipelines (e.g. a content pipeline that includes a review sub-pipeline), users have to flatten everything into a single flow, which becomes unwieldy.

**Solution**: Allow a flow step to reference another flow ID, not just an agent ID. Since flows already compile to ADK agents (`SequentialAgent`, `ParallelAgent`, `LoopAgent`) and register in `adkAgentMap`, a step pointing to a flow ID resolves to the sub-flow's compiled agent.

**Key design decisions**:

- **Compilation order**: Build flows in topological order (leaf flows first). Flows with no flow-dependencies compile first, then flows that reference them.
- **Cycle detection**: Reject flow A → flow B → flow A at save time (admin API validation) and at compile time (`BuildFlowAgent`).
- **responseAgent inheritance**: A step pointing to a sub-flow has a toggle: "inherit responseAgents" (default) or "silence" (step produces no public output). No partial override — inherit all or none. To change which agents are responseAgents, edit the sub-flow directly.
- **Output key**: Sub-flow's output key passes through as the step's output, same as a regular agent step.
- **UI**: Flow step agent selector shows both agents and flows (distinguished by `type` field). Cycle detection prevents selecting a flow that would create a circular dependency.

**Implementation**:

1. `BuildFlowAgent` (`server/agent/flow.go`): when resolving a step's agent ID, look in `adkAgentMap` which already contains both agents and compiled flows
2. Add topological sort in `agent.go` `New()` before the flow compilation loop (~line 181)
3. Add cycle detection: build dependency graph from flow steps, reject if cycle found
4. `FlowStep` gains `InheritResponseAgents *bool` (default true). When false, the step's sub-flow responseAgents are excluded from the parent flow's `ResponseAgentIDs()`
5. Admin API: validate no cycles on flow create/update
6. Admin UI: flow step selector includes flows, visual indicator for flow vs agent

**Modify**: `server/agent/flow.go`, `server/agent/agent.go`, `server/store/types.go`, `server/api/admin/flows.go`, `frontend/admin-ui/`

---

### Refactor MemoryCard to use Card component

`MemoryCard.vue` duplicates hover styles from `Card.vue`. Should wrap `<Card color="green">` instead.

**Modify**: `frontend/admin-ui/src/views/memory/MemoryCard.vue`

---

### Voice Activity Detection During TTS

On mobile, microphone picks up speaker output and triggers wake word during TTS playback. Options: mute mic during TTS, echo cancellation, or increase threshold temporarily.

---

### Move `response_format` Out of Clients

TTS `response_format` (opus, mp3, wav) is hardcoded per client. Could be per-agent in `TTSRef`, per-client in config, or documented as client contract. **Decision**: TBD.

---

### Remote A2A Agents as Tools (orchestration mode)

**Problem**: A user may have multiple A2A agents deployed across their network (e.g. researcher, architect, code reviewer). They want a local "header" agent (MetaMagecAgent) that can call those remote agents when it decides, consolidate their responses, and deliver a unified answer to the user.

**Solution**: Use ADK's `agent/remoteagent` + `tool/agenttool` to wrap each remote A2A agent as a tool callable by the orchestrator's LLM. The orchestrator maintains full control — it decides which remotes to call, can call multiple, and consolidates before responding.

**How it works**:
```
User → MetaMagecAgent (LLM + remote agent tools)
           ├── ask_architect("design this system") → A2A call → response
           ├── ask_researcher("find prior art")    → A2A call → response
           └── LLM consolidates both → responds to user
```

**ADK native support** (already available in v0.4.0):
```go
import (
    "google.golang.org/adk/agent/remoteagent"
    "google.golang.org/adk/tool/agenttool"
)

remote, _ := remoteagent.NewA2A(remoteagent.A2AConfig{
    Name:            "architect",
    AgentCardSource: "http://architect-agent:8080",
})
architectTool := agenttool.New(remote, nil)
```

**What to implement in magec**:
1. New entity `RemoteAgent` in the store: `{id, name, agentCardURL, credentials}`
2. In `buildToolsets()` (`server/agent/agent.go`): for each remote agent configured on the agent, create `remoteagent.NewA2A()` + `agenttool.New()` and add to toolsets
3. Agent config: new field `remoteAgents []string` (list of RemoteAgent IDs), similar to how `mcpServers` works
4. Admin UI: section for managing remote agents (CRUD), agent form gains a "Remote Agents" multi-select like MCPs
5. System prompt guidance: the orchestrator agent's prompt should describe what each remote agent does so the LLM knows when to use them

**Characteristics**:
- Orchestrator always keeps control
- Can call multiple remotes per turn
- Can compare, filter, reformulate remote responses
- User always talks to the orchestrator, never directly to remotes
- Works as a flow step — the flow doesn't know or care that it uses remote agents internally

**Modify**: `server/agent/agent.go`, `server/store/types.go`, `server/api/admin/`, `frontend/admin-ui/`

---

### Remote A2A Agents as Sub-agents (transfer mode)

**Problem**: In some cases, a remote A2A agent needs to interact directly with the user — ask clarifying questions, iterate on a solution, have a multi-turn conversation — without the orchestrator in the middle adding latency and losing context.

**Solution**: Use ADK's `agent/remoteagent` to create the remote as a proper sub-agent. The orchestrator's LLM can "transfer" the conversation to the remote agent. The remote then talks directly with the user until it's done, then control returns to the orchestrator.

**How it works**:
```
User → MetaMagecAgent: "I need a system architecture"
  MetaMagecAgent → transfer to architect
    User ↔ Architect (direct multi-turn conversation)
    Architect: "done, here's the design"
  ← control returns to MetaMagecAgent
MetaMagecAgent → continues with next step
```

**ADK native support**:
```go
remote, _ := remoteagent.NewA2A(remoteagent.A2AConfig{
    Name:            "architect",
    AgentCardSource: "http://architect-agent:8080",
})
// Pass as sub-agent directly, no agenttool wrapper
orchestrator, _ := llmagent.New(llmagent.Config{
    SubAgents: []agent.Agent{remote},
    // ...
})
```

**Characteristics**:
- Remote gets full conversation context and direct user interaction
- No orchestrator latency/tokens in the middle during the transfer
- Remote can use all its own tools and personality
- Orchestrator loses visibility during the transfer
- One transfer at a time (can't talk to two remotes simultaneously)
- Better for deep specialization tasks that need multi-turn interaction

**When to use which**:

| Scenario | Use |
|---|---|
| "Ask the researcher for X and the architect for Y, then combine" | Tool mode |
| "Hand this off to the architect, let them work it out with the user" | Transfer mode |
| Quick factual queries to remotes | Tool mode |
| Complex tasks needing clarification/iteration | Transfer mode |

**Implementation**: Same `RemoteAgent` entity as tool mode. The agent config would specify per-remote whether it's a tool or sub-agent (or both — ADK allows it). Can be implemented after tool mode as an incremental addition.

**Modify**: Same files as tool mode, plus sub-agent wiring in `agent.go`

---

### Evaluate Flow Subagent Invocation Model

Should clients target sub-agents within flows? Should flows support conditional routing? Should execution include per-step metadata? Should flows be composable (reference other flows)?

Design evaluation for when more complex workflows are needed.

---

### Evaluate Subagent-as-Tool Pattern

ADK supports agents as tools — orchestrator decides at runtime which specialists to call. More flexible than static flows but harder to represent in the UI. Design evaluation for when sequential/parallel model feels too rigid.

---

### ContextGuard Summary Tier Migration (app/user scope)

**Problem**: ContextGuard summaries are currently session-scoped. When a user switches client (Discord → Telegram) or starts a new thread, the agent loses all conversation context from previous sessions. The summary dies with the session.

**Blocked by**: All users are currently `default_user`. Moving summaries to `app:` tier would share them across **all** clients/channels for that agent — if a user asks about Kubernetes deployments on Discord and networking on Telegram, both contexts contaminate each other's summary.

**Solution (requires real user identity)**:
1. Implement per-client user identity: each client generates a meaningful `userID` (e.g. `discord_123456`, `slack_U0ABC`, `telegram_98765`) instead of `default_user`
2. Move ContextGuard state keys to `user:` tier (`session.KeyPrefixUser` prefix) so summaries are scoped per-user across all that user's sessions with a given agent
3. The `user:` tier in `adk-utils-go` v0.5.0 already supports differentiated TTL (defaults to no expiration), so summaries survive indefinitely

**What's already in place**:
- `adk-utils-go` v0.5.0 has full tier support (`app:`, `user:`, `temp:`) with independent TTLs for app/user state (default: no expiration, matching canonical ADK DatabaseService behaviour)
- ContextGuard state keys are simple string constants in `server/plugin/contextguard/contextguard.go` — adding the prefix is a one-line change per key
- The Redis session service stores `user:` state in a dedicated HASH (`userstate:{appName}:{userID}`) separate from session data

**Cross-client identity (future)**:
If a single person uses Discord AND Telegram, they'd have two `userID`s and two separate summaries — which is actually correct (different conversational contexts). True cross-client identity (linking `discord_123` and `telegram_456` as the same person) is a separate, larger problem.

**Modify**: `server/plugin/contextguard/contextguard.go`, `server/clients/telegram/bot.go`, `server/clients/slack/bot.go`, `server/clients/discord/bot.go`, `server/clients/executor.go`

---

## Low Priority

### More TTS Voices Configuration UI

Voice selection is server-side only. Could add UI for preview and selection.

### Offline Mode

Cache TTS, service worker, local transcription model.

### Multi-Language Wake Words

Different models per language, auto-switch based on i18n selection.
