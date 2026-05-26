#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Export/import the small set of GoClaw rows needed to bootstrap a production-like
runtime: tenant metadata, providers, selected agents, agent context files,
builtin tool settings, tenant tool overrides, and system configs.

Usage:
  scripts/essential-data.sh export --output essential-data.sql [options]
  scripts/essential-data.sh import --input essential-data.sql [options]

Options:
  --dsn DSN             Postgres DSN. Defaults to GOCLAW_POSTGRES_DSN.
  --tenant VALUE        Tenant UUID or slug. "default" maps to "master".
                        Default: master
  --agent KEY           Agent key to export. May be repeated. Default: closy
  --provider NAME       Provider name to export. May be repeated.
                        Default: all enabled tenant providers plus selected agents' providers.
  --include-secrets     Also export tenant config_secrets. Provider api_key is always exported
                        from llm_providers because providers are part of this bundle.
  -o, --output PATH     Export output path.
  -i, --input PATH      Import input path.
  -h, --help            Show this help.

Notes:
  - Run migrations before importing.
  - llm_providers.api_key is exported exactly as stored. If it is encrypted,
    the target runtime must use the same GOCLAW_ENCRYPTION_KEY.
  - Import uses upsert and does not delete rows that are absent from the export.
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PSQL_MODE=""

MODE="${1:-}"
if [[ -z "$MODE" || "$MODE" == "-h" || "$MODE" == "--help" ]]; then
  usage
  exit 0
fi
shift

DSN="${GOCLAW_POSTGRES_DSN:-}"
TENANT="master"
OUTPUT=""
INPUT=""
INCLUDE_SECRETS=0
AGENTS=()
PROVIDERS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dsn)
      DSN="${2:-}"
      shift 2
      ;;
    --tenant)
      TENANT="${2:-}"
      shift 2
      ;;
    --agent)
      AGENTS+=("${2:-}")
      shift 2
      ;;
    --provider)
      PROVIDERS+=("${2:-}")
      shift 2
      ;;
    --include-secrets)
      INCLUDE_SECRETS=1
      shift
      ;;
    -o|--output)
      OUTPUT="${2:-}"
      shift 2
      ;;
    -i|--input)
      INPUT="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

[[ -n "$DSN" ]] || die "Postgres DSN is required; set GOCLAW_POSTGRES_DSN or pass --dsn"

if [[ ${#AGENTS[@]} -eq 0 ]]; then
  AGENTS=("closy")
fi

join_csv() {
  local IFS=,
  echo "$*"
}

AGENT_CSV="$(join_csv "${AGENTS[@]}")"
if [[ ${#PROVIDERS[@]} -gt 0 ]]; then
  PROVIDER_CSV="$(join_csv "${PROVIDERS[@]}")"
else
  PROVIDER_CSV=""
fi

init_psql() {
  if command -v psql >/dev/null 2>&1; then
    PSQL_MODE="local"
    return
  fi

  if command -v docker >/dev/null 2>&1 &&
    [[ -f "$REPO_ROOT/deploy/compose/docker-compose.yml" ]] &&
    [[ -f "$REPO_ROOT/deploy/compose/docker-compose.postgres.yml" ]] &&
    docker compose \
      -f "$REPO_ROOT/deploy/compose/docker-compose.yml" \
      -f "$REPO_ROOT/deploy/compose/docker-compose.postgres.yml" \
      ps -q postgres >/dev/null 2>&1; then
    PSQL_MODE="docker_compose"
    return
  fi

  die "psql is required. Install it locally, or start the compose postgres service and rerun:
  docker compose -f deploy/compose/docker-compose.yml -f deploy/compose/docker-compose.postgres.yml up -d postgres"
}

psql_base() {
  case "$PSQL_MODE" in
    local)
      psql "$DSN" -X -v ON_ERROR_STOP=1 "$@"
      ;;
    docker_compose)
      docker compose \
        -f "$REPO_ROOT/deploy/compose/docker-compose.yml" \
        -f "$REPO_ROOT/deploy/compose/docker-compose.postgres.yml" \
        exec -T postgres psql "$DSN" -X -v ON_ERROR_STOP=1 "$@"
      ;;
    *)
      die "psql backend not initialized"
      ;;
  esac
}

tenant_expr="CASE WHEN :'tenant' IN ('', 'default') THEN 'master' ELSE :'tenant' END"

json_query() {
  printf "%s\n" "$1" | psql_base -qAt \
    -v tenant="$TENANT" \
    -v agents="$AGENT_CSV" \
    -v providers="$PROVIDER_CSV"
}

ensure_tenant_exists() {
  local found
  found="$(json_query "SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1;")"
  [[ -n "$found" ]] || die "tenant not found: $TENANT"
}

payload_json() {
  local table="$1"
  case "$table" in
    tenants)
      json_query "
        WITH selected_tenant AS (
          SELECT *
          FROM tenants
          WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr)
          LIMIT 1
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(selected_tenant) ORDER BY slug), '[]'::jsonb)::text
        FROM selected_tenant;"
      ;;
    tenant_users)
      json_query "
        WITH selected_tenant AS (
          SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(tu) ORDER BY user_id), '[]'::jsonb)::text
        FROM tenant_users tu
        JOIN selected_tenant t ON t.id = tu.tenant_id;"
      ;;
    llm_providers)
      json_query "
        WITH selected_tenant AS (
          SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1
        ),
        selected_agents AS (
          SELECT a.*
          FROM agents a
          JOIN selected_tenant t ON t.id = a.tenant_id
          WHERE a.deleted_at IS NULL
            AND a.agent_key = ANY(string_to_array(:'agents', ','))
        ),
        selected_provider_names AS (
          SELECT unnest(string_to_array(:'providers', ',')) AS name
          WHERE :'providers' <> ''
          UNION
          SELECT provider FROM selected_agents WHERE provider <> ''
          UNION
          SELECT p.name
          FROM llm_providers p
          JOIN selected_tenant t ON t.id = p.tenant_id
          WHERE :'providers' = '' AND p.enabled = true
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.name), '[]'::jsonb)::text
        FROM llm_providers p
        JOIN selected_tenant t ON t.id = p.tenant_id
        WHERE p.name IN (SELECT name FROM selected_provider_names WHERE name <> '');"
      ;;
    agents)
      json_query "
        WITH selected_tenant AS (
          SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(a) - 'embedding' - 'tsv' ORDER BY a.agent_key), '[]'::jsonb)::text
        FROM agents a
        JOIN selected_tenant t ON t.id = a.tenant_id
        WHERE a.deleted_at IS NULL
          AND a.agent_key = ANY(string_to_array(:'agents', ','));"
      ;;
    agent_context_files)
      json_query "
        WITH selected_tenant AS (
          SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1
        ),
        selected_agents AS (
          SELECT a.id
          FROM agents a
          JOIN selected_tenant t ON t.id = a.tenant_id
          WHERE a.deleted_at IS NULL
            AND a.agent_key = ANY(string_to_array(:'agents', ','))
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(f) ORDER BY f.agent_id, f.file_name), '[]'::jsonb)::text
        FROM agent_context_files f
        JOIN selected_agents a ON a.id = f.agent_id;"
      ;;
    agent_config_permissions)
      json_query "
        WITH selected_tenant AS (
          SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1
        ),
        selected_agents AS (
          SELECT a.id
          FROM agents a
          JOIN selected_tenant t ON t.id = a.tenant_id
          WHERE a.deleted_at IS NULL
            AND a.agent_key = ANY(string_to_array(:'agents', ','))
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY p.agent_id, p.scope, p.config_type, p.user_id), '[]'::jsonb)::text
        FROM agent_config_permissions p
        JOIN selected_agents a ON a.id = p.agent_id;"
      ;;
    builtin_tools)
      json_query "
        SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.name), '[]'::jsonb)::text
        FROM builtin_tools t;"
      ;;
    builtin_tool_tenant_configs)
      json_query "
        WITH selected_tenant AS (
          SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY c.tool_name), '[]'::jsonb)::text
        FROM builtin_tool_tenant_configs c
        JOIN selected_tenant t ON t.id = c.tenant_id;"
      ;;
    system_configs)
      json_query "
        WITH selected_tenant AS (
          SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY c.key), '[]'::jsonb)::text
        FROM system_configs c
        JOIN selected_tenant t ON t.id = c.tenant_id;"
      ;;
    config_secrets)
      if [[ "$INCLUDE_SECRETS" -ne 1 ]]; then
        echo "[]"
        return
      fi
      json_query "
        WITH selected_tenant AS (
          SELECT id FROM tenants WHERE id::text = ($tenant_expr) OR slug = ($tenant_expr) LIMIT 1
        )
        SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.key), '[]'::jsonb)::text
        FROM config_secrets s
        JOIN selected_tenant t ON t.id = s.tenant_id;"
      ;;
    *)
      die "unknown payload table: $table"
      ;;
  esac
}

write_sql_header() {
  local out="$1"
  cat >"$out" <<'SQL'
-- GoClaw essential runtime data seed.
-- Generated by scripts/essential-data.sh.
-- Import with:
--   psql "$GOCLAW_POSTGRES_DSN" -v ON_ERROR_STOP=1 -f this-file.sql
--
-- This file contains provider rows as stored in llm_providers. If api_key is
-- encrypted, the target runtime must use the same GOCLAW_ENCRYPTION_KEY.

BEGIN;

CREATE TEMP TABLE _goclaw_seed_payload (
  table_name text PRIMARY KEY,
  data jsonb NOT NULL
) ON COMMIT DROP;

CREATE OR REPLACE FUNCTION pg_temp._goclaw_payload(_table_name text)
RETURNS jsonb
LANGUAGE sql
AS $$
  SELECT COALESCE(
    (SELECT data FROM _goclaw_seed_payload WHERE table_name = _table_name),
    '[]'::jsonb
  )
$$;

CREATE OR REPLACE FUNCTION pg_temp._goclaw_upsert_json(
  _target regclass,
  _payload jsonb,
  _conflict text,
  _conflict_where text DEFAULT '',
  _update_exclude text[] DEFAULT ARRAY[]::text[]
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  _cols text;
  _updates text;
  _action text;
  _conflict_suffix text;
BEGIN
  IF _payload IS NULL OR jsonb_array_length(_payload) = 0 THEN
    RETURN;
  END IF;

  WITH keys AS (
    SELECT DISTINCT jsonb_object_keys(value) AS key
    FROM jsonb_array_elements(_payload)
  )
  SELECT string_agg(quote_ident(a.attname), ', ' ORDER BY a.attnum)
    INTO _cols
  FROM pg_attribute a
  JOIN keys k ON k.key = a.attname
  WHERE a.attrelid = _target
    AND a.attnum > 0
    AND NOT a.attisdropped
    AND a.attgenerated = '';

  IF _cols IS NULL OR _cols = '' THEN
    RETURN;
  END IF;

  WITH keys AS (
    SELECT DISTINCT jsonb_object_keys(value) AS key
    FROM jsonb_array_elements(_payload)
  )
  SELECT string_agg(format('%1$I = EXCLUDED.%1$I', a.attname), ', ' ORDER BY a.attnum)
    INTO _updates
  FROM pg_attribute a
  JOIN keys k ON k.key = a.attname
  WHERE a.attrelid = _target
    AND a.attnum > 0
    AND NOT a.attisdropped
    AND a.attgenerated = ''
    AND NOT (a.attname = ANY(_update_exclude));

  IF _updates IS NULL OR _updates = '' THEN
    _action := 'DO NOTHING';
  ELSE
    _action := 'DO UPDATE SET ' || _updates;
  END IF;

  _conflict_suffix := CASE
    WHEN _conflict_where IS NULL OR _conflict_where = '' THEN ''
    ELSE ' ' || _conflict_where
  END;

  EXECUTE format(
    'INSERT INTO %s (%s) SELECT %s FROM jsonb_populate_recordset(NULL::%s, $1) ON CONFLICT %s%s %s',
    _target, _cols, _cols, _target, _conflict, _conflict_suffix, _action
  )
  USING _payload;
END;
$$;
SQL
}

append_payload() {
  local out="$1"
  local table="$2"
  local json="$3"
  local tag="$4"
  {
    printf "\n-- payload: %s\n" "$table"
    printf "INSERT INTO _goclaw_seed_payload(table_name, data) VALUES ('%s', $%s$%s$%s$::jsonb);\n" "$table" "$tag" "$json" "$tag"
  } >>"$out"
}

write_sql_footer() {
  local out="$1"
  cat >>"$out" <<'SQL'

-- Independent rows first.
DO $goclaw_seed$
BEGIN
  PERFORM pg_temp._goclaw_upsert_json('tenants', pg_temp._goclaw_payload('tenants'), '(id)', '', ARRAY['id']);
  PERFORM pg_temp._goclaw_upsert_json('tenant_users', pg_temp._goclaw_payload('tenant_users'), '(tenant_id, user_id)', '', ARRAY['id']);
  PERFORM pg_temp._goclaw_upsert_json('llm_providers', pg_temp._goclaw_payload('llm_providers'), '(tenant_id, name)', '', ARRAY['id']);
  PERFORM pg_temp._goclaw_upsert_json('builtin_tools', pg_temp._goclaw_payload('builtin_tools'), '(name)', '', ARRAY['name']);
  PERFORM pg_temp._goclaw_upsert_json('system_configs', pg_temp._goclaw_payload('system_configs'), '(key, tenant_id)', '', ARRAY['key', 'tenant_id']);
  PERFORM pg_temp._goclaw_upsert_json('config_secrets', pg_temp._goclaw_payload('config_secrets'), '(key, tenant_id)', '', ARRAY['key', 'tenant_id']);
  PERFORM pg_temp._goclaw_upsert_json('builtin_tool_tenant_configs', pg_temp._goclaw_payload('builtin_tool_tenant_configs'), '(tool_name, tenant_id)', '', ARRAY['tool_name', 'tenant_id']);
END
$goclaw_seed$;

-- Agents can already exist because GoClaw seeds "closy" on startup. Keep the
-- target id on conflict, then map exported agent_id references onto the live id.
CREATE TEMP TABLE _goclaw_seed_agents ON COMMIT DROP AS
SELECT *
FROM jsonb_populate_recordset(NULL::agents, pg_temp._goclaw_payload('agents'));

DO $goclaw_seed$
BEGIN
  PERFORM pg_temp._goclaw_upsert_json(
    'agents',
    pg_temp._goclaw_payload('agents'),
    '(tenant_id, agent_key)',
    'WHERE deleted_at IS NULL',
    ARRAY['id']
  );
END
$goclaw_seed$;

CREATE TEMP TABLE _goclaw_seed_agent_id_map ON COMMIT DROP AS
SELECT s.id AS exported_id, a.id AS live_id
FROM _goclaw_seed_agents s
JOIN agents a
  ON a.tenant_id = s.tenant_id
 AND a.agent_key = s.agent_key
 AND a.deleted_at IS NULL;

DO $goclaw_seed$
DECLARE
  payload jsonb;
BEGIN
  WITH mapped AS (
    SELECT to_jsonb(r) || jsonb_build_object('agent_id', m.live_id) AS row_json
    FROM jsonb_populate_recordset(NULL::agent_context_files, pg_temp._goclaw_payload('agent_context_files')) AS r
    JOIN _goclaw_seed_agent_id_map m ON m.exported_id = r.agent_id
  )
  SELECT COALESCE(jsonb_agg(row_json), '[]'::jsonb) INTO payload
  FROM mapped;

  PERFORM pg_temp._goclaw_upsert_json(
    'agent_context_files',
    payload,
    '(agent_id, file_name)',
    '',
    ARRAY['id']
  );

  WITH mapped AS (
    SELECT to_jsonb(r) || jsonb_build_object('agent_id', m.live_id) AS row_json
    FROM jsonb_populate_recordset(NULL::agent_config_permissions, pg_temp._goclaw_payload('agent_config_permissions')) AS r
    JOIN _goclaw_seed_agent_id_map m ON m.exported_id = r.agent_id
  )
  SELECT COALESCE(jsonb_agg(row_json), '[]'::jsonb) INTO payload
  FROM mapped;

  PERFORM pg_temp._goclaw_upsert_json(
    'agent_config_permissions',
    payload,
    '(agent_id, scope, config_type, user_id)',
    '',
    ARRAY['id']
  );
END
$goclaw_seed$;

COMMIT;
SQL
}

do_export() {
  [[ -n "$OUTPUT" ]] || die "export requires --output"
  init_psql
  ensure_tenant_exists

  local tag="goclaw_seed_$(date +%s)_$$"
  local tmp
  tmp="$(mktemp "${OUTPUT}.tmp.XXXXXX")"
  trap 'rm -f "$tmp"' RETURN

  write_sql_header "$tmp"
  local tables=(
    tenants
    tenant_users
    llm_providers
    builtin_tools
    system_configs
    config_secrets
    builtin_tool_tenant_configs
    agents
    agent_context_files
    agent_config_permissions
  )
  local table json
  for table in "${tables[@]}"; do
    json="$(payload_json "$table")"
    append_payload "$tmp" "$table" "$json" "$tag"
  done
  write_sql_footer "$tmp"
  mv "$tmp" "$OUTPUT"
  trap - RETURN

  echo "exported essential data to $OUTPUT"
  echo "tenant=$TENANT agents=$AGENT_CSV providers=${PROVIDER_CSV:-enabled+agent-providers} include_secrets=$INCLUDE_SECRETS"
}

do_import() {
  [[ -n "$INPUT" ]] || die "import requires --input"
  [[ -f "$INPUT" ]] || die "input file not found: $INPUT"
  init_psql
  psql_base -q <"$INPUT"
  echo "imported essential data from $INPUT"
}

case "$MODE" in
  export)
    do_export
    ;;
  import)
    do_import
    ;;
  *)
    die "unknown mode: $MODE"
    ;;
esac
