#!/usr/bin/env bash
# tests/operational/lib/common.sh — shared helpers for black-box smoke tests.
#
# Source-able library. Every smoke script does:
#   DIR=$(cd "$(dirname "$0")" && pwd)
#   # shellcheck disable=SC1091
#   source "$DIR/lib/common.sh"
#
# Contract:
#   - exit 0  : every assertion passed
#   - exit 1  : one or more assertions failed
#   - exit 2  : internal setup error (unknown flag, missing token, missing binary)
#   - exit 124: polling loop or wall-clock timeout exceeded
#
# Environment variables (all overridable; defaults shown):
#   API_BASE                        host:port (default 127.0.0.1:${VELOX_PORT:-8080})
#   VELOX_ADMIN_TOKEN               bearer token (mandatory if TOKEN_FILE unset)
#   TOKEN_FILE                      env file containing VELOX_ADMIN_TOKEN=… (alt)
#   SMOKE_TIMEOUT_SECONDS           per-script overall wall clock (default 180)
#   SMOKE_POLL_TIMEOUT_SECONDS      poll loop ceiling (default 120)
#   SMOKE_POLL_INTERVAL_SECONDS     poll sleep (default 2)
#   SMOKE_HTTP_TIMEOUT_SECONDS      per-curl --max-time (default 8)
#   SMOKE_DRY_RUN                   non-empty => equivalent to passing --dry
#   NO_COLOR                        non-empty disables ANSI colour codes

set -euo pipefail
umask 077

# ── Colour variables — initialised BEFORE any printf that might reference them.
# Pre-fill with empty defaults so `RED`/`GREEN`/etc. are always defined, then
# upgrade to tput-detected values when stdout is a TTY and NO_COLOR is unset.
# Bug-fix (June 2026): these used to live further down the file and any earlier
# printf that referenced them under `set -u` (e.g. when sourcing in a non-tty
# context) raised "unbound variable" before the colour setup ever ran.
RED=""
GREEN=""
YELLOW=""
CYAN=""
DIM=""
RESET=""
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
    RED=$(tput setaf 1 2>/dev/null || true)
    GREEN=$(tput setaf 2 2>/dev/null || true)
    YELLOW=$(tput setaf 3 2>/dev/null || true)
    CYAN=$(tput setaf 6 2>/dev/null || true)
    DIM=$(tput dim 2>/dev/null || true)
    RESET=$(tput sgr0 2>/dev/null || true)
fi
export RED GREEN YELLOW CYAN DIM RESET

# ── Preflight: missing-binary guard ─────────────────────────────────────────
# Centralised check that runs at source-time. A missing binary aborts with
# exit 2 BEFORE any HTTP request fires, so log inspection shows the missing
# dep name first (rather than a cryptic "jq: command not found" nested under
# a half-completed curl chain).
#
# Only `jq` is pre-required unconditionally (used by smoke_poll_terminal and
# every *_smoke.sh). The other optional binaries are required per-script via
# a top-level `smoke_require …` call in the smoke script itself.
smoke_require() {
    local missing=()
    local bin
    for bin in "$@"; do
        if ! command -v "$bin" >/dev/null 2>&1; then
            missing+=("$bin")
        fi
    done
    if (( ${#missing[@]} > 0 )); then
        printf '%ssetup error: missing required binaries:%s %s\n' \
            "$RED" "$RESET" "${missing[*]}" >&2
        exit 2
    fi
}
smoke_require jq

# ── Per-script wall clock ──────────────────────────────────────────────────
SMOKE_DEADLINE=$(( $(date +%s) + ${SMOKE_TIMEOUT_SECONDS:-180} ))
smoke_wallclock_check() {
    if (( $(date +%s) >= SMOKE_DEADLINE )); then
        printf '%sFAIL: overall SMOKE_TIMEOUT_SECONDS exceeded%s\n' \
            "$RED" "$RESET" >&2
        exit 124
    fi
}

# ── Workdir + cleanup ─────────────────────────────────────────────────────
WORK_DIR=$(mktemp -d "/tmp/smoke.XXXXXX")
trap 'smoke_cleanup' EXIT INT TERM

smoke_cleanup() {
    # `artlist_run` and similar helpers are invoked through command
    # substitution in the smoke scripts. Bash inherits EXIT traps into
    # those subshells, so the cleanup must no-op there or the child shell
    # would delete the parent shell's WORK_DIR mid-run.
    if [[ "${BASHPID:-$$}" != "$$" ]]; then
        return 0
    fi
    if [[ -n "${WORK_DIR:-}" && -d "$WORK_DIR" ]]; then
        rm -rf "$WORK_DIR"
    fi
}

# ── Effective API base + tunables ─────────────────────────────────────────
SMOKE_API_BASE="${API_BASE:-127.0.0.1:${VELOX_PORT:-8000}}"
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-180}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-120}"
SMOKE_POLL_INTERVAL_SECONDS="${SMOKE_POLL_INTERVAL_SECONDS:-2}"
SMOKE_HTTP_TIMEOUT_SECONDS="${SMOKE_HTTP_TIMEOUT_SECONDS:-8}"

# ── Curl result state — initialised empty so `set -u` (nounset) never aborts
# when a smoke script reads these BEFORE the first smoke_curl() call (e.g.
# common.sh:smoke_assert_http_2xx referencing SMOKE_LAST_HTTP, or
# outbox_smoke.sh reading SMOKE_LAST_BODY from a pre-curl diagnostic path).
# smoke_curl() overwrites both on every call.
SMOKE_LAST_HTTP=""
SMOKE_LAST_BODY=""
SMOKE_LAST_STATUS=""

# ── CLI flags ─────────────────────────────────────────────────────────────
# SMOKE_DRY_RUN=1 env var is honoured so callers can flip dry mode without
# passing --dry explicitly (e.g. `SMOKE_DRY_RUN=1 make smoke-dry`).
DRY_RUN=0
HELP_REQUESTED=0
if [[ "${SMOKE_DRY_RUN:-0}" == "1" ]]; then
    DRY_RUN=1
fi
for arg in "$@"; do
    case "$arg" in
        --dry) DRY_RUN=1 ;;
        -h|--help) HELP_REQUESTED=1 ;;
        *) printf '%ssetup error: unknown flag %s%s\n' "$RED" "$arg" "$RESET" >&2
            exit 2 ;;
    esac
done
export DRY_RUN

# ── Token resolution (skipped under --dry or -h/--help so those paths don't
# require a token). Real probes will fail loudly later via smoke_curl if the
# token is still empty; that is intentional (force operators to see the
# authentication gap when actually hitting the server).
smoke_resolve_token() {
    # If already resolved by a parent process (exported via SMOKE_TOKEN),
    # reuse it. This propagates the token through nested script invocations
    # (e.g. full_battery.sh → run_scenario.sh) without requiring
    # VELOX_ADMIN_TOKEN or TOKEN_FILE to be present in every child.
    if [[ -n "${SMOKE_TOKEN:-}" ]]; then
        printf '%s' "$SMOKE_TOKEN"
        return 0
    fi
    # Canonical file takes precedence over bare env var (AGENTS.md SSOT)
    if [[ -n "${TOKEN_FILE:-}" && -f "${TOKEN_FILE}" ]]; then
        local token
        token=$(grep -E '^VELOX_ADMIN_TOKEN=' "$TOKEN_FILE" | head -1 | cut -d= -f2- ||
            true)
        if [[ -n "$token" ]]; then
            printf '%s' "$token"
            return 0
        fi
    fi
    # Last resort: direct env var (backward-compatible with CI)
    if [[ -n "${VELOX_ADMIN_TOKEN:-}" ]]; then
        printf '%s' "$VELOX_ADMIN_TOKEN"
        return 0
    fi
    return 1
}
if [[ "$DRY_RUN" == "1" || "$HELP_REQUESTED" == "1" ]]; then
    SMOKE_TOKEN=""
else
    if ! SMOKE_TOKEN=$(smoke_resolve_token); then
        printf '%ssetup error: VELOX_ADMIN_TOKEN env var unset and TOKEN_FILE not provided%s\n' \
            "$RED" "$RESET" >&2
        exit 2
    fi
    export SMOKE_TOKEN
fi

# ── Logging helpers ──────────────────────────────────────────────────────
smoke_log_section() {
    [[ "$DRY_RUN" == "1" ]] && return 0
    printf '\n%s===== %s =====%s\n' "$CYAN" "$1" "$RESET"
}

# Echo with strict token redaction. Covers:
#   1. Authorization: (Bearer|Basic) <value>           →  … <REDACTED>
#   2. X-Velox-Admin-Token: <value>                   (allowed header per routes.go)
#   3. X-Admin-Token: <value>                         (variant header)
#   4. Bare "Bearer <value>" not under Authorization: →  Bearer <REDACTED>
#   5. URL parameters (?token=…|&token=…)             →  … <REDACTED>
#   6. JSON "token"|"auth"|"authorization"|"password"→  "…":"<REDACTED>"
#   7. VELOX_ADMIN_TOKEN=<value> / TOKEN=<value>      →  …=<REDACTED>
# Every output path (printf, smoke_log_section, smoke_curl diagnostics) MUST
# route through this function so disk-resident log files inherit the same
# redaction guarantee.
smoke_echo_safe() {
    local payload="${*}"
    printf '%s\n' "$payload" | sed -E \
        -e 's/(Authorization:[[:space:]]+(Bearer|Basic)[[:space:]]+)[A-Za-z0-9._~+/-]+=*[^[:space:]]*/\1<REDACTED>/gI' \
        -e 's/(X-(Velox-)?Admin-Token:[[:space:]]+)[A-Za-z0-9._~+/-]+=*[^[:space:]]*/\1<REDACTED>/gI' \
        -e 's/([?&](token|access_token|apikey|api_key|set_token)=)[A-Za-z0-9._~+/-]+=*[^[:space:]]*/\1<REDACTED>/gI' \
        -e 's/(Bearer[[:space:]]+)[A-Za-z0-9._~+/-]+=*[^[:space:]]*/\1<REDACTED>/gI' \
        -e 's/("(token|auth|authorization|password)"[[:space:]]*:[[:space:]]*")[^"]*"/\1<REDACTED>"/gI' \
        -e 's/(VELOX_ADMIN_TOKEN|TOKEN)=[^[:space:]]+/\1=<UNSET>/g'
}

# Write a diagnostic dump to $SMOKE_LOG_DIR (mode 0600; never printed to
# stdout/stderr). The dump is FIRST routed through smoke_echo_safe so even
# the on-disk copy is token-redacted. Used for forensic inspection after a
# failed smoke run.
smoke_log_response() {
    local label="$1"
    if [[ "$DRY_RUN" == "1" || -z "${SMOKE_LOG_DIR:-}" ]]; then
        return 0
    fi
    mkdir -p "$SMOKE_LOG_DIR"
    chmod 700 "$SMOKE_LOG_DIR" 2>/dev/null || true
    local out="$SMOKE_LOG_DIR/${label}.txt"
    {
        echo "=== $(date -Iseconds 2>/dev/null || date) ==="
        echo "label: $label"
        echo "http : ${SMOKE_LAST_HTTP:-?}"
        smoke_echo_safe "$(cat "${SMOKE_LAST_BODY:-/dev/null}" 2>/dev/null || true)"
    } > "$out"
    chmod 600 "$out"
}

# Portable UUID source. Order matters:
#   1. uuidgen   (util-linux on Linux; ships by default on macOS via OSXFoundation)
#   2. /proc/sys/kernel/random/uuid  (Linux kernel only)
#   3. python3 -c '…'                (universal fallback)
#   4. epoch + RANDOM                 (last resort; unique enough for the
#                                       black-box "this ID should not exist"
#                                       guarantee, not a true v4 UUID)
# failed_job_smoke uses this to mint guaranteed-nonexistent job IDs so the
# negative-path probe is reproducible across macOS dev boxes, Linux CI, and
# sandboxed runners without /proc.
smoke_gen_uuid() {
    if command -v uuidgen >/dev/null 2>&1; then
        uuidgen
        return 0
    fi
    if [[ -r /proc/sys/kernel/random/uuid ]]; then
        cat /proc/sys/kernel/random/uuid
        return 0
    fi
    if command -v python3 >/dev/null 2>&1; then
        python3 -c 'import uuid; print(uuid.uuid4())'
        return 0
    fi
    printf 'smoke-%d-%d-%d\n' "$(date +%s)" "$RANDOM" "$RANDOM"
}

# curl wrapper — adds the bearer token and an Idempotency-Key header for
# non-GET requests (never via -H printed by shell).
# Always captures the body in $WORK_DIR/last.body and stores the HTTP code in $SMOKE_LAST_HTTP.
# Diagnostics are routed through smoke_log_response (redacted) when SMOKE_LOG_DIR is set.
smoke_curl() {
    local method="$1"; shift
    local url_path="$1"; shift
    local out_file="$WORK_DIR/last.body"
    local code_file="$WORK_DIR/last.code"
    local code
    local idem_headers=()
    if [[ "$method" != "GET" ]]; then
        idem_headers=(-H "Idempotency-Key: ${SMOKE_IDEMPOTENCY_KEY:-$(smoke_gen_uuid)}")
    fi
    code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X "$method" \
        -o "$out_file" -w '%{http_code}' \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        "${idem_headers[@]}" \
        -H 'Content-Type: application/json' \
        "$@" \
        "http://${SMOKE_API_BASE}${url_path}")
    export SMOKE_LAST_HTTP="$code"
    export SMOKE_LAST_BODY="$out_file"
    # Also write the HTTP code to a file so callers in $(…) subshells can
    # read it after the subshell exits. SMOKE_LAST_HTTP/SIDE effects of
    # smoke_curl are lost when called inside $(…) because exports die with
    # the child process. The .code file persists in $WORK_DIR.
    printf '%s' "$code" > "$code_file"
    printf '%s' "$code"
}

# Poll /api/jobs/{id} until the job reaches a terminal status.
# Terminal statuses: completed, failed, cancelled, dead_letter.
# Returns 0 on terminal state, 124 on timeout.
# After return 0, SMOKE_LAST_BODY contains the /api/jobs/{id} status body.
# Callers needing the FULL result (segments, candidates, etc.) must fetch
# /api/jobs/{id}/full separately after this function returns.
smoke_poll_terminal() {
    local job_id="$1"
    SMOKE_LAST_STATUS=""
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        # NOTE: smoke_curl MUST NOT run inside $(…) — it sets
        # SMOKE_LAST_BODY/SMOKE_LAST_HTTP as side-effects; inside a subshell
        # those exports are lost and subsequent jq reads fail with
        # "No such file or directory".
        smoke_curl GET "/api/jobs/${job_id}" >/dev/null
        if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
            return 1
        fi
        local status
        status=$(jq -r '.status // .job.status // "?"' "$SMOKE_LAST_BODY")
        SMOKE_LAST_STATUS="$status"
        case "$status" in
            completed|SUCCEEDED|failed|FAILED|cancelled|dead_letter) return 0 ;;
        esac
        sleep "$SMOKE_POLL_INTERVAL_SECONDS"
    done
    return 124
}

# Assertion helpers — return non-zero on failure so callers can collect a count.
smoke_assert_eq() {
    local expected="$1"; local actual="$2"; local label="$3"
    if [[ "$expected" != "$actual" ]]; then
        printf '%sFAIL: %s — expected [%s], got [%s]%s\n' \
            "$RED" "$label" "$expected" "$actual" "$RESET" >&2
        return 1
    fi
}

smoke_assert_http_2xx() {
    local label="$1"
    if [[ ! "$SMOKE_LAST_HTTP" =~ ^2[0-9][0-9]$ ]]; then
        printf '%sFAIL: %s — HTTP %s (expected 2xx)%s\n' \
            "$RED" "$label" "$SMOKE_LAST_HTTP" "$RESET" >&2
        if [[ -s "$SMOKE_LAST_BODY" ]]; then
            smoke_echo_safe "$(head -c 400 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        fi
        return 1
    fi
}

# ── Outbox chain verification ────────────────────────────────────────────
# smoke_outbox_chain_verify — classified per-clip outbox chain probe.
#
# Runs a GROUP BY query on outbox_events for the given clip IDs and
# classifies each clip as COMPLETED / PENDING / DEAD_LETTER / SUPERSEDED /
# MISSING. The classification uses the most severe event status present
# for that aggregate_id:
#
#   DEAD_LETTER  ≥1 dead_letter event (inspection required)
#   COMPLETED    ≥1 completed event (chain healthy)
#   SUPERSEDED   ≥1 superseded event AND zero completed (waiting on reindex)
#   PENDING      ≥1 pending event AND zero terminal (dispatcher not yet run)
#   MISSING      zero outbox_events rows (register-batch may have failed)
#
# Outputs a header-column table + summary line. Returns 0 when every clip
# is COMPLETED (chain fully healthy). Returns 1 when any clip has
# DEAD_LETTER, MISSING, or is not yet COMPLETED (PENDING/SUPERSEDED).
# Returns 2 on setup error.
#
# Usage:
#   smoke_outbox_chain_verify <db_path> <clip_ids_file>
#   if ! smoke_outbox_chain_verify "$SMOKE_DB" "$clip_ids_file"; then
#       fail_count=$((fail_count + 1))
#   fi
smoke_outbox_chain_verify() {
    local db_path="$1"
    local clip_ids_file="$2"

    if [[ ! -f "$db_path" ]]; then
        printf '%sFAIL: outbox chain verify — DB %s not found%s\n' \
            "$RED" "$db_path" "$RESET" >&2
        return 2
    fi
    if [[ ! -s "$clip_ids_file" ]]; then
        printf '%sFAIL: outbox chain verify — no clip IDs in %s%s\n' \
            "$RED" "$clip_ids_file" "$RESET" >&2
        return 2
    fi

    # Build quoted IN-clause from the newline-separated clip_ids_file.
    local in_list
    in_list=$(awk 'BEGIN{ORS=","; q=sprintf("%c",39)}
                  {printf q "%s" q, $0}' "$clip_ids_file" | sed 's/,$//')

    # Per-clip classification: GROUP BY aggregate_id with CASE over status
    # counts. MISSING clips (no outbox row) are detected by comparing the
    # result row count with the input clip count in bash after the query.
    local chain_sql="SELECT
    aggregate_id AS clip_id,
    CASE
        WHEN SUM(CASE WHEN status='dead_letter' THEN 1 ELSE 0 END) > 0 THEN 'DEAD_LETTER'
        WHEN SUM(CASE WHEN status='completed'   THEN 1 ELSE 0 END) > 0 THEN 'COMPLETED'
        WHEN SUM(CASE WHEN status='superseded'  THEN 1 ELSE 0 END) > 0 THEN 'SUPERSEDED'
        WHEN SUM(CASE WHEN status='pending'     THEN 1 ELSE 0 END) > 0 THEN 'PENDING'
        ELSE 'UNKNOWN'
    END AS chain_status,
    SUM(CASE WHEN status='completed'   THEN 1 ELSE 0 END) AS completed,
    SUM(CASE WHEN status='superseded'  THEN 1 ELSE 0 END) AS superseded,
    SUM(CASE WHEN status='pending'     THEN 1 ELSE 0 END) AS pending,
    SUM(CASE WHEN status='dead_letter' THEN 1 ELSE 0 END) AS dead_letter,
    COUNT(*) AS total_events,
    MAX(created_at) AS last_event_at,
    substr(COALESCE(
        (SELECT error FROM outbox_events oe2
         WHERE oe2.aggregate_id = oe.aggregate_id
           AND oe2.event_type = 'asset.index.requested'
           AND oe2.error IS NOT NULL AND oe2.error != ''
         ORDER BY oe2.created_at DESC LIMIT 1), ''
    ), 1, 80) AS last_error
FROM outbox_events oe
WHERE event_type = 'asset.index.requested'
  AND aggregate_id IN (${in_list})
GROUP BY aggregate_id
ORDER BY aggregate_id;"

    printf '\n  %s--- outbox chain classification ---%s\n' "$DIM" "$RESET"

    # Run query ONCE, save to temp file for display + parsing.
    local chain_out="$WORK_DIR/outbox_chain.txt"
    sqlite3 -header -column "$db_path" "$chain_sql" > "$chain_out" 2>&1
    cat "$chain_out"

    # Parse counts from saved output (avoids redundant sqlite3 invocations).
    # Column 3 is chain_status in -header -column format.
    local completed_count dead_count rows_returned total_input
    completed_count=$(awk 'NR>=3 && $3 == "COMPLETED" {n++} END{print n+0}' "$chain_out")
    dead_count=$(awk 'NR>=3 && $3 == "DEAD_LETTER" {n++} END{print n+0}' "$chain_out")
    rows_returned=$(awk 'NR>=3 && $1 != "" {n++} END{print n+0}' "$chain_out")
    total_input=$(wc -l < "$clip_ids_file" | tr -d ' ')
    local missing_count=$(( total_input - rows_returned ))
    (( missing_count < 0 )) && missing_count=0

    printf '\n  chain summary: COMPLETED=%s  DEAD_LETTER=%s  MISSING=%s  input=%s\n' \
        "$completed_count" "$dead_count" "$missing_count" "$total_input"

    local fail=0
    if (( dead_count > 0 )); then
        printf '%sFAIL: %s clip(s) have dead_letter outbox events — inspection required%s\n' \
            "$RED" "$dead_count" "$RESET" >&2
        fail=1
    fi
    if (( missing_count > 0 )); then
        printf '%sFAIL: %s clip(s) have NO outbox event (MISSING) — register-batch may have failed%s\n' \
            "$RED" "$missing_count" "$RESET" >&2
        fail=1
    fi
    if (( completed_count < total_input )); then
        local non_terminal=$(( total_input - completed_count - missing_count ))
        printf '%sWARN: %s clip(s) not yet COMPLETED (PENDING/SUPERSEDED) — outbox lagging%s\n' \
            "$YELLOW" "$non_terminal" "$RESET" >&2
        fail=1
    fi
    if (( fail == 0 )); then
        printf '%soutbox chain OK (%s/%s clips COMPLETED)%s\n' \
            "$GREEN" "$completed_count" "$total_input" "$RESET"
    fi
    return $fail
}

# ── Domain-portable helpers — generic across PipelineGen ops batteries ──
# Naming rule (July 2026 DoD refactor): `smoke_*` for infrastructure helpers
# (HTTP, SQLite, ffprobe, jq-assert, dry-run); `velox_*` for PipelineGen
# domain assertions live in tests/operational/lib/velox_domain.sh. The two
# libraries are independent sources — callers `source` both.

# ── HTTP call wrapper that preserves a caller-supplied output file ──────
# smoke_http_call METHOD PATH OUT [DATA]
#   METHOD  : GET / POST / PUT / DELETE
#   PATH    : path appended to http://${SMOKE_API_BASE}
#   OUT     : absolute path of the response body file (always written)
#   DATA    : optional inline JSON body (used when METHOD != GET)
# Returns the HTTP code on stdout. The OUT file is the canonical handoff:
# if the caller wraps this helper in `$(...)`, the smoke_curl side-effects
# (SMOKE_LAST_HTTP / SMOKE_LAST_BODY env exports) are LOST to the parent
# shell — always read OUT to inspect the body in those paths.
smoke_http_call() {
    local method="$1" path="$2" out="$3" data="${4:-}"
    [[ -n "$out" ]] || { printf '0\n'; return 1; }
    local args=()
    if [[ -n "$data" ]]; then
        args+=( -d "$data" )
    fi
    local code
    code=$(smoke_curl "$method" "$path" "${args[@]}" 2>/dev/null)
    if [[ -n "$SMOKE_LAST_BODY" && -s "$SMOKE_LAST_BODY" && "$SMOKE_LAST_BODY" != "$out" ]]; then
        cp "$SMOKE_LAST_BODY" "$out" 2>/dev/null || true
    fi
    printf '%s' "$code"
}

# ── Read-only SQLite query with optional JSON output ──────────────────
# smoke_sqlite_query DB_PATH [-json] [--] QUERY
# Examples:
#   smoke_sqlite_query "$DB" "SELECT COUNT(*) FROM media_assets"
#   smoke_sqlite_query "$DB" -json "SELECT id,file_hash FROM media_assets"
# Returns 0 on success, 1 on missing db / sqlite error. Output is on stdout.
smoke_sqlite_query() {
    local db="" fmt="" q=""
    while (( $# > 0 )); do
        case "$1" in
            -json|--json) fmt="-json" ;;
            --) shift; q="$1"; break ;;
            -*) ;; # ignore unknown flag for forward-compat
            *)  if [[ -z "$db" ]]; then db="$1"; else q="$1"; fi ;;
        esac
        shift
    done
    [[ -n "${db:-}" ]] || return 1
    [[ -f "$db" ]] || { printf '\n'; return 1; }
    [[ -n "$q" ]] || { printf '\n'; return 1; }
    sqlite3 -readonly $fmt "$db" "$q" 2>/dev/null
}

# ── ffprobe structural check on a local media file ───────────────────
# smoke_ffprobe_check FILE [MIN_DURATION_SECONDS]
# PASS if: file exists; ffprobe produced JSON; duration >= MIN_DURATION_SECONDS;
#          has at least one video stream with width>0 and height>0.
# Returns 0 on PASS, 1 on FAIL. Logs are written through the battery's
# log_pass/log_fail; this helper stays pure (no stdout/stderr noise).
smoke_ffprobe_check() {
    local path="$1" min_dur="${2:-0}"
    [[ -f "$path" ]] || return 1
    command -v ffprobe >/dev/null 2>&1 || return 1
    local probe_json
    probe_json=$(ffprobe -v error -show_entries format=duration,size \
        -show_entries stream=codec_type,codec_name,width,height \
        -of json "$path" 2>/dev/null || true)
    [[ -n "$probe_json" ]] || return 1
    jq -e --argjson min "$min_dur" '
        (.format.duration // 0 | tonumber) >= $min
        and (.format.size // 0 | tonumber) > 0
        and ([.streams[]? | select(.codec_type=="video")
              | select((.width // 0 | tonumber) > 0
                       and (.height // 0 | tonumber) > 0)] | length) >= 1
    ' <<<"$probe_json" >/dev/null 2>&1
}

# ── Dry-run announcement gate ─────────────────────────────────────────
# smoke_dry_run DESCRIPTION
# If SMOKE_DRY_RUN=1 (or --dry was passed): prints DESCRIPTION on stdoud and
# returns 0. Otherwise: returns 1 so the caller can short-circuit.
# Used by every battery's `main` to early-exit before any state-mutating
# HTTP / SQLite / Drive call fires.
smoke_dry_run() {
    local desc="$1"
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        printf '%s\n' "$desc"
        return 0
    fi
    return 1
}
