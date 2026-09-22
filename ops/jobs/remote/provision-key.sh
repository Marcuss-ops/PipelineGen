#!/usr/bin/env bash
# provision-key.sh — mint, rotate or revoke an M2M client key on THIS master.
#
# Run this ON the master host (the one that owns the primary SQLite database),
# not on the remote computer. The remote only ever needs the 0600 env file this
# script writes for it.
#
# ── WHY THIS SCRIPT EXISTS ────────────────────────────────────────────────
# The M2M auth middleware resolves `Authorization: Bearer <secret>` by looking
# up SHA-256(secret) — LOWERCASE HEX — in the primary SQLite `m2m_clients`
# table (internal/platform/sqlite/m2m/store.go, migration
# 252_m2m_clients.sql). This checkout exposes NO admin HTTP endpoint that mints
# keys: `POST /api/v1/admin/m2m/keys` exists only on the remote master build,
# and `cmd/admin` has zero M2M references. So a key is provisioned HERE, with
# the same digest primitive the middleware uses
# (internal/kernel/digest.SHA256String → SHA-256 of the exact bytes, no
# trailing newline), never a second hasher. `preflight.sh` probes that admin
# route; a 404 on this host is expected and is exactly why this script exists.
#
# ── SECRET HANDLING ───────────────────────────────────────────────────────
#   * the plaintext secret is generated locally, written ONCE to a 0600 env
#     file OUTSIDE the repository, and never printed to stdout;
#   * only `secret_hash` is persisted, so a database read (even by an admin
#     with direct SQLite access) cannot recover the secret;
#   * the secret is never passed as a command-line argument (no `ps` leak) and
#     never lands in shell history.
#
# ── PREREQUISITES ─────────────────────────────────────────────────────────
#   * the master must run with M2M ENFORCED: `security.enable_m2m: true`
#     (env VELOX_ENABLE_M2M=true). With it `false` the guard passes through in
#     admin context — the `/api/v1/*` surface is open and the key is neither
#     required nor verified;
#   * the server must have booted at least once so migration 252 created
#     `m2m_clients`.
#
# Usage:
#   ops/jobs/remote/provision-key.sh --list
#   ops/jobs/remote/provision-key.sh --client-id computer-editor-77-01
#   ops/jobs/remote/provision-key.sh --client-id x --scopes jobs.submit,jobs.read
#   ops/jobs/remote/provision-key.sh --client-id x --rotate       # new secret, same row
#   ops/jobs/remote/provision-key.sh --client-id x --disable      # revoke (enabled=0)
#   ops/jobs/remote/provision-key.sh --client-id x --dry-run      # print the SQL only
#   ops/jobs/remote/provision-key.sh --client-id x \
#     --master-url http://77.93.152.122:8000 --env-file ~/computer-editor-77-01.env
#
# Options:
#   --client-id ID      client_id (required unless --list)
#   --description D     free-text description stored on the row
#   --scopes LIST       comma-separated scopes
#                       (default: jobs.submit,jobs.read,media.read)
#   --master-url URL    written as VELOX_MASTER_URL (default http://127.0.0.1:8000)
#   --env-file PATH     output env file (default $HOME/<client-id>.env)
#   --db PATH           primary SQLite DB (default: canonical path resolver)
#   --rate-limit N      rate_limit_rps  (default 2)
#   --burst N           rate_limit_burst (default 10)
#   --quota-scenes N    quota_max_scenes (default 1000)
#   --rotate            replace the secret of an existing client_id
#   --disable           set enabled=0 on an existing client_id (revocation)
#   --list              list provisioned clients (never prints secret material)
#   --dry-run           print the SQL/actions, change nothing
#   --force             allow overwriting an EXISTING --env-file
#
# Exit codes: 0 ok · 1 usage · 2 environment (db/sqlite3 missing) · 3 client_id
#             already exists (use --rotate) · 4 client_id not found
#             · 5 env file exists (use --force or --env-file)
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/../../.." && pwd)"

# shellcheck source=scripts/lib/dotenv.sh
source "$ROOT_DIR/scripts/lib/dotenv.sh"
# shellcheck source=scripts/lib/canonical_db_path.sh
source "$ROOT_DIR/scripts/lib/canonical_db_path.sh"
load_dotenv_missing "$ROOT_DIR/.env"

log()  { printf '[provision-key] %s\n' "$*"; }
fail() { printf '[provision-key] ERROR: %s\n' "$*" >&2; exit 1; }

CLIENT_ID=""
DESCRIPTION=""
SCOPES="jobs.submit,jobs.read,media.read"
MASTER_URL="${VELOX_MASTER_URL:-http://127.0.0.1:8000}"
ENV_FILE=""
DB_PATH="${VELOX_PRIMARY_DB_PATH:-}"
RATE_LIMIT="2"
BURST="10"
QUOTA_SCENES="1000"
MODE="mint"          # mint | rotate | disable | list
DRY_RUN="0"
FORCE="0"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --client-id)    CLIENT_ID="${2:?--client-id needs a value}"; shift 2 ;;
    --description)  DESCRIPTION="${2:?--description needs a value}"; shift 2 ;;
    --scopes)       SCOPES="${2:?--scopes needs a value}"; shift 2 ;;
    --master-url)   MASTER_URL="${2:?--master-url needs a value}"; shift 2 ;;
    --env-file)     ENV_FILE="${2:?--env-file needs a value}"; shift 2 ;;
    --db)           DB_PATH="${2:?--db needs a value}"; shift 2 ;;
    --rate-limit)   RATE_LIMIT="${2:?--rate-limit needs a value}"; shift 2 ;;
    --burst)        BURST="${2:?--burst needs a value}"; shift 2 ;;
    --quota-scenes) QUOTA_SCENES="${2:?--quota-scenes needs a value}"; shift 2 ;;
    --rotate)       MODE="rotate"; shift ;;
    --disable)      MODE="disable"; shift ;;
    --list)         MODE="list"; shift ;;
    --dry-run)      DRY_RUN="1"; shift ;;
    --force)        FORCE="1"; shift ;;
    -h|--help)      sed -n '2,58p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "provision-key: unknown argument $1" >&2; exit 1 ;;
  esac
done

command -v sqlite3 >/dev/null 2>&1 || fail "sqlite3 not on PATH (exit 2)"
[[ -n "$DB_PATH" ]] || DB_PATH="$(canonical_primary_db_path "$ROOT_DIR")"
DB_PATH="$(require_canonical_primary_db "$DB_PATH")" || fail "canonical primary DB not found (exit 2)"

sql() { sqlite3 -noheader -batch "$DB_PATH" "$1"; }

if [[ "$MODE" == "list" ]]; then
  TABLES="$(sql "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='m2m_clients';")"
  [[ "$TABLES" == "1" ]] || fail "m2m_clients does not exist yet — boot the master once so migration 252 runs"
  log "db=$DB_PATH"
  # secret_hash is deliberately NOT selected: it is the only credential-shaped
  # column and there is never a reason to print it.
  sqlite3 -batch -column -header "$DB_PATH" \
    "SELECT client_id, enabled, scopes_json, rate_limit_rps, quota_max_scenes,
            created_at, COALESCE(last_used_at,'-') AS last_used_at
       FROM m2m_clients ORDER BY client_id;"
  exit 0
fi

[[ -n "$CLIENT_ID" ]] || fail "--client-id is required"
[[ "$CLIENT_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] \
  || fail "client_id must match ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ (got: $CLIENT_ID)"
[[ "$RATE_LIMIT" =~ ^[0-9]+(\.[0-9]+)?$ ]] || fail "--rate-limit must be numeric"
[[ "$BURST" =~ ^[0-9]+$ ]] || fail "--burst must be an integer"
[[ "$QUOTA_SCENES" =~ ^[0-9]+$ ]] || fail "--quota-scenes must be an integer"
[[ -n "$SCOPES" ]] || fail "--scopes must not be empty"

# Reject typos loudly: a scope that nothing checks is a silent no-op grant, and
# a scope this surface never issues silently produces 403s on every route.
VALID_SCOPES=("jobs.submit" "jobs.read" "media.read")
IFS=',' read -r -a SCOPE_LIST <<<"$SCOPES"
for s in "${SCOPE_LIST[@]}"; do
  [[ -n "$s" ]] || fail "--scopes contains an empty entry"
  found="0"
  for v in "${VALID_SCOPES[@]}"; do [[ "$s" == "$v" ]] && found="1"; done
  [[ "$found" == "1" ]] || fail "unknown scope '$s' (known: ${VALID_SCOPES[*]})"
done

TABLES="$(sql "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='m2m_clients';")"
[[ "$TABLES" == "1" ]] || fail "m2m_clients does not exist yet — boot the master once so migration 252 runs"

# SQL string escaping: double every single quote. The client_id is already
# charset-restricted; the description is free text.
esc() { printf '%s' "${1//\'/\'\'}"; }

# Build the JSON scope array as a SQLite JSON literal (the middleware
# json.Unmarshal's this column into []string).
scopes_json="["
for i in "${!SCOPE_LIST[@]}"; do
  [[ "$i" -gt 0 ]] && scopes_json+=","
  scopes_json+="\"$(esc "${SCOPE_LIST[$i]}")\""
done
scopes_json+="]"

EXISTS="$(sql "SELECT COUNT(*) FROM m2m_clients WHERE client_id='$(esc "$CLIENT_ID")';")"

describe_sql() {
  case "$MODE" in
    mint)
      cat <<SQL
INSERT INTO m2m_clients
  (client_id, description, secret_hash, scopes_json, enabled,
   rate_limit_rps, rate_limit_burst, quota_max_scenes)
VALUES
  ('$(esc "$CLIENT_ID")', '$(esc "$DESCRIPTION")', '<sha256-hex(secret)>',
   '$(esc "$scopes_json")', 1, $RATE_LIMIT, $BURST, $QUOTA_SCENES);
SQL
      ;;
    rotate)
      cat <<SQL
UPDATE m2m_clients
   SET secret_hash = '<sha256-hex(new-secret)>'
 WHERE client_id = '$(esc "$CLIENT_ID")';
SQL
      ;;
    disable)
      cat <<SQL
UPDATE m2m_clients SET enabled = 0 WHERE client_id = '$(esc "$CLIENT_ID")';
SQL
      ;;
  esac
}

case "$MODE" in
  mint)
    if [[ "$EXISTS" != "0" ]]; then
      log "client '$CLIENT_ID' already exists — refusing to overwrite its secret"
      log "use --rotate (new secret) or --disable (revoke), or --list to inspect"
      exit 3
    fi
    ;;
  rotate|disable)
    if [[ "$EXISTS" == "0" ]]; then
      log "client '$CLIENT_ID' not found in $DB_PATH"
      exit 4
    fi
    ;;
esac

if [[ "$MODE" == "disable" ]]; then
  if [[ "$DRY_RUN" == "1" ]]; then
    log "dry-run — SQL that would run:"; describe_sql
    exit 0
  fi
  sql "UPDATE m2m_clients SET enabled = 0 WHERE client_id='$(esc "$CLIENT_ID")';"
  log "client '$CLIENT_ID' DISABLED (enabled=0) — every request now answers 403"
  log "db=$DB_PATH"
  exit 0
fi

# ── secret generation + canonical digest ──────────────────────────────────
gen_secret() {
  if command -v openssl >/dev/null 2>&1; then
    printf 'velox_m2m_%s' "$(openssl rand -hex 32)"
  elif [[ -r /dev/urandom ]]; then
    printf 'velox_m2m_%s' "$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')"
  else
    fail "no CSPRNG available (need openssl or /dev/urandom)"
  fi
}

# MUST be byte-identical to internal/kernel/digest.SHA256String: SHA-256 over
# the raw secret bytes, lowercase hex. `printf '%s'` (never `echo`) so no
# trailing newline enters the digest.
hash_secret() {
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$1" | sha256sum | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    printf '%s' "$1" | shasum -a 256 | awk '{print $1}'
  else
    fail "no sha256 tool available (need sha256sum or shasum)"
  fi
}

SECRET="$(gen_secret)"
SECRET_HASH="$(hash_secret "$SECRET")"
[[ "${#SECRET_HASH}" == "64" ]] || fail "digest is not 64 hex chars — refusing to write an unusable credential"

if [[ -z "$ENV_FILE" ]]; then
  ENV_FILE="$HOME/${CLIENT_ID}.env"
fi

if [[ "$DRY_RUN" == "1" ]]; then
  log "dry-run — would write $ENV_FILE (0600) and run:"
  log "  secret prefix: ${SECRET:0:14}…  digest: ${SECRET_HASH:0:12}…"
  [[ -e "$ENV_FILE" ]] && log "  NOTE: $ENV_FILE already exists — a real run would refuse (use --force)"
  describe_sql
  exit 0
fi

# Refuse to clobber an existing credential file. This guard runs BEFORE the DB
# write on purpose: minting a row and then bailing on the env file would leave
# an orphan credential whose secret was never handed to anyone, and overwriting
# it silently would destroy a key some other host may still be using.
if [[ -e "$ENV_FILE" && "$FORCE" != "1" ]]; then
  log "env file $ENV_FILE already exists — refusing to overwrite an existing credential"
  log "pass --force to replace it, or --env-file PATH to write elsewhere (no DB change made)"
  exit 5
fi

case "$MODE" in
  mint)
    sql "INSERT INTO m2m_clients
           (client_id, description, secret_hash, scopes_json, enabled,
            rate_limit_rps, rate_limit_burst, quota_max_scenes)
         VALUES
           ('$(esc "$CLIENT_ID")', '$(esc "$DESCRIPTION")', '$SECRET_HASH',
            '$(esc "$scopes_json")', 1, $RATE_LIMIT, $BURST, $QUOTA_SCENES);"
    ;;
  rotate)
    sql "UPDATE m2m_clients SET secret_hash='$SECRET_HASH', enabled=1
          WHERE client_id='$(esc "$CLIENT_ID")';"
    ;;
esac

# Write the client env file with a 0600 mask BEFORE the secret touches disk, so
# the file never exists with wider permissions even for an instant.
ENV_DIR="$(dirname -- "$ENV_FILE")"
mkdir -p -- "$ENV_DIR"
umask 077
cat >"$ENV_FILE" <<ENV
# M2M client credentials for '$CLIENT_ID' (generated by provision-key.sh).
# Scope of the credential: ${SCOPES}
# Keep this file 0600 and outside version control.
VELOX_MASTER_URL=${MASTER_URL}
VELOX_CLIENT_ID=${CLIENT_ID}
VELOX_M2M_SECRET=${SECRET}
ENV
chmod 600 "$ENV_FILE"

log "$([[ "$MODE" == "rotate" ]] && echo "rotated" || echo "minted") client '$CLIENT_ID'"
log "  scopes      : ${SCOPES}"
log "  db          : $DB_PATH"
log "  env file    : $ENV_FILE (0600)"
log "  secret      : (written to the env file only — not printed here)"
log ""
log "remote computer: export VELOX_M2M_ENV=$ENV_FILE  then run:"
log "  ops/jobs/remote/preflight.sh"
log "  ops/jobs/remote/verify-remote.sh"
