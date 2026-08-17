#!/usr/bin/env bash
# maya_image_cache_certification.sh — Maya internet_images COLD→WARM cache certification.
#
# Certifies the architectural rule that an internet image is materialized
# exactly ONCE and that a subsequent warm job reuses the persisted asset
# without calling the internet_images provider again:
#
#   COLD  → entities → image_queries → internet_images → download → SHA256
#         → Drive → SQLite → Qdrant/outbox → scene binding
#   WARM  → cache/DB lookup → same asset_id → same Drive asset
#         → zero provider calls, zero downloads, zero duplicate rows
#
# Usage:
#   bash maya_image_cache_certification.sh [--probe|--probe-db]
#
# Column semantics (media registry SSOT — do not regress to legacy columns):
#   Web images are selected by the media-registry TAXONOMY — source_type='web'
#   (+ asset_kind='web_image') — NOT by the legacy `provider` column. The
#   byte-identity digest is file_hash (64-hex SHA-256). content_sha256 is the
#   CAS digest (backfilled to equal file_hash); it is not a discovery
#   predicate and is empty/UNKNOWN on pre-CAS rows.
#   The /metrics counter still uses the Prometheus label
#   provider="internet_images" (that is how metrics_vidrush.go emits it) — a
#   different surface from the SQLite taxonomy filter above.
#
# Environment (see lib/common.sh for the shared vars):
#   METRICS_URL           (default http://${SMOKE_API_BASE}/metrics)
#   METRICS_AUTH_TOKEN    bearer token for /metrics — REQUIRED and fail-closed.
#                         The admin token (SMOKE_TOKEN / VELOX_ADMIN_TOKEN) is
#                         NOT accepted by /metrics.
#   DB_PATH               (default data/media/media.db.sqlite)

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)

# Intercept the read-only probe flags BEFORE common.sh's flag parser, which
# only accepts --dry / -h / --help and hard-exits on any other argument.
PROBE_MODE=""
case "${1:-}" in
    --probe)    PROBE_MODE="metrics" ; set -- ;;
    --probe-db) PROBE_MODE="db"      ; set -- ;;
esac

SMOKE_TIMEOUT_SECONDS="${MAYA_IMG_CERT_TIMEOUT_SECONDS:-1800}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-600}"
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
smoke_require curl jq sqlite3

DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
METRICS_URL="${METRICS_URL:-http://${SMOKE_API_BASE}/metrics}"

# ── Metrics helpers ───────────────────────────────────────────────────────
# /metrics is a separate fail-closed surface: the admin token is NOT accepted
# there. The provider request counter is read with METRICS_AUTH_TOKEN. A
# non-200, an empty body, or a missing token is a hard failure — never a
# silent no-op.
metrics_text() {
    if [[ -z "${METRICS_AUTH_TOKEN:-}" ]]; then
        printf '%ssetup error: METRICS_AUTH_TOKEN env var unset (needed for /metrics fail-closed)%s\n' \
            "$RED" "$RESET" >&2
        return 2
    fi
    curl -fsS --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -H "Authorization: Bearer $METRICS_AUTH_TOKEN" \
        "$METRICS_URL" 2>/dev/null || {
            printf '%sFAIL: /metrics not reachable (fail-closed)%s\n' "$RED" "$RESET" >&2
            return 1
        }
}

# internet_requests — current value of vidrush_provider_requests_total for the
# internet_images provider, or "MISSING" if the series has not been observed
# yet (label vectors are emitted only after the first observation).
internet_requests() {
    metrics_text | awk \
        '$1 ~ /^vidrush_provider_requests_total\{/ && $1 ~ /provider="internet_images"/ {print $2; found=1} END {if (!found) print "MISSING"}' | tail -1
}

# assert_no_internet_requests BEFORE AFTER [LABEL]
# The strongest cache-certification gate: a warm run must not move the
# provider counter at all (delta == 0).
assert_no_internet_requests() {
    local before="$1" after="$2" label="${3:-warm}"
    if [[ "$before" == "MISSING" || "$after" == "MISSING" ]]; then
        printf '  %sFAIL%s %s: internet_images counter MISSING (before=%s after=%s)\n' \
            "$RED" "$RESET" "$label" "$before" "$after" >&2
        return 1
    fi
    local delta=$(( after - before ))
    if [[ "$delta" == "0" ]]; then
        printf '  %sPASS%s %s: internet_images provider NOT called (delta=0, counter=%s)\n' \
            "$GREEN" "$RESET" "$label" "$after"
        return 0
    fi
    printf '  %sFAIL%s %s: internet_images provider was called (delta=%d, counter %s → %s)\n' \
        "$RED" "$RESET" "$label" "$delta" "$before" "$after" >&2
    return 1
}

# ── DB helpers (media registry taxonomy) ───────────────────────────────────
# source_type='web' + asset_kind='web_image' is the canonical web-image
# selection; file_hash is the byte-identity digest. All queries are read-only
# (-readonly) so the certification never mutates the registry.

web_images_count() {
    sqlite3 -readonly "$DB_PATH" \
        "SELECT COUNT(*) FROM media_assets WHERE source_type='web' AND asset_kind='web_image';"
}

# web_image_ids / web_image_hashes — COLD/WARM comparison snapshots.
web_image_ids() {
    sqlite3 -readonly "$DB_PATH" \
        "SELECT id FROM media_assets WHERE source_type='web' AND asset_kind='web_image' ORDER BY rowid DESC;"
}

web_image_hashes() {
    sqlite3 -readonly "$DB_PATH" \
        "SELECT file_hash FROM media_assets WHERE source_type='web' AND asset_kind='web_image' AND file_hash != '' ORDER BY rowid DESC;"
}

# duplicate_web_image_hashes — content-level dedup: byte-identical images must
# not exist as two separate media_assets rows. Zero rows = clean.
duplicate_web_image_hashes() {
    sqlite3 -readonly "$DB_PATH" \
        "SELECT file_hash, COUNT(*) AS copies FROM media_assets WHERE source_type='web' AND asset_kind='web_image' AND file_hash != '' GROUP BY file_hash HAVING COUNT(*) > 1 ORDER BY copies DESC;"
}

# ── Probe mode: verify the counter surface only (no job is dispatched) ────
if [[ "$PROBE_MODE" == "metrics" ]]; then
    smoke_log_section "internet_images metrics probe"
    BEFORE=$(internet_requests)
    printf '  vidrush_provider_requests_total{provider="internet_images"} = %s\n' "$BEFORE"
    if [[ "$BEFORE" == "MISSING" ]]; then
        printf '  %sWARN%s counter not observed yet (label vector appears after the first provider call)\n' \
            "$YELLOW" "$RESET"
        exit 0
    fi
    printf '  %sOK%s counter readable\n' "$GREEN" "$RESET"
    exit 0
fi

# ── DB probe mode: verify the taxonomy surface only (read-only) ───────────
if [[ "$PROBE_MODE" == "db" ]]; then
    smoke_log_section "web-image DB taxonomy probe (source_type='web', file_hash)"
    [[ -f "$DB_PATH" ]] || { printf '  %sFAIL%s DB not found: %s\n' "$RED" "$RESET" "$DB_PATH" >&2; exit 1; }
    printf '  web images (source_type=%sweb%s) = %s\n' "$GREEN" "$RESET" "$(web_images_count)"
    dup_groups=$(duplicate_web_image_hashes | wc -l | tr -d ' ')
    printf '  duplicate file_hash groups = %s\n' "$dup_groups"
    if [[ "$dup_groups" == "0" ]]; then
        printf '  %sOK%s no byte-identical web-image duplicates\n' "$GREEN" "$RESET"
    else
        printf '  %sWARN%s %s duplicate file_hash group(s) present\n' "$YELLOW" "$RESET" "$dup_groups"
    fi
    exit 0
fi

cat <<'EOF'
The metrics counter and the DB taxonomy helpers are wired. The COLD/WARM
battery (dispatch, persistence, snapshot, warm replay, zero-call gate) is
added by the subsequent certification steps.

  --probe     verify /metrics is reachable and the provider counter is readable
  --probe-db  verify the media registry surface: web images counted via
              source_type='web', duplicate file_hash groups listed
EOF
exit 0
