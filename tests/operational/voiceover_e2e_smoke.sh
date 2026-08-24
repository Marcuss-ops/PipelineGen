#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────
# voiceover_e2e_smoke.sh — Voiceover end-to-end smoke test
# ────────────────────────────────────────────────────────────────────
#
# Purpose: Verify the voiceover pipeline end-to-end against a live
# PipelineGen server. Mirrors the STK-E2E-* pattern (hermetic shell
# smoke, no extra deps, exit codes 0/1/2, verdict block, per-failure
# canonical PR-* mapping).
#
# Sections (per AGENTS.md godlike/07 NO-FAKE-AVAILABILITY):
#   1. Pre-flight — required tools, server, token, DB
#   2. TTS worker health — persistent or legacy fallback
#   3. Enqueue — canonical Plan V2 envelope (per the test fixtures in
#                internal/capabilities/scripts/jobs/generation_job_failures_test.go)
#   4. Poll — /api/jobs/{id}/full until terminal
#   5. 4-table check — voiceovers / upload_intents / outbox_events / media_assets
#   6. Verdict — PASS only if job=SUCCEEDED AND all 4 tables populated
#
# Exit codes:
#   0 = PASS (job SUCCEEDED + all 4 tables populated)
#   1 = FAIL (any canonical check failed; PR-* mapping printed)
#   2 = prereq missing (server down / token missing / DB missing)
#
# Overridable env vars:
#   BASE / ENV_FILE / DB_PATH / CORRELATION_ID / ITEM_ID / TOPIC / LANGUAGE
#
# Usage:
#   bash tests/operational/voiceover_e2e_smoke.sh
#   BASE=http://10.0.0.1:8000 bash tests/operational/voiceover_e2e_smoke.sh
# ────────────────────────────────────────────────────────────────────

set -euo pipefail

# ─── CONFIG ─────────────────────────────────────────────────────
BASE="${BASE:-http://127.0.0.1:8000}"
ENV_FILE="${ENV_FILE:-.env}"
DB_PATH="${DB_PATH:-data/media/media.db.sqlite}"
CORRELATION_ID="${CORRELATION_ID:-vo-e2e-smoke-$(date +%Y%m%d-%H%M%S)}"
ITEM_ID="${ITEM_ID:-vo-e2e-smoke-item}"
TOPIC="${TOPIC:-pugilato italiano}"
LANGUAGE="${LANGUAGE:-it-IT}"
POLL_ITERATIONS="${POLL_ITERATIONS:-80}"   # 80 × 3s = 4min max
POLL_SLEEP_S="${POLL_SLEEP_S:-3}"

# ─── COLORS ─────────────────────────────────────────────────────
RED=$'\033[0;31m'; GRN=$'\033[0;32m'; YEL=$'\033[1;33m'; BLU=$'\033[0;34m'; NC=$'\033[0m'

# ─── HELPERS ────────────────────────────────────────────────────
fail() { printf '%sFAIL%s %s\n' "$RED" "$NC" "$1" >&2; }
warn() { printf '%sWARN%s %s\n' "$YEL" "$NC" "$1" >&2; }
ok()   { printf '%sOK%s   %s\n' "$GRN" "$NC" "$1"; }
info() { printf '%sINFO%s %s\n' "$BLU" "$NC" "$1"; }
die()  { fail "$1"; exit "${2:-1}"; }

# ─── PREREQ ─────────────────────────────────────────────────────
echo "============================================"
echo "VOICEOVER E2E SMOKE — godlike/07 NO-FAKE-AVAILABILITY"
echo "============================================"
echo "BASE:        $BASE"
echo "ENV_FILE:    $ENV_FILE"
echo "DB_PATH:     $DB_PATH"
echo "CORRELATION: $CORRELATION_ID"
echo "ITEM_ID:     $ITEM_ID"
echo "TOPIC:       $TOPIC"
echo "LANGUAGE:    $LANGUAGE"
echo

echo "─── Pre-flight: required tools ───"
for cmd in curl jq sqlite3 python3; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    die "prereq missing: '$cmd' not in PATH" 2
  fi
done
ok "curl / jq / sqlite3 / python3 present"

echo "─── Pre-flight: server reachable ───"
HEALTH_CODE=$(curl -sS -m 5 -o /dev/null -w '%{http_code}' "$BASE/healthz" 2>/dev/null || echo "000")
if [ "$HEALTH_CODE" != "200" ]; then
  die "server unreachable on $BASE/healthz (HTTP $HEALTH_CODE)" 2
fi
ok "server up on $BASE (HTTP 200 on /healthz)"

echo "─── Pre-flight: VELOX_ADMIN_TOKEN ───"
if [ ! -f "$ENV_FILE" ]; then die ".env not found at $ENV_FILE" 2; fi
TOKEN=$(awk -F= '/^VELOX_ADMIN_TOKEN=/{gsub(/^["'"'"']|["'"'"']$/, "", $2); print $2; exit}' "$ENV_FILE")
if [ -z "$TOKEN" ]; then die "VELOX_ADMIN_TOKEN missing in $ENV_FILE" 2; fi
ok "VELOX_ADMIN_TOKEN loaded (len=${#TOKEN})"

echo "─── Pre-flight: DB ───"
if [ ! -f "$DB_PATH" ]; then die "DB not found at $DB_PATH" 2; fi
ok "DB exists ($DB_PATH)"

# ─── TTS WORKER HEALTH ─────────────────────────────────────────
echo
echo "─── TTS worker health (godlike/07 fail-closed) ───"
TTS_WORKER_PID=$(pgrep -f 'tts_edge_server\.py' 2>/dev/null | head -1 || true)
if [ -n "$TTS_WORKER_PID" ]; then
  ok "TTS persistent worker PID=$TTS_WORKER_PID (canonical path: scripts/bridges/tts_edge_server.py)"
else
  warn "TTS persistent worker NOT detected (pgrep tts_edge_server.py empty)"
  warn "canonical fallback: legacy spawn-per-call (processor.go:Generate legacy path)"
  if [ ! -f scripts/bridges/tts_edge.py ]; then
    die "TTS persistent worker absent AND legacy bridge missing at scripts/bridges/tts_edge.py" 2
  fi
  ok "legacy TTS bridge present (spawn-per-call fallback wired)"
fi

# ─── ENQUEUE canonical Plan V2 ─────────────────────────────────
echo
echo "─── Enqueue canonical Plan V2 ───"
ENVELOPE=$(jq -n \
  --arg cid "$CORRELATION_ID" \
  --arg iid "$ITEM_ID" \
  --arg lang "$LANGUAGE" \
  --arg topic "$TOPIC" \
  '{
    version: 2,
    preset: "custom",
    correlation_id: $cid,
    items: [{
      id: $iid,
      language: $lang,
      source: { type: "text", topic: $topic },
      output: {
        languages: [$lang],
      }
    }]
  }')

RESP_FILE=$(mktemp)
# Cleanup pattern (godlike/07 minimum-blast-radius):
#   PASS verdict  -> explicit `rm -f "$RESP_FILE"` (no forensics needed)
#   FAIL verdict  -> preserve $RESP_FILE path in the message (operator forensics)
#   SIGINT/SIGTERM -> rm the tempfile (hygiene; the verdict flow itself is unaffected)
# We do NOT trap EXIT — a single-file forensic artifact is easier to reason about
# than a trap that future FAIL branches might forget to disable.
trap 'rm -f "$RESP_FILE" 2>/dev/null || true' INT TERM

# Capture POST_TS BEFORE the curl POST (godlike/07 NO-FAKE-AVAILABILITY):
# the server can write rows with `created_at = now` during the POST itself;
# if we capture POST_TS AFTER curl, fast pipelines may produce rows whose
# created_at is EARLIER than POST_TS → silently filtered out.
# Z suffix matches the canonical column format `2026-07-08T12:09:03Z`
# (outbox_events + media_assets) so lexicographic TEXT comparison is exact.
POST_TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
info "POST timestamp (UTC): $POST_TS"

HTTP_CODE=$(curl -sS -m 30 -o "$RESP_FILE" -w '%{http_code}' \
  -X POST "$BASE/api/script/generate" \
  -H "X-Velox-Admin-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d "$ENVELOPE" 2>/dev/null || echo "000")

info "POST /api/script/generate -> HTTP $HTTP_CODE"

JOB_ID=""
if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "202" ]; then
  JOB_ID=$(jq -r '.job_id // .id // empty' "$RESP_FILE" 2>/dev/null || true)
fi

# ─── FAILURE MAPPING (godlike/06 one-canonical-owner-per-fact) ─
if [ "$HTTP_CODE" = "503" ]; then
  ERROR_CLASS=$(jq -r '.error_class // empty' "$RESP_FILE" 2>/dev/null || true)
  PROCESSOR=$(jq -r '.processor // empty' "$RESP_FILE" 2>/dev/null || true)
  if [ "$ERROR_CLASS" = "preflight_processor_missing" ]; then
    fail "preflight 503 — postprocessor '$PROCESSOR' not wired in composition"
    fail "canonical fix: PR-SCRIPTCONTRACT-COMPOSITION-WIRE (deadline 2026-07-15)"
    fail "  surface: internal/app/wire_script.go must populate PreflightCaps from"
    fail "  root.Domains.VoiceoverService / ImageService / root.Drive.DocClient"
    fail "  and inject deps.Generate.Caps. Until that lands, every"
    fail "  POST /api/script/generate with generate_voiceover=true returns 503"
    fail "  (godlike/07 conservative default — NO silent-skip of user-requested postprocessors)"
    echo
    echo "  Response body preserved at: $RESP_FILE"
    exit 1
  fi
  fail "503 with error_class=$ERROR_CLASS (body preserved at $RESP_FILE)"
  exit 1
fi

if [ "$HTTP_CODE" != "200" ] && [ "$HTTP_CODE" != "202" ]; then
  fail "POST /api/script/generate failed with HTTP $HTTP_CODE"
  fail "no canonical PR for HTTP $HTTP_CODE — inspect $RESP_FILE for error_class"
  echo
  echo "  Response body preserved at: $RESP_FILE"
  exit 1
fi

if [ -z "$JOB_ID" ]; then
  fail "no job_id in successful response: $(cat "$RESP_FILE")"
  fail "no canonical PR for missing job_id — inspect $RESP_FILE for response shape"
  echo
  echo "  Response body preserved at: $RESP_FILE"
  exit 1
fi

ok "job_id=$JOB_ID"

# ─── POLL job status ────────────────────────────────────────────
echo
echo "─── Poll job status (max $POLL_ITERATIONS iter × ${POLL_SLEEP_S}s = ~$((POLL_ITERATIONS * POLL_SLEEP_S / 60))min) ───"
TERMINAL=""
LAST_STATUS=""
# POSIX-portable for-loop (avoids `seq` which is missing on busybox/ash).
for ((i=1; i<=POLL_ITERATIONS; i++)); do
  POLL_FILE=$(mktemp)
  if ! curl -sS -m 5 -o "$POLL_FILE" -H "X-Velox-Admin-Token: $TOKEN" \
    "$BASE/api/jobs/$JOB_ID/full" 2>/dev/null; then
    rm -f "$POLL_FILE"
    sleep "$POLL_SLEEP_S"
    continue
  fi
  STATUS=$(jq -r '.status // .job.status // empty' "$POLL_FILE" 2>/dev/null || true)
  LAST_STATUS="$STATUS"
  if [ -n "$STATUS" ]; then info "iter=$i status=$STATUS"; fi
  case "$STATUS" in
    SUCCEEDED|completed|INDEX_PENDING)
      TERMINAL="SUCCEEDED"
      rm -f "$POLL_FILE"
      break
      ;;
    FAILED)
      TERMINAL="FAILED"
      rm -f "$POLL_FILE"
      break
      ;;
  esac
  rm -f "$POLL_FILE"
  sleep "$POLL_SLEEP_S"
done

if [ -z "$TERMINAL" ]; then
  fail "job $JOB_ID stuck in non-terminal after $POLL_ITERATIONS iter (last status=$LAST_STATUS)"
  fail "canonical fix: PR-JOBS-T01-ZOMBIE-SWEEP (stuck-no-terminal failure class — ship_sha 04235a7c)"
  echo
  echo "  Response file preserved at: $RESP_FILE"
  exit 1
fi

if [ "$TERMINAL" = "FAILED" ]; then
  fail "job $JOB_ID FAILED (terminal=$TERMINAL)"
  fail "canonical fix: PR-VOICEOVER-PIPELINE-DEBUG-2026-07-08 (TTS→Drive→finalize gap)"
  fail "see also: PR-VO-TTS-PERSISTENT-WORKER (TTS worker reliability — ship_sha 40239e039)"
  echo
  echo "  Response file preserved at: $RESP_FILE"
  exit 1
fi

ok "job terminal=$TERMINAL"

# ─── 4-TABLE CHECK ──────────────────────────────────────────────
echo
echo "─── 4-table verification (window: created_at > $POST_TS) ───"
V_FAIL=0

# Table 1: voiceovers — canonical ownership of per-voiceover row
# Schema: voiceovers has job_id (UNIQUE INDEX on (job_id, language)) + correlation_id
# canonical linkage. job_id is the SOLE filter; correlation_id is optional fallback
# (not all callers set it).
V1=$(sqlite3 -separator '|' "$DB_PATH" \
  "SELECT COUNT(*) FROM voiceovers WHERE job_id='$JOB_ID'" 2>/dev/null) || V1="ERR"
case "$V1" in
  ERR)
    fail "voiceovers: sqlite3 query FAILED (likely column drift / schema mismatch)"
    V_FAIL=$((V_FAIL+1))
    ;;
  ''|0)
    fail "voiceovers: 0 rows (expected ≥1) — finalizer didn't write per-language voiceover row"
    fail "canonical fix: PR-VOICEOVER-PIPELINE-DEBUG-2026-07-08 (finalizer 6-step TX)"
    V_FAIL=$((V_FAIL+1))
    ;;
  *)
    ok "voiceovers: $V1 row(s) for job_id=$JOB_ID"
    ;;
esac

# Table 2: upload_intents — canonical 5-step Drive upload lifecycle
# Schema: upload_intents has voiceover_id column (FK to voiceovers.id) + status
# + attempts + created_at (INTEGER epoch — driver-portable, no TEXT-format assumption).
# Filter via subquery on voiceovers (canonical job_id); NO timestamp filter to
# avoid the silent-success failure mode where INTEGER column compared against
# ISO 8601 TEXT returns 0 rows. The voiceover_id subquery is the canonical join.
V2=$(sqlite3 -separator '|' "$DB_PATH" \
  "SELECT COUNT(*) FROM upload_intents WHERE voiceover_id IN (SELECT id FROM voiceovers WHERE job_id='$JOB_ID')" 2>/dev/null) || V2="ERR"
case "$V2" in
  ERR)
    fail "upload_intents: sqlite3 query FAILED (likely column drift / schema mismatch)"
    V_FAIL=$((V_FAIL+1))
    ;;
  ''|0)
    fail "upload_intents: 0 rows (expected ≥1) — Drive upload didn't enqueue"
    fail "canonical fix: PR-VOICEOVER-PIPELINE-DEBUG-2026-07-08 (delivery.Publisher.Publish)"
    V_FAIL=$((V_FAIL+1))
    ;;
  *)
    ok "upload_intents: $V2 row(s) for voiceover_id IN voiceovers(job_id=$JOB_ID)"
    ;;
esac

# Table 3: outbox_events — asset.index.requested + voiceover.cleanup.requested
# (event names are canonical per internal/platform/sqlite/outboxevents/registry.go
# — EventVoiceoverCleanupRequested = "voiceover.cleanup.requested")
V3=$(sqlite3 -separator '|' "$DB_PATH" \
  "SELECT COUNT(*) FROM outbox_events WHERE (event_type='voiceover.cleanup.requested' OR event_type='asset.index.requested') AND created_at > '$POST_TS'" 2>/dev/null) || V3="ERR"
case "$V3" in
  ERR)
    fail "outbox_events: sqlite3 query FAILED (likely column drift / schema mismatch)"
    V_FAIL=$((V_FAIL+1))
    ;;
  ''|0)
    fail "outbox_events: 0 rows recent (expected ≥1) — outbox emit didn't fire"
    fail "canonical fix: PR-VOICEOVER-PIPELINE-DEBUG-2026-07-08 (outbox dispatch)"
    V_FAIL=$((V_FAIL+1))
    ;;
  *)
    ok "outbox_events: $V3 row(s) recent (voiceover.cleanup.requested OR asset.index.requested)"
    ;;
esac

# Table 4: media_assets — UPSERT projection from finalizer
V4=$(sqlite3 -separator '|' "$DB_PATH" \
  "SELECT COUNT(*) FROM media_assets WHERE source='voiceover' AND created_at > '$POST_TS'" 2>/dev/null) || V4="ERR"
case "$V4" in
  ERR)
    fail "media_assets: sqlite3 query FAILED (likely column drift / schema mismatch)"
    V_FAIL=$((V_FAIL+1))
    ;;
  ''|0)
    fail "media_assets: 0 rows recent (expected ≥1) — projection UPSERT didn't fire"
    fail "canonical fix: PR-VOICEOVER-PIPELINE-DEBUG-2026-07-08 (media_assets projection)"
    V_FAIL=$((V_FAIL+1))
    ;;
  *)
    ok "media_assets: $V4 row(s) recent (source=voiceover)"
    ;;
esac

# ─── VERDICT ────────────────────────────────────────────────────
echo
echo "============================================"
if [ "$V_FAIL" -eq 0 ]; then
  printf '%sVERDICT: PASS%s — job SUCCEEDED, all 4 tables populated\n' "$GRN" "$NC"
  echo "  job_id:         $JOB_ID"
  echo "  correlation_id: $CORRELATION_ID"
  echo "  voiceovers:     $V1"
  echo "  upload_intents: $V2"
  echo "  outbox_events:  $V3"
  echo "  media_assets:   $V4"
  rm -f "$RESP_FILE"   # PASS cleanup (no forensics needed)
  exit 0
else
  printf '%sVERDICT: FAIL%s — %s of 4 table check(s) failed\n' "$RED" "$NC" "$V_FAIL"
  echo "  job_id:         $JOB_ID (terminal=$TERMINAL)"
  echo "  correlation_id: $CORRELATION_ID"
  echo "  voiceovers:     $V1"
  echo "  upload_intents: $V2"
  echo "  outbox_events:  $V3"
  echo "  media_assets:   $V4"
  echo "  see canonical PR-* mapping above"
  echo
  echo "  Response file preserved at: $RESP_FILE"
  # FAIL: do NOT rm — preserve for operator forensics
  exit 1
fi
