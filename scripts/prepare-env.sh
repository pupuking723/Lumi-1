#!/usr/bin/env bash
# prepare-env.sh — Create or update .env with auto-generated secrets.
# Safe to run multiple times: only fills in missing values, never overwrites existing ones.
#
# Usage:  ./scripts/prepare-env.sh

set -euo pipefail

SCRIPT="${BASH_SOURCE[0]}"
SCRIPT_DIR="$(cd "$(dirname "${SCRIPT}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="${GOCLAW_ENV_FILE:-$ROOT_DIR/.env}"
ENV_EXAMPLE="${GOCLAW_ENV_EXAMPLE:-$ROOT_DIR/.env.example}"

# --- helpers ---

gen_hex() { openssl rand -hex "$1" 2>/dev/null || head -c "$1" /dev/urandom | xxd -p | tr -d '\n'; }

# Read current value from .env (KEY=VALUE format, no export prefix).
get_env_val() {
  local key="$1"
  if [ -f "$ENV_FILE" ]; then
    grep -E "^${key}=" "$ENV_FILE" 2>/dev/null | tail -1 | cut -d'=' -f2-
  fi
}

# Set a key in .env. Appends if missing, replaces if empty.
set_env_val() {
  local key="$1" val="$2"
  if [ ! -f "$ENV_FILE" ]; then
    echo "${key}=${val}" >> "$ENV_FILE"
  elif grep -qE "^${key}=" "$ENV_FILE" 2>/dev/null; then
    # Key exists — only replace if current value is empty
    local current
    current="$(get_env_val "$key")"
    if [ -z "$current" ]; then
      if [[ "$OSTYPE" == "darwin"* ]]; then
        sed -i '' "s|^${key}=.*|${key}=${val}|" "$ENV_FILE"
      else
        sed -i "s|^${key}=.*|${key}=${val}|" "$ENV_FILE"
      fi
    fi
  else
    echo "${key}=${val}" >> "$ENV_FILE"
  fi
}

# --- main ---

echo ""
echo "=== GoClaw Environment Preparation ==="
echo ""

# 1. Create .env from .env.example if it doesn't exist
if [ ! -f "$ENV_FILE" ]; then
  if [ -f "$ENV_EXAMPLE" ]; then
    # Strip 'export ' prefix for Docker Compose compatibility
    sed 's/^export //' "$ENV_EXAMPLE" > "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    echo "  [created]   $ENV_FILE from $ENV_EXAMPLE"
  else
    touch "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    echo "  [created]   $ENV_FILE (empty)"
  fi
else
  echo "  [exists]    $ENV_FILE"
fi

# 2. Auto-generate GOCLAW_ENCRYPTION_KEY if missing
current_enc="$(get_env_val GOCLAW_ENCRYPTION_KEY)"
if [ -z "$current_enc" ]; then
  new_key="$(gen_hex 32)"
  set_env_val "GOCLAW_ENCRYPTION_KEY" "$new_key"
  echo "  [generated] GOCLAW_ENCRYPTION_KEY"
else
  echo "  [exists]    GOCLAW_ENCRYPTION_KEY"
fi

# 3. Auto-generate GOCLAW_GATEWAY_TOKEN if missing
current_tok="$(get_env_val GOCLAW_GATEWAY_TOKEN)"
if [ -z "$current_tok" ]; then
  new_tok="$(gen_hex 16)"
  set_env_val "GOCLAW_GATEWAY_TOKEN" "$new_tok"
  echo "  [generated] GOCLAW_GATEWAY_TOKEN"
else
  echo "  [exists]    GOCLAW_GATEWAY_TOKEN"
fi

echo ""
echo "=== Done ==="
echo ""
echo "  Run: make up"
echo ""
echo "  Web dashboard: http://localhost:18790"
echo "  With separate nginx: make up WITH_WEB_NGINX=1 → http://localhost:3000"
echo ""
