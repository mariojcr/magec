---
title: "Docker Reference"
---

This page covers advanced Docker topics: the container architecture, Docker Compose file structure, GPU support, data persistence, customization, and common operations. For getting started with Docker, see the installation guides:

- [Docker Quick Start (OpenAI)](/docs/install-docker/) — single container, minimal setup
- [Docker Compose — Local](/docs/install-compose-local/) — fully local, all services
- [Docker Compose — Cloud](/docs/install-compose-cloud/) — cloud AI providers

## Container architecture

A full Magec deployment includes several containers working together:

| Container | Purpose | Always present |
|-----------|---------|---------------|
| **magec** | The Magec server — API, Admin UI, Voice UI, agent runtime | Yes |
| **redis** | Session memory storage | Yes |
| **postgres** | Long-term memory storage (pgvector) | Yes |
| **ollama** | Local LLM and embeddings (Qwen 3, nomic-embed-text) | Local mode only |
| **ollama-setup** | Downloads Ollama models on first start, then exits | Local mode only |
| **parakeet** | Local speech-to-text (NVIDIA Parakeet) | Local, Anthropic, Gemini |
| **tts** | Local text-to-speech (OpenAI Edge TTS) | Local, Anthropic, Gemini |

In cloud modes (OpenAI, Anthropic, Gemini), some containers are replaced by cloud API calls. Redis and PostgreSQL always run locally because they store your data.

## Docker Compose files

## Docker Compose file

The install script generates a `docker-compose.yaml` with all services pre-configured for your deployment. GPU acceleration for Ollama is included when you enable NVIDIA support during the interactive setup.

## Docker image

The Magec Docker image (`ghcr.io/achetronic/magec`) is built with a multi-stage Dockerfile:

| Stage | Base | Purpose |
|-------|------|---------|
| `frontend` | `node:22-slim` | Builds Admin UI and Voice UI |
| `models` | `golang:1.25-alpine` | Downloads auxiliary ONNX models from HuggingFace |
| `ffmpeg` | `mwader/static-ffmpeg:7.1` | Provides static ffmpeg binary (~135MB) |
| `onnx` | `debian:bookworm-slim` | Downloads ONNX Runtime shared library (arch-aware) |
| `builder` | `golang:1.25` | Compiles Go binary with everything embedded |
| Runtime | `gcr.io/distroless/cc-debian12` | Final image — binary + ffmpeg + ONNX Runtime |

The runtime image uses **distroless** — no shell, no package manager, minimal attack surface. The image is available for `linux/amd64` and `linux/arm64`.

## GPU support

The interactive installer detects NVIDIA GPUs and offers to enable GPU acceleration for Ollama automatically. You can also enable it manually by editing `docker-compose.override.yaml`:

Requirements:

- An NVIDIA GPU
- [nvidia-container-toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) installed
- Docker configured to use the NVIDIA runtime

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

## Data persistence

All persistent data is stored in Docker volumes:

| Volume | Contains |
|--------|----------|
| `magec_data` | `store.json` (configuration), `conversations.json` |
| `redis_data` | Session memory |
| `postgres_data` | Long-term memory (pgvector) |
| `ollama_data` | Downloaded AI models |

Data survives container restarts, image updates, and `docker compose down/up` cycles.

To back up, copy `store.json` from the `magec_data` volume. To start fresh:

```bash
docker compose down -v    # removes all volumes
docker compose up -d      # fresh start
```

## Customizing your deployment

### Adding MCP servers as containers

You can extend the Docker Compose configuration to add MCP servers:

```yaml
services:
  hass-mcp:
    image: ghcr.io/achetronic/hass-mcp:latest
    environment:
      - HASS_URL=http://homeassistant:8123
      - HASS_TOKEN=${HASS_TOKEN}
    ports:
      - "8888:8080"
```

Then add the MCP server in the Admin UI pointing at `http://hass-mcp:8080/sse`.

### Changing ports

```yaml
services:
  magec:
    ports:
      - "3000:8080"   # Voice UI + User API on port 3000
      - "3001:8081"   # Admin UI + Admin API on port 3001
```

### Environment variables

All Magec configuration supports `${VAR}` substitution:

```yaml
services:
  magec:
    environment:
      - OPENAI_API_KEY=${OPENAI_API_KEY}
      - LOG_LEVEL=debug
```

### Accessing host services

If your LLM or other services run on the Docker host (not in containers), use `host.docker.internal`:

```yaml
services:
  magec:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

{{< callout type="info" >}}
On macOS and Windows, `host.docker.internal` works automatically. On Linux, you need the `extra_hosts` mapping above (or `--add-host=host.docker.internal:host-gateway` with `docker run`).
{{< /callout >}}

## Common operations

```bash
cd magec                                    # your deployment directory

# Logs
docker compose logs -f                      # all services
docker compose logs -f magec                # Magec server only
docker compose logs -f ollama               # Ollama only

# Lifecycle
docker compose down                         # stop everything
docker compose up -d                        # start everything
docker compose restart magec                # restart Magec only

# Updates
docker compose pull                         # pull latest images
docker compose up -d                        # restart with new versions

# Backup
docker compose cp magec:/app/data/store.json ./store-backup.json

# Reset
docker compose down -v                      # stop and remove volumes
docker compose up -d                        # fresh start
```

{{< callout type="info" >}}
When updating, `docker compose pull && docker compose up -d` is all you need. Your configuration in `data/` persists across image updates.
{{< /callout >}}
