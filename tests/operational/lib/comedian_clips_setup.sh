#!/usr/bin/env bash
# Source-only helpers for comedian_clips_setup.sh.
# shellcheck shell=bash
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo "[ERROR] comedian_clips_setup.sh must be sourced, not executed directly." >&2
    exit 1
fi

comedian_setup() {
ROOT_DIR=$(cd "$DIR/../.." && pwd)
SMOKE_DB="${SMOKE_DB:-$ROOT_DIR/data/media/media.db.sqlite}"

# ── Velox Master config ───────────────────────────────────────────────
VELOX_MASTER_URL="${VELOX_MASTER_URL:-http://127.0.0.1:8000}"
VELOX_M2M_TOKEN="${VELOX_M2M_TOKEN:-}"
VELOX_DESTINATION_ID="${VELOX_DESTINATION_ID:-comedy_test}"
VELOX_RENDER_POLL_TIMEOUT="${VELOX_RENDER_POLL_TIMEOUT:-1800}"
VELOX_RENDER_POLL_INTERVAL="${VELOX_RENDER_POLL_INTERVAL:-5}"

# ── 5 comedian clips from the production DB ───────────────────────────
CLIP_IDS=(
    "1ACocqdNciHEBScJ1-mTb9DOaPRyH4jZj"
    "1GJY5u0kE43t0YkGQ7LPhY8aXVqxYD74R"
    "1YiQb90UbNsCSlF_tg6kckKaePsQF9BgQ"
    "yt_ibPkLdbG4VU_150_210_v1"
    "14lC5FyiwBTD5QrCf4jnE08EsjPeyTYRV"
)

# ── Work dir ──────────────────────────────────────────────────────────
# Note: common.sh already creates WORK_DIR; override with a named one
# for cleaner artifact naming.
VEL_E2E_WORK=$(mktemp -d "/tmp/comedian-e2e.XXXXXX")
trap 'rm -rf "$VEL_E2E_WORK"' EXIT INT TERM

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,40p' "$0"; exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
    cat <<EOF
DRY RUN — Full comedian clips → Velox Master pipeline
PipelineGen: http://${SMOKE_API_BASE}
Velox Master: ${VELOX_MASTER_URL}
Clips: ${CLIP_IDS[*]}
Steps: generate → voiceover → subtitles → manifest → velox submit → poll → verify
EOF
    exit 0
fi

# ══════════════════════════════════════════════════════════════════════
}

comedian_preflight() {
# STEP 1: PREFLIGHT
# ══════════════════════════════════════════════════════════════════════
smoke_log_section "Step 1/11: Preflight checks"

# 1a. DB exists
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi

# 1b. Clips exist in DB
CLIP_OK=0
for i in "${!CLIP_IDS[@]}"; do
    clip_id="${CLIP_IDS[$i]}"
    row_count=$(sqlite3 "$SMOKE_DB" \
        "SELECT COUNT(*) FROM media_assets WHERE id='${clip_id}' AND lifecycle_state='ACTIVE';" 2>/dev/null || echo 0)
    if [[ "$row_count" == "0" ]]; then
        printf '%sWARN: clip %s not found in DB%s\n' "$YELLOW" "$clip_id" "$RESET"
    else
        CLIP_OK=$((CLIP_OK + 1))
        tracks=$(sqlite3 "$SMOKE_DB" \
            "SELECT COUNT(*) FROM asset_text_tracks WHERE asset_id='${clip_id}' AND text_content != '' AND status='READY';" 2>/dev/null || echo 0)
        printf '  clip=%s tracks=%s %sOK%s\n' "$clip_id" "$tracks" "$GREEN" "$RESET"
    fi
done
(( CLIP_OK == ${#CLIP_IDS[@]} )) || { printf '%ssetup error: need all %d clips in DB, got %d%s\n' "$RED" "${#CLIP_IDS[@]}" "$CLIP_OK" "$RESET" >&2; exit 2; }

# 1c. Velox Master health
printf '\n'
MASTER_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
    "${VELOX_MASTER_URL}/health/ready" 2>/dev/null || echo "000")
if [[ "$MASTER_HTTP" == "200" ]]; then
    printf '  Velox Master health: %sOK%s\n' "$GREEN" "$RESET"
else
    printf '%ssetup error: Velox Master at %s returned HTTP %s%s\n' \
        "$RED" "$VELOX_MASTER_URL" "$MASTER_HTTP" "$RESET" >&2
    exit 2
fi

# 1d. Worker connectivity (only if Master reachable)
SKIP_VELOX="${SKIP_VELOX:-0}"
WORKERS_AUTH_TOKEN="${VELOX_MASTER_ADMIN_TOKEN:-${VELOX_ADMIN_TOKEN:-$VELOX_M2M_TOKEN}}"
if [[ "$SKIP_VELOX" == "0" && -n "$WORKERS_AUTH_TOKEN" ]]; then
    WORKERS_BODY="${VEL_E2E_WORK}/velox_workers.json"
    WORKERS_HTTP=$(curl -s -o "$WORKERS_BODY" -w '%{http_code}' --max-time 10 \
        -H "Authorization: Bearer $WORKERS_AUTH_TOKEN" \
        "${VELOX_MASTER_URL}/api/v1/workers" 2>/dev/null || echo "000")
    if [[ "$WORKERS_HTTP" == "200" ]]; then
        CAPABLE=$(jq -r '[.workers[]? | select((.status|ascii_upcase)=="CONNECTED") | select(any(.executors[]?; (.id|startswith("scene.composite.v1")))) | .worker_id] | length' "$WORKERS_BODY" 2>/dev/null || echo 0)
        if (( CAPABLE > 0 )); then
            printf '  Velox workers: %s%d connected with scene.composite.v1%s\n' "$GREEN" "$CAPABLE" "$RESET"
        else
            printf '%ssetup error: no capable Velox workers advertising scene.composite.v1%s\n' "$RED" "$RESET" >&2
            exit 2
        fi
    else
        printf '%ssetup error: Velox workers API returned HTTP %s%s\n' "$RED" "$WORKERS_HTTP" "$RESET" >&2
        exit 2
    fi
elif [[ -z "$VELOX_M2M_TOKEN" ]]; then
    printf '%ssetup error: VELOX_M2M_TOKEN is required for Velox job submission%s\n' "$RED" "$RESET" >&2
    exit 2
fi

# ══════════════════════════════════════════════════════════════════════
}

