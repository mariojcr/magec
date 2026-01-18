#!/usr/bin/env bash
# Magec installer — https://github.com/achetronic/magec
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/achetronic/magec/master/scripts/install.sh | bash
#   curl -fsSL .../install.sh | bash -s -- --gpu

set -euo pipefail

# ── Config ──────────────────────────────────────────────────────────────────

REPO="achetronic/magec"
BRANCH="master"
BASE_URL="https://raw.githubusercontent.com/${REPO}/${BRANCH}"
INSTALL_DIR="${MAGEC_DIR:-magec}"

# ── Colors ──────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { printf "${CYAN}▸${NC} %s\n" "$*"; }
ok()    { printf "${GREEN}✓${NC} %s\n" "$*"; }
warn()  { printf "${YELLOW}!${NC} %s\n" "$*"; }
err()   { printf "${RED}✗${NC} %s\n" "$*" >&2; }
die()   { err "$@"; exit 1; }

# ── Parse args ──────────────────────────────────────────────────────────────

GPU=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --gpu)  GPU=true; shift ;;
    --dir)  INSTALL_DIR="$2"; shift 2 ;;
    --help|-h)
      cat <<'EOF'

  Magec Installer

  Usage:
    curl -fsSL .../install.sh | bash
    curl -fsSL .../install.sh | bash -s -- [OPTIONS]

  Options:
    --gpu         Enable NVIDIA GPU support for Ollama.
    --dir NAME    Installation directory (default: magec).
    -h, --help    Show this help.

  Environment:
    MAGEC_DIR     Installation directory (alternative to --dir).

  Examples:
    # Default install
    curl -fsSL .../install.sh | bash

    # With NVIDIA GPU
    curl -fsSL .../install.sh | bash -s -- --gpu

EOF
      exit 0
      ;;
    *) die "Unknown option: $1. Use --help for usage." ;;
  esac
done

# ── Banner ──────────────────────────────────────────────────────────────────

printf "\n${BOLD}"
cat <<'EOF'
  __  __
 |  \/  | __ _  __ _  ___  ___
 | |\/| |/ _` |/ _` |/ _ \/ __|
 | |  | | (_| | (_| |  __/ (__
 |_|  |_|\__,_|\__, |\___|\___|
               |___/
EOF
printf "${NC}\n"

info "Mode: ${BOLD}fully local${NC} — no API keys needed"
$GPU && info "GPU: ${BOLD}NVIDIA${NC} enabled"
echo

# ── Preflight checks ───────────────────────────────────────────────────────

check_cmd() {
  if ! command -v "$1" &>/dev/null; then
    die "$1 is required but not installed. $2"
  fi
}

check_cmd docker "Install it from https://docs.docker.com/get-docker/"
check_cmd curl   "Install it with your package manager."

if ! docker compose version &>/dev/null && ! docker-compose version &>/dev/null; then
  die "Docker Compose is required. Install it from https://docs.docker.com/compose/install/"
fi

if ! docker info &>/dev/null; then
  die "Docker daemon is not running. Start it and try again."
fi

ok "All dependencies met"

# ── Resolve compose command ─────────────────────────────────────────────────

if docker compose version &>/dev/null; then
  COMPOSE="docker compose"
else
  COMPOSE="docker-compose"
fi

# ── Download files ──────────────────────────────────────────────────────────

COMPOSE_DIR="docker/compose"

info "Creating ${INSTALL_DIR}/"
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

info "Downloading configuration..."

curl -fsSL "${BASE_URL}/${COMPOSE_DIR}/docker-compose.yaml" -o docker-compose.yaml
curl -fsSL "${BASE_URL}/${COMPOSE_DIR}/config.yaml" -o config.yaml

ok "Files downloaded"

# ── GPU support ─────────────────────────────────────────────────────────────

if $GPU; then
  info "Enabling NVIDIA GPU for Ollama..."
  if command -v sed &>/dev/null; then
    sed -i 's/^    # \(deploy:\)/    \1/' docker-compose.yaml
    sed -i 's/^    #   \(resources:\)/      \1/' docker-compose.yaml
    sed -i 's/^    #     \(reservations:\)/        \1/' docker-compose.yaml
    sed -i 's/^    #       \(devices:\)/          \1/' docker-compose.yaml
    sed -i 's/^    #         \(- driver: nvidia\)/            \1/' docker-compose.yaml
    sed -i 's/^    #           \(count: all\)/              \1/' docker-compose.yaml
    sed -i 's/^    #           \(capabilities: \[gpu\]\)/              \1/' docker-compose.yaml
    ok "GPU support enabled"
  else
    warn "Could not enable GPU automatically. Uncomment the 'deploy' section in docker-compose.yaml manually."
  fi
fi

# ── Launch ──────────────────────────────────────────────────────────────────

echo
info "Starting Magec..."

$COMPOSE up -d

# ── Done ────────────────────────────────────────────────────────────────────

echo
printf "${GREEN}${BOLD}"
cat <<'EOF'
  ┌──────────────────────────────────────────┐
  │           Magec is running! ☀            │
  ├──────────────────────────────────────────┤
  │                                          │
  │   Voice UI  →  http://localhost:8080     │
  │   Admin UI  →  http://localhost:8081     │
  │                                          │
  └──────────────────────────────────────────┘
EOF
printf "${NC}\n"

info "First start downloads ~5GB of models. This may take a few minutes."
info "Track progress: ${BOLD}${COMPOSE} logs -f ollama-setup${NC}"
info "Manage: ${BOLD}cd ${INSTALL_DIR} && ${COMPOSE} [up -d | down | logs]${NC}"
echo
