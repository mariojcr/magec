---
title: "Docker Compose"
---

The fully local deployment. Everything runs on your machine: LLM, speech-to-text, text-to-speech, embeddings, and memory. No API keys, no cloud accounts, no data leaving your network.

**Requirements:**
- Docker and Docker Compose
- At least 8 GB of RAM (for Ollama + the LLM model)
- ~6 GB disk space for AI models (downloaded on first start)

## One-line install

The installer downloads the Docker Compose file, pulls images, and starts everything:

```bash
curl -fsSL https://raw.githubusercontent.com/achetronic/magec/master/scripts/install.sh | bash
```

{{< callout type="info" >}}
The first start downloads approximately 5 GB of AI models (Qwen 3 8B for LLM, nomic-embed-text for embeddings). This only happens once. Track progress with `docker compose logs -f ollama-setup`.
{{< /callout >}}

### With NVIDIA GPU

If you have an NVIDIA GPU and [nvidia-container-toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) installed, enable GPU acceleration for significantly faster LLM inference:

```bash
curl -fsSL https://raw.githubusercontent.com/achetronic/magec/master/scripts/install.sh | bash -s -- --gpu
```

## Manual setup

If you prefer to set things up yourself, or want to customize the configuration before starting:

### 1. Download the files

```bash
mkdir magec && cd magec

curl -fsSL https://raw.githubusercontent.com/achetronic/magec/master/docker/compose/docker-compose.yaml \
  -o docker-compose.yaml

curl -fsSL https://raw.githubusercontent.com/achetronic/magec/master/docker/compose/config.yaml \
  -o config.yaml
```

### 2. (Optional) Enable GPU

Edit `docker-compose.yaml` and uncomment the `deploy` section under the `ollama` service:

```yaml
services:
  ollama:
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
```

### 3. Start

```bash
docker compose up -d
```

## What gets deployed

| Container | Purpose | Port |
|-----------|---------|------|
| **magec** | Magec server — Admin UI, Voice UI, API, agent runtime | 8080, 8081 |
| **redis** | Session memory storage | 6379 |
| **postgres** | Long-term memory (pgvector) | 5432 |
| **ollama** | LLM and embeddings (Qwen 3 8B, nomic-embed-text) | 11434 |
| **ollama-setup** | Downloads Ollama models on first start, then exits | — |
| **parakeet** | Speech-to-text (NVIDIA Parakeet) | 8888 |
| **tts** | Text-to-speech (OpenAI Edge TTS) | 5050 |

## Set up your first agent

Once everything is running, open the **Admin UI** at `http://localhost:8081`.

### Create backends

You need three backends — one for the LLM/embeddings, one for STT, one for TTS:

**Ollama (LLM + Embeddings)** — Backends → New:

| Field | Value |
|-------|-------|
| Name | `Ollama` |
| Type | `openai` |
| URL | `http://ollama:11434/v1` |
| API Key | *(leave empty)* |

**Parakeet (STT)** — Backends → New:

| Field | Value |
|-------|-------|
| Name | `Parakeet` |
| Type | `openai` |
| URL | `http://parakeet:8888` |
| API Key | *(leave empty)* |

**Edge TTS (TTS)** — Backends → New:

| Field | Value |
|-------|-------|
| Name | `Edge TTS` |
| Type | `openai` |
| URL | `http://tts:5050` |
| API Key | *(leave empty)* |

### Create memory providers

**Session memory** — Memory → New Session Provider:

| Field | Value |
|-------|-------|
| Type | `redis` |
| URL | `redis://redis:6379` |

**Long-term memory** — Memory → New Long-term Provider:

| Field | Value |
|-------|-------|
| Type | `pgvector` |
| URL | `postgres://magec:magec@postgres:5432/magec?sslmode=disable` |
| Embedding Backend | `Ollama` |
| Embedding Model | `nomic-embed-text` |

### Create an agent

Agents → New:

| Field | Value |
|-------|-------|
| Name | `Assistant` |
| System Prompt | Your agent's personality and instructions |
| LLM Backend | `Ollama` |
| LLM Model | `qwen3:8b` |

Expand the **Voice** section:

| Field | Value |
|-------|-------|
| Transcription Backend | `Parakeet` |
| Transcription Model | `nvidia/parakeet-ctc-0.6b-rnnt` |
| TTS Backend | `Edge TTS` |
| TTS Model | `tts-1` |
| TTS Voice | `es-ES-AlvaroNeural` (or any voice from the Edge TTS catalog) |

### Create a client

Clients → New:

| Field | Value |
|-------|-------|
| Name | `My Voice UI` |
| Type | `Voice UI` |
| Agent | `Assistant` |

Copy the **pairing token**, open `http://localhost:8080`, paste it, and start talking.

## Using cloud providers instead

The Docker Compose includes all the local AI services, but you can use cloud providers by simply creating different backends in the Admin UI. The local services will still be running but unused — or you can stop them.

### OpenAI (handles everything)

Create a single backend:

| Field | Value |
|-------|-------|
| Name | `OpenAI` |
| Type | `openai` |
| API Key | `sk-...` |
| URL | *(leave empty)* |

Use it for the agent's LLM (`gpt-4.1-mini`), transcription (`whisper-1`), and TTS (`tts-1`). For embeddings in long-term memory, use `text-embedding-3-small`.

Then stop the local services you don't need:

```bash
docker compose stop ollama ollama-setup parakeet tts
```

### Anthropic / Gemini (LLM only)

These providers only offer LLM — STT, TTS, and embeddings stay local. Create the cloud backend for the LLM and keep using Parakeet, Edge TTS, and Ollama (for embeddings) as configured above.

| Provider | Backend type | Model example |
|----------|-------------|---------------|
| Anthropic | `anthropic` | `claude-sonnet-4-20250514` |
| Gemini | `gemini` | `gemini-2.0-flash` |

## Managing the deployment

```bash
cd magec                               # your installation directory

docker compose logs -f                 # follow all logs
docker compose logs -f magec           # Magec server only
docker compose logs -f ollama-setup    # model download progress

docker compose down                    # stop everything
docker compose up -d                   # start again

docker compose pull                    # pull latest images
docker compose up -d                   # restart with new versions
```

## Data persistence

All data is stored in Docker volumes:

| Volume | Contains |
|--------|----------|
| `magec_data` | `store.json` (agents, backends, clients), `conversations.json` |
| `redis_data` | Session memory |
| `postgres_data` | Long-term memory (pgvector) |
| `ollama_data` | Downloaded AI models |

Your data survives `docker compose down/up`, image updates, and container recreation. To back up your Magec configuration, copy `data/store.json` from the `magec_data` volume.

To start completely fresh:

```bash
docker compose down -v    # -v removes all volumes
docker compose up -d      # fresh start
```

## Next steps

- **[Configuration](/magec/docs/configuration/)** — understand `config.yaml` vs. Admin UI resources
- **[Agents](/magec/docs/agents/)** — customize agent behavior, prompts, and voice
- **[MCP Tools](/magec/docs/mcp/)** — connect external tools (Home Assistant, GitHub, databases, etc.)
- **[Flows](/magec/docs/flows/)** — chain agents into multi-step workflows
- **[Docker Reference](/magec/docs/docker/)** — advanced Docker topics (image architecture, customization, host access)
