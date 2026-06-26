#!/usr/bin/env bash
#
# marker_audit.sh - PipelineGen marker-injection diagnostic
#
# Usage:
#   ./marker_audit.sh                  # run a fresh audit
#   ./marker_audit.sh --dry            # print the would-be payload and exit
#   ./marker_audit.sh --quiet          # only print OK/FAIL line (CI-friendly)
#   NO_COLOR=1 ./marker_audit.sh       # disable ANSI colors
#
# Env overrides: API_BASE, TOKEN_FILE, MODEL, TONE, TOPIC, TITLE,
#                TARGET_WORDS, POLL_INTERVAL, POLL_TIMEOUT.
#
# Exit codes:
#   0  audit passed (5 [Clip: <id>] markers present, all clip_ids covered)
#   1  audit failed (missing/extra markers, or upstream error)

set -euo pipefail

# ── Defence-in-depth: tokens written to /tmp should not be world-readable
umask 077

# ── Per-run work dir (race-safe with concurrent operators) ─────────────
WORK_DIR=$(mktemp -d /tmp/marker_audit.XXXXXX)
trap 'rm -rf "$WORK_DIR"' EXIT INT TERM

POST_FILE="$WORK_DIR/post.json"
CURR_FILE="$WORK_DIR/curr.json"
HELPER="$WORK_DIR/helper.py"

# ── Config (overridable via env) ───────────────────────────────────────
# Default API base host:port. 8080 matches the canonical server
# default (`internal/platform/config/types.go` `Server.Port`).
# Override via `API_BASE=127.0.0.1:NNNN ./marker_audit.sh` — keep the
# `host:port` (NOT `http://host:port`) shape since the script appends
# the rest of the URL itself.
API_BASE="${API_BASE:-127.0.0.1:8080}"
TOKEN_FILE="${TOKEN_FILE:-/tmp/velox.env}"
TOPIC="${TOPIC:-Jackie Chan Cinematic Stunt Legend Speaks}"
TITLE="${TITLE:-Jackie Chan The Cinematic Stunt Legend}"
TONE="${TONE:-documentary}"
MODEL="${MODEL:-gemma2:2b}"
TARGET_WORDS="${TARGET_WORDS:-600}"
POLL_INTERVAL="${POLL_INTERVAL:-8}"
POLL_TIMEOUT="${POLL_TIMEOUT:-600}"

# Five well-known Jackie Chan clip IDs (Google Drive). These clips were
# chosen as the canonical smoke-test set because each has a transcript
# >150 words and the IDs were first used as the B/F filter regression
# fixture during the marker-injection rollout. Single source of truth:
# the Python helper reuses $EXPECTED_CLIP_IDS so the two copies can't
# drift apart.
CLIP_IDS='["1XcdSo0so-ur0-cITwWNeQyP9Q-n_GKvv","1o2-jTsU4i09zz0Qdo8lKC17oX3DHaNwo","1cWTfJJf9wCvO7CrMmCHqxdhq0MagiO50","1tf-FOsC0rxIjhjScLQYl9evzNyOIDd90","1jHqyBgUlTjAYyhsFosa0I3gmHmedsUYK"]'
export EXPECTED_CLIP_IDS="$CLIP_IDS"

# ── CLI flags ──────────────────────────────────────────────────────────
QUIET=0
for arg in "$@"; do
    case "$arg" in
        --dry)   DRY_RUN=1 ;;
        --quiet) QUIET=1 ;;
        -h|--help)
            sed -n '2,18p' "$0"; exit 0 ;;
        *) echo "unknown flag: $arg" >&2; exit 2 ;;
    esac
done

# ── Color setup (honour NO_COLOR + non-tty) ───────────────────────────
if [[ -t 1 && "${NO_COLOR:-0}" == "0" ]]; then
    RED=$(tput setaf 1 2>/dev/null || true)
    GREEN=$(tput setaf 2 2>/dev/null || true)
    YELLOW=$(tput setaf 3 2>/dev/null || true)
    CYAN=$(tput setaf 6 2>/dev/null || true)
    DIM=$(tput dim 2>/dev/null || true)
    RESET=$(tput sgr0 2>/dev/null || true)
else
    RED=""; GREEN=""; YELLOW=""; CYAN=""; DIM=""; RESET=""
fi
export RED GREEN YELLOW CYAN DIM RESET

print_label() { [[ "$QUIET" == "0" ]] && printf '\033[?25l'; :; }  # hide cursor while polling
restore_cursor() { [[ "$QUIET" == "0" ]] && printf '\033[?25h'; :; }
trap restore_cursor EXIT INT TERM

# ── Header (skipped in --quiet) ───────────────────────────────────────
if [[ "$QUIET" == "0" ]]; then
    cat << 'EOF'

  ┌──────────────────────────────────────────────┐
  │  PipelineGen marker-injection audit (PR4)   │
  └──────────────────────────────────────────────┘

EOF
fi

# ── Dry-run short-circuit ─────────────────────────────────────────────
if [[ "${DRY_RUN:-0}" == "1" ]]; then
    cat <<EOF
DRY RUN payload that would be POSTed to /api/script/generate-from-clips:

{
  "clip_ids": $CLIP_IDS,
  "topic": "$TOPIC",
  "title": "$TITLE",
  "tone": "$TONE",
  "model": "$MODEL",
  "target_words": $TARGET_WORDS,
  "force_refresh": true,
  "min_quality_score": 0,
  "min_transcript_words": 100,
  "generate_scene_images": false,
  "extract_entities": false,
  "generate_metadata": false
}
EOF
    exit 0
fi

# ── Token resolution ──────────────────────────────────────────────────
if [[ ! -f "$TOKEN_FILE" ]]; then
    echo -e "${RED}FAIL:${RESET} token file missing at $TOKEN_FILE"
    echo "       override via TOKEN_FILE=/path/to/velox.env $0"
    exit 1
fi
TOKEN=$(grep -E '^VELOX_ADMIN_TOKEN=' "$TOKEN_FILE" | head -1 | cut -d= -f2-)
if [[ -z "$TOKEN" ]]; then
    echo -e "${RED}FAIL:${RESET} VELOX_ADMIN_TOKEN not found in $TOKEN_FILE"
    exit 1
fi

# ── 1. Dispatch ────────────────────────────────────────────────────────
if [[ "$QUIET" == "0" ]]; then
    echo -e "${CYAN}===== 1. Dispatch job =====${RESET}"
    echo "endpoint: POST http://$API_BASE/api/script/generate-from-clips"
    echo "model:    $MODEL"
    echo "target:   $TARGET_WORDS words"
fi

PAYLOAD=$(cat <<EOF
{
  "clip_ids": $CLIP_IDS,
  "topic": "$TOPIC",
  "title": "$TITLE",
  "tone": "$TONE",
  "model": "$MODEL",
  "target_words": $TARGET_WORDS,
  "min_quality_score": 0,
  "min_transcript_words": 100,
  "transcript_policy": "auto",
  "ordering_strategy": "auto",
  "save_to_db": true,
  "force_refresh": true,
  "generate_scene_images": false,
  "extract_entities": false,
  "generate_metadata": false
}
EOF
)

HTTP=$(curl -s --max-time 30 -o "$POST_FILE" \
    -w '%{http_code}' \
    -X POST "http://$API_BASE/api/script/generate-from-clips" \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d "$PAYLOAD")

if [[ "$HTTP" != "200" && "$HTTP" != "202" ]]; then
    echo -e "${RED}FAIL:${RESET} job dispatch returned HTTP $HTTP"
    head -c 300 "$POST_FILE"
    echo
    exit 1
fi

JOB_ID=$(python3 -c "import json; print(json.load(open('$POST_FILE')).get('job_id',''))")
if [[ -z "$JOB_ID" ]]; then
    echo -e "${RED}FAIL:${RESET} no job_id in response"
    cat "$POST_FILE"
    exit 1
fi
[[ "$QUIET" == "0" ]] && echo -e "job_id:  ${YELLOW}$JOB_ID${RESET}"

# ── 2. Poll until terminal status ──────────────────────────────────────
if [[ "$QUIET" == "0" ]]; then
    echo
    echo -e "${CYAN}===== 2. Poll until terminal =====${RESET}"
fi
ELAPSED=0
LAST_STATUS=""
while (( ELAPSED < POLL_TIMEOUT )); do
    HTTP=$(curl -s --max-time 8 -o "$CURR_FILE" -w '%{http_code}' \
        -H "Authorization: Bearer $TOKEN" \
        "http://$API_BASE/api/jobs/$JOB_ID/full")
    if [[ "$HTTP" != "200" ]]; then
        echo -e "${RED}FAIL:${RESET} poll GET returned HTTP $HTTP"
        exit 1
    fi
    STATUS=$(python3 -c "import json; print(json.load(open('$CURR_FILE')).get('status','?'))")
    PROGRESS=$(python3 -c "import json; print(json.load(open('$CURR_FILE')).get('progress','?'))")
    if [[ "$QUIET" == "0" ]]; then
        if [[ "$STATUS" != "$LAST_STATUS" ]]; then
            echo "[$(date +%H:%M:%S)] iter=$((ELAPSED/POLL_INTERVAL + 1)) status=$STATUS progress=$PROGRESS%"
            LAST_STATUS="$STATUS"
        else
            printf '.'
        fi
    fi
    if [[ "$STATUS" == "completed" || "$STATUS" == "failed" || "$STATUS" == "cancelled" || "$STATUS" == "dead_letter" ]]; then
        [[ "$QUIET" == "0" ]] && echo
        break
    fi
    sleep "$POLL_INTERVAL"
    ELAPSED=$(( ELAPSED + POLL_INTERVAL ))
done

if (( ELAPSED >= POLL_TIMEOUT )); then
    echo -e "${RED}FAIL:${RESET} polling timed out after ${POLL_TIMEOUT}s"
    exit 1
fi

# ── 3. Marker audit ────────────────────────────────────────────────────
if [[ "$QUIET" == "0" ]]; then
    echo
    echo -e "${CYAN}===== 3. Marker audit =====${RESET}"
fi

cat > "$HELPER" << 'PYEOF'
import sys, json, os, re

job = json.load(sys.stdin)
status = job.get("status", "?")
result = job.get("result") or {}
script = result.get("script") or ""
word_count = result.get("word_count", 0)
clip_scene_count = len(result.get("clip_scenes") or [])
cache_status = result.get("cache_status", "?")
err = job.get("error") or ""

R  = os.environ.get("RED", "")
G  = os.environ.get("GREEN", "")
Y  = os.environ.get("YELLOW", "")
C  = os.environ.get("CYAN", "")
DI = os.environ.get("DIM", "")
RS = os.environ.get("RESET", "")
QUIET = os.environ.get("QUIET", "0") == "1"

def emit(line=""):
    if not QUIET:
        print(line)

emit(f"  job status:      {C}{status}{RS}")
emit(f"  cache_status:    {cache_status}")
emit(f"  word_count:      {word_count}")
emit(f"  script_chars:    {len(script)}")
emit(f"  clip_scenes:     {clip_scene_count}")
if err and not QUIET:
    emit(f"  {R}job error:{RS} {err[:160]}")

emit()
emit("  markers found in result.script:")
matches = list(re.finditer(r'\[Clip:\s*([^\]\s]+)\s*\]', script))
for i, m in enumerate(matches, 1):
    cid = m.group(1).strip()
    emit(f"    [{i}] @ char {m.start():4d}: {Y}{cid}{RS}")

n_clip = len(matches)
n_narr = len(re.findall(r'\[Narration:\s*[a-z_]+\s*\]', script))

emit()
emit(f"  [Clip: count]      = {n_clip}")
emit(f"  [Narration: count] = {n_narr}")

if not QUIET:
    emit()
    emit(f"  {C}--- result.script (first 800 chars) ---{RS}")
    snippet = script[:800]
    emit('    ' + snippet.replace('\n', '\n    '))
    if len(script) > 800:
        emit('    ...')
    emit(f"  {C}---------------------------------------{RS}")

# Single source of truth comes in via env. This is the same JSON array
# the Bash side reads out of CLIP_IDS, so the two can never drift apart.
try:
    expected = set(json.loads(os.environ["EXPECTED_CLIP_IDS"]))
except (KeyError, json.JSONDecodeError) as e:
    sys.stderr.write(f"helper: invalid EXPECTED_CLIP_IDS env var: {e}\n")
    sys.exit(2)
found_ids = set(m.group(1).strip() for m in matches)
missing = expected - found_ids

if QUIET:
    if status != "completed":
        print(f"FAIL status={status}")
        sys.exit(1)
    if missing:
        print(f"FAIL missing={','.join(sorted(missing))}")
        sys.exit(1)
    if n_clip < len(expected):
        print(f"FAIL markers={n_clip} expected={len(expected)}")
        sys.exit(1)
    print("OK")
    sys.exit(0)

emit()
if status != "completed":
    emit(f"  {R}FAIL:{RS} job ended with status={status} (not completed)")
    sys.exit(1)
if missing:
    emit(f"  {R}FAIL:{RS} expected {len(expected)} markers covering {len(expected)} clip_ids; "
          f"got {n_clip}, missing {len(missing)}: {', '.join(sorted(missing))}")
    sys.exit(1)
if n_clip < len(expected):
    emit(f"  {R}FAIL:{RS} only {n_clip} [Clip:] markers (expected {len(expected)})")
    sys.exit(1)

emit(f"  {G}OK — marker injection working{RS}")
emit(f"  all {len(expected)} expected clip_ids visible as [Clip:] markers in script text")
sys.exit(0)
PYEOF

export QUIET
python3 "$HELPER" < "$CURR_FILE"
EXIT=$?

# Final note (skip in --quiet since CI parses the OK/FAIL line)
if [[ "$QUIET" == "0" && "$EXIT" == "0" && "${word_count:-0}" -lt 100 ]]; then
    echo
    echo -e "${DIM}hint: body has <100 words — likely gemma2:2b cold-start. Check \`data/logs/pipelinegen.log\` for the 'marker normalization applied' line; try MODEL=qwen2.5:7b for richer prose.${RESET}"
fi
if [[ "$QUIET" == "0" && "$EXIT" == "0" ]]; then
    echo
    echo -e "${DIM}deeper investigation (server logs, prompt fingerprint, cache):${RESET}"
    echo -e "${DIM}  tail -n 200 data/logs/pipelinegen.log | grep '$JOB_ID\\|marker normalization applied'${RESET}"
fi

exit "$EXIT"
