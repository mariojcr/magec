---
title: "Slack"
---

Magec can connect to Slack through a bot using Socket Mode. Users send text or audio clip messages to the bot in DMs or @mention it in channels, and the bot responds using your configured agents. It supports multiple response modes, per-channel agent switching, and voice messages via Slack's audio clips. No public URL or webhooks needed — the bot connects outward via WebSocket.

This is ideal for teams already on Slack who want to interact with their agents without leaving their workspace.

## Setup

### 1. Create a Slack App

Go to [api.slack.com/apps](https://api.slack.com/apps) and create a new app:

1. Click **Create New App** → **From scratch**
2. Name it (e.g., "Magec Assistant") and select your workspace
3. Under **Socket Mode**, enable it — Slack generates an **App Token** (`xapp-...`). Copy it.
4. Under **OAuth & Permissions**, add these Bot Token Scopes:
   - `app_mentions:read` — receive @mentions in channels
   - `chat:write` — send messages
   - `files:read` — access audio clips and file attachments
   - `files:write` — upload voice response files
   - `im:history` — read DM messages
   - `im:read` — access DM conversations
   - `im:write` — send DMs
   - `users:read` — look up user names and display names
   - `users:read.email` — access user email addresses
5. Under **Event Subscriptions**, enable events and subscribe to:
   - `app_mention` — triggers when someone @mentions the bot
   - `message.im` — triggers on direct messages to the bot
6. Under **App Home**:
   - Enable **"Allow users to send Slash commands and messages from the messages tab"** — this lets users DM the bot
   - Enable **"Show My Bot as Online"** — shows the bot with a green presence dot when the Magec server is running
7. Install the app to your workspace — copy the **Bot Token** (`xoxb-...`)

{{< callout >}}
**Changes not taking effect?** If you modify App Home settings (DMs, presence) after the initial install, you may need to reinstall the app under **Install App** → **Reinstall to Workspace**. Some users also need to restart their Slack client for the changes to appear.
{{< /callout >}}

### 2. Get your user ID

In Slack, click on your profile → **Copy member ID**. You'll need this if you want to restrict who can use the bot.

### 3. Create a Slack client in Magec

In the Admin UI, go to **Clients** → **New** → **Slack**:

| Field | Description |
|-------|-------------|
| `name` | Display name for this client |
| `botToken` | The Bot Token from OAuth & Permissions (`xoxb-...`) |
| `appToken` | The App Token from Socket Mode settings (`xapp-...`) |
| `allowedUsers` | Slack user IDs that can use this bot (empty = everyone) |
| `allowedChannels` | Slack channel IDs where the bot can respond (empty = everywhere) |
| `responseMode` | How the bot responds — see below |
| `allowedAgents` | Which agents and flows this bot can access |

### 4. Start chatting

DM the bot directly or @mention it in a channel. The bot responds using the first allowed agent.

## How it works

Magec uses Slack's **Socket Mode** — the bot opens a WebSocket connection to Slack's servers. This means:

- **No public URL needed** — works behind firewalls, NATs, on your local machine
- **No webhook configuration** — no need to expose ports or set up SSL certificates
- **Outbound only** — the bot connects outward, nothing connects inward

This matches Magec's self-hosted philosophy: everything runs on your infrastructure with no external dependencies.

## Interaction modes

The bot responds to messages in two contexts:

| Context | How it works |
|---------|-------------|
| **Direct Messages** | Send any message (text or audio clip) to the bot. It responds directly in the DM. Bot commands (`!help`, `!agent`, `!responsemode`) are available here. |
| **Channel mentions** | @mention the bot in a channel. It responds in a thread under your message. |

## Response modes

The response mode controls the format of the bot's replies:

| Mode | Behavior |
|------|----------|
| `text` | Always respond with text (default). Simple and reliable. |
| `voice` | Always respond with a voice file. Requires the agent to have TTS configured. |
| `mirror` | Mirror the user's format — text replies to text, voice replies to audio clips. |
| `both` | Respond with both a text message and a voice file. |

Users can change the response mode at runtime with the `!responsemode` command.

## Voice messages

Slack audio clips (voice messages) work in both directions:

**Incoming voice:** When a user sends an audio clip in a DM, Magec:
1. Downloads the audio file from Slack (M4A format)
2. Converts it from M4A to WAV using ffmpeg
3. Sends it to the agent's STT backend for transcription
4. The agent processes the transcribed text and responds

**Outgoing voice:** When the response mode requires voice (`voice`, `mirror`, `both`), Magec:
1. Sends the agent's text response to the TTS backend
2. Gets the audio back (Opus format)
3. Uploads it as a file in the Slack conversation

{{< callout >}}
**Docker users:** Voice processing requires ffmpeg inside the container. The official Magec Docker image includes it. If using a custom image, ensure ffmpeg is available.
{{< /callout >}}

## Bot commands

Bot commands use an `!` prefix (IRC-style) and work in DMs only (not in channel mentions):

| Command | Description |
|---------|-------------|
| `!help` | List available commands |
| `!agent` | Show the active agent and list all available agents |
| `!agent <id>` | Switch to a specific agent |
| `!responsemode` | Show the current response mode |
| `!responsemode <mode>` | Change the response mode (`text`, `voice`, `mirror`, `both`, `reset`) |

The `!agent` command shows all agents and flows the bot has access to. Select one and all subsequent messages in that conversation go to the selected agent. Different channels/DMs can use different agents simultaneously.

The `!responsemode` command lets users override the default response mode configured in the Admin UI. Using `!responsemode reset` reverts to the configured default.

## Context metadata

When a message arrives from Slack, Magec injects metadata about the source. This information is available to the agent as part of the message context (via `MAGEC_META`):

| Field | Description |
|-------|-------------|
| `source` | Always `"slack"` |
| `slack_user_id` | Slack user ID of the sender |
| `slack_username` | Slack username (handle) |
| `slack_name` | Display name (real name) |
| `slack_email` | Email address (requires `users:read.email` scope) |
| `slack_channel_id` | Channel or DM conversation ID |
| `slack_channel_type` | `"im"` for DMs, `"channel"` for channel mentions |
| `slack_team_id` | Slack workspace ID |
| `slack_thread_ts` | Thread timestamp (for channel mentions) |

You can use this in system prompts to personalize responses (e.g., *"Address the user by their first name when available"*).

## Security

{{< callout >}}
**Always restrict access.** Set `allowedUsers` and/or `allowedChannels` to control who can use your bot. Without restrictions, anyone in your Slack workspace who can DM the bot or invite it to a channel can interact with your agents — and through them, any MCP tools those agents have access to.
{{< /callout >}}

If a user not in the allowed list sends a message, it is silently ignored. The same applies for channels not in the allowed list.

## Multiple bots

You can create multiple Slack clients, each with its own bot, its own set of allowed users, and its own agent access. For example:

- A **personal bot** with full access to all agents, restricted to your user ID
- A **team bot** with access to specific work agents, restricted to a team channel
- An **ops bot** with access to infrastructure agents, restricted to the ops channel

## Differences from Telegram

| Feature | Slack | Telegram |
|---------|-------|----------|
| Connection | Socket Mode (WebSocket) | Long polling |
| Voice messages | Audio clips (M4A → WAV) | Native voice (OGG → WAV) |
| Response modes | Text, voice, mirror, both | Text, voice, mirror, both |
| Commands | `!` prefix (`!help`, `!agent`, `!responsemode`) | Slash commands (`/help`, `/agent`, `/responsemode`) |
| Channel interaction | @mention → thread reply | Works in groups with bot commands |
| Voice response format | Uploaded file (Opus) | Native voice message (OGG/Opus) |
