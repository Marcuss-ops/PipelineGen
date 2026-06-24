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
    if [[ -n "${WORK_DIR:-}" && -d "$WORK_DIR" ]]; then
        rm -rf "$WORK_DIR"
    fi
}

# ── Effective API base + tunables ─────────────────────────────────────────
SMOKE_API_BASE="${API_BASE:-127.0.0.1:${VELOX_PORT:-8080}}"
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-180}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-120}"
SMOKE_POLL_INTERVAL_SECONDS="${SMOKE_POLL_INTERVAL_SECONDS:-2}"
SMOKE_HTTP_TIMEOUT_SECONDS="${SMOKE_HTTP_TIMEOUT_SECONDS:-8}"

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
    if [[ -n "${VELOX_ADMIN_TOKEN:-}" ]]; then
        printf '%s' "$VELOX_ADMIN_TOKEN"
        return 0
    fi
    if [[ -n "${TOKEN_FILE:-}" && -f "${TOKEN_FILE}" ]]; then
        local token
        token=$(grep -E '^VELOX_ADMIN_TOKEN=' "$TOKEN_FILE" | head -1 | cut -d= -f2- ||
            true)
        if [[ -n "$token" ]]; then
            printf '%s' "$token"
            return 0
        fi
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

# curl wrapper — adds the bearer token silently (never via -H printed by shell).
# Always captures the body in $WORK_DIR/last.body and stores the HTTP code in $SMOKE_LAST_HTTP.
# Diagnostics are routed through smoke_log_response (redacted) when SMOKE_LOG_DIR is set.
smoke_curl() {
    local method="$1"; shift
    local url_path="$1"; shift
    local out_file="$WORK_DIR/last.body"
    local code
    code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -X "$method" \
        -o "$out_file" -w '%{http_code}' \
        -H "Authorization: Bearer $SMOKE_TOKEN" \
        -H 'Content-Type: application/json' \
        "$@" \
        "http://${SMOKE_API_BASE}${url_path}")
    export SMOKE_LAST_HTTP="$code"
    export SMOKE_LAST_BODY="$out_file"
    printf '%s' "$code"
}

# Poll /api/jobs/{id}/full until the job reaches a terminal status.
# Terminal statuses: completed, failed, cancelled, dead_letter.
# Returns 0 on terminal state, 124 on timeout.
smoke_poll_terminal() {
    local job_id="$1"
    SMOKE_LAST_STATUS=""
    local deadline=$(( $(date +%s) + SMOKE_POLL_TIMEOUT_SECONDS ))
    while (( $(date +%s) < deadline )); do
        smoke_wallclock_check
        local code
        code=$(smoke_curl GET "/api/jobs/${job_id}/full")
        if [[ "$code" != "200" ]]; then
            return 1
        fi
        local status
        status=$(jq -r '.status // "?"' "$SMOKE_LAST_BODY")
        SMOKE_LAST_STATUS="$status"
        case "$status" in
            completed|failed|cancelled|dead_letter) return 0 ;;
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
