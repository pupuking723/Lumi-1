#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TARGET_HOST="${TARGET_HOST:-216.128.159.73}"
TARGET_DIR="${TARGET_DIR:-/opt/lumi-pro}"
TARGET_PG_CONTAINER="${TARGET_PG_CONTAINER:-lumi-pro-postgres-1}"
TARGET_COMPOSE_FILE="${TARGET_COMPOSE_FILE:-$TARGET_DIR/docker-compose.yml}"
LOCAL_PG_CONTAINER="${LOCAL_PG_CONTAINER:-goclaw-postgres-1}"
LOCAL_DSN="${LOCAL_DSN:-postgres://goclaw:goclaw@localhost:5432/goclaw?sslmode=disable}"
TENANT="${TENANT:-master}"
AGENTS="${AGENTS:-closy}"
PROVIDERS="${PROVIDERS:-}"
KEEP_REMOTE_SQL="${KEEP_REMOTE_SQL:-0}"

read_env_value() {
  local file="$1"
  local key="$2"
  awk -F= -v key="$key" '
    $1 == key || $1 == "export " key {
      sub(/^export[[:space:]]+/, "", $0)
      sub("^[^=]*=", "", $0)
      gsub(/^["'\'']|["'\'']$/, "", $0)
      print
      exit
    }
  ' "$file"
}

SOURCE_ENCRYPTION_KEY="$(read_env_value "$REPO_ROOT/.env.local" GOCLAW_ENCRYPTION_KEY)"
[[ -n "$SOURCE_ENCRYPTION_KEY" ]] || {
  echo "error: GOCLAW_ENCRYPTION_KEY not found in $REPO_ROOT/.env.local" >&2
  exit 1
}

TARGET_ENCRYPTION_KEY="$(
  ssh "$TARGET_HOST" "awk -F= '/^GOCLAW_ENCRYPTION_KEY=/{print \$2; exit}' '$TARGET_DIR/goclaw.env'"
)"
[[ -n "$TARGET_ENCRYPTION_KEY" ]] || {
  echo "error: GOCLAW_ENCRYPTION_KEY not found on $TARGET_HOST:$TARGET_DIR/goclaw.env" >&2
  exit 1
}

agent_args=()
IFS=',' read -r -a agent_list <<<"$AGENTS"
for agent in "${agent_list[@]}"; do
  [[ -n "$agent" ]] && agent_args+=(--agent "$agent")
done

provider_args=()
if [[ -n "$PROVIDERS" ]]; then
  IFS=',' read -r -a provider_list <<<"$PROVIDERS"
  for provider in "${provider_list[@]}"; do
    [[ -n "$provider" ]] && provider_args+=(--provider "$provider")
  done
fi

stamp="$(date +%Y%m%d%H%M%S)"
local_sql="$(mktemp "/tmp/goclaw-essential-${stamp}.XXXXXX")"
remote_sql="$TARGET_DIR/essential-data-${stamp}.sql"
remote_backup="$TARGET_DIR/backups/goclaw-before-essential-${stamp}.dump"

cleanup() {
  rm -f "$local_sql"
}
trap cleanup EXIT

"$SCRIPT_DIR/essential-data.sh" export \
  --dsn "$LOCAL_DSN" \
  --pg-container "$LOCAL_PG_CONTAINER" \
  --tenant "$TENANT" \
  "${agent_args[@]}" \
  ${provider_args[@]+"${provider_args[@]}"} \
  --source-encryption-key "$SOURCE_ENCRYPTION_KEY" \
  --target-encryption-key "$TARGET_ENCRYPTION_KEY" \
  --output "$local_sql"

scp "$local_sql" "$TARGET_HOST:$remote_sql" >/dev/null

ssh "$TARGET_HOST" "set -e
  mkdir -p '$TARGET_DIR/backups'
  docker exec '$TARGET_PG_CONTAINER' pg_dump -U goclaw -d goclaw -Fc > '$remote_backup'
  docker exec -i '$TARGET_PG_CONTAINER' psql -U goclaw -d goclaw -v ON_ERROR_STOP=1 < '$remote_sql'
  vertex_project=\$(awk -F= '/^GOCLAW_VERTEX_PROJECT_ID=/{print \$2; exit}' '$TARGET_DIR/goclaw.env')
  if [[ -n \"\$vertex_project\" ]]; then
    docker exec '$TARGET_PG_CONTAINER' psql -U goclaw -d goclaw -v ON_ERROR_STOP=1 -c \"UPDATE llm_providers SET settings = jsonb_set(coalesce(settings, '{}'::jsonb), '{project_id}', to_jsonb('\$vertex_project'::text), true), updated_at = now() WHERE name = 'vertex'\"
  fi
  docker exec '$TARGET_PG_CONTAINER' psql -U goclaw -d goclaw -v ON_ERROR_STOP=1 -c \"UPDATE agents SET workspace = '/app/workspace/' || agent_key, updated_at = now() WHERE agent_key = ANY(ARRAY['closy']) AND deleted_at IS NULL\" -c \"UPDATE user_agent_profiles p SET workspace = '/app/workspace/' || a.agent_key || '/http' FROM agents a WHERE p.agent_id = a.id AND a.agent_key = ANY(ARRAY['closy']) AND (p.workspace LIKE '/Users/%' OR p.workspace LIKE '~/.goclaw/%' OR p.workspace IS NULL OR p.workspace = '')\"
  if [[ '$KEEP_REMOTE_SQL' != '1' ]]; then
    rm -f '$remote_sql'
  fi
  cd '$TARGET_DIR'
  docker compose -f '$TARGET_COMPOSE_FILE' up -d --force-recreate goclaw >/dev/null
  docker exec '$TARGET_PG_CONTAINER' psql -U goclaw -d goclaw -P pager=off -c \"select name, provider_type, enabled from llm_providers order by name\"
  docker exec '$TARGET_PG_CONTAINER' psql -U goclaw -d goclaw -P pager=off -c \"select agent_key, display_name, provider, model from agents where deleted_at is null order by agent_key\"
  docker exec '$TARGET_PG_CONTAINER' psql -U goclaw -d goclaw -Atc \"select 'agent_context_files', count(*) from agent_context_files union all select 'user_agent_profiles', count(*) from user_agent_profiles union all select 'closy_style_preferences', count(*) from closy_style_preferences\"
"

echo "synced essential GoClaw data to $TARGET_HOST"
if [[ "$KEEP_REMOTE_SQL" == "1" ]]; then
  echo "remote import SQL: $remote_sql"
else
  echo "remote import SQL removed after import"
fi
echo "remote backup: $remote_backup"
