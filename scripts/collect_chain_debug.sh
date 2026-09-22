#!/usr/bin/env bash
# scripts/collect_chain_debug.sh — collect ONE job's whole chain into a single
# markdown report: master stages/timings, prefetch decisions, remote queue
# records, GPU-worker logs + telemetry, local caches, Drive destinations.
#
# Why this exists: the chain spans four processes (pipelinegen master, the
# RenderingGen queue, the native GPU worker, the Chronon daemon) and each of
# them stores a different piece of the truth under a different identity. The
# queue record now carries the run correlation id (parent_job_id), which makes
# the join possible at all; this script performs it, so "where did the time go"
# is answered from evidence instead of from guesswork.
#
# Usage:
#   scripts/collect_chain_debug.sh <job_id>
#   scripts/collect_chain_debug.sh job_1790093191569826084_fbcfed1c
#
# Environment (defaults suit the local deployment):
#   VELOX_BASE_URL     default http://127.0.0.1:${VELOX_PORT:-8000}
#   VELOX_PORT         read from .env when present
#   VELOX_ADMIN_TOKEN  read from .env when present
#   QUEUE_URL          default http://127.0.0.1:8081  (RenderingGen queue)
#   WORKER_URL         default http://127.0.0.1:8085  (native GPU worker)
#   WORKER_UNIT        default renderinggen-worker.service
#   MASTER_UNIT        default pipelinegen.service
#   CHAIN_OUT_DIR      default ops/benchmarks/chain-debug
#   CHAIN_LOG_PAD      seconds of log context before/after the job (default 30)
#   CHAIN_DEPLOY_ENV   systemd EnvironmentFile to diff against ./.env
#                      (default /etc/pipelinegen/pipelinegen.env)
#   CHAIN_WORKSPACE_ROOT  worker job workspaces (default /tmp/renderinggen/jobs)
#   CHAIN_JOURNALD_CONF   journald config path (default /etc/systemd/journald.conf)
#   CHAIN_ACCEPTANCE      1 = the cold-cache prefetch assertion is BLOCKING:
#                         the script exits non-zero when it fails or cannot be
#                         evaluated (the report is still written).
#   CHAIN_SUDO_PASSWORD  optional: password piped to `sudo -S` so the worker /
#                        master journals can be read when the current user has
#                        no journal access. Never required, never prompted.
#
# Exit status: 0 when the report was written. Missing optional pieces (no
# sudo for journalctl, unavailable queue, empty caches) are recorded in the
# report as NOT AVAILABLE — they never abort the collection.
#
# HONESTY CONTRACT (why the journal reader has five states): an empty log window
# and an unreadable log are DIFFERENT facts, and conflating them ("no
# privileges?") sent an operator chasing sudo while the run had simply reused
# cached renders. Every section states which of the two it is, and an empty
# window falls back to the nearest lines in time plus a per-job worker.log when
# one exists.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 2
REPO_ROOT="$(pwd)"

if [ $# -lt 1 ]; then
  echo "usage: $0 <job_id>" >&2
  exit 2
fi
JOB_ID="$1"

if [ -f .env ]; then
  # shellcheck disable=SC1091
  set -a; . ./.env >/dev/null 2>&1; set +a
fi

BASE_URL="${VELOX_BASE_URL:-http://127.0.0.1:${VELOX_PORT:-8000}}"
QUEUE_URL="${QUEUE_URL:-http://127.0.0.1:8081}"
WORKER_URL="${WORKER_URL:-http://127.0.0.1:8085}"
WORKER_UNIT="${WORKER_UNIT:-renderinggen-worker.service}"
MASTER_UNIT="${MASTER_UNIT:-pipelinegen.service}"
OUT_DIR="${CHAIN_OUT_DIR:-ops/benchmarks/chain-debug}"
LOG_PAD="${CHAIN_LOG_PAD:-30}"
# CHAIN_ACCEPTANCE=1 turns the cold-cache prefetch assertion into a BLOCKING
# gate: the report is still written, but the script exits non-zero when the
# assertion fails or cannot be evaluated.
ACCEPTANCE_FAILED=0

if ! command -v jq >/dev/null 2>&1; then
  echo "collect_chain_debug: jq is required" >&2
  exit 2
fi
if [ -z "${VELOX_ADMIN_TOKEN:-}" ]; then
  echo "collect_chain_debug: VELOX_ADMIN_TOKEN is not set (and not in .env)" >&2
  exit 2
fi

mkdir -p "$OUT_DIR"
RAW="$OUT_DIR/$JOB_ID"
mkdir -p "$RAW"
REPORT="$OUT_DIR/$JOB_ID.md"

say() { printf '%s\n' "$*" >>"$REPORT"; }
section() { printf '\n## %s\n\n' "$*" >>"$REPORT"; }
fence() { printf '```\n' >>"$REPORT"; }
unfence() { printf '```\n' >>"$REPORT"; }

: >"$REPORT"
say "# Chain debug — \`$JOB_ID\`"
say ""
say "- collected: \`$(date -u +%Y-%m-%dT%H:%M:%SZ)\`"
say "- host: \`$(hostname)\`"
say "- master: \`$BASE_URL\` · queue: \`$QUEUE_URL\` · worker: \`$WORKER_URL\`"

# ── 1. master job payload ─────────────────────────────────────────────
section "1. Master job (GET /api/jobs/{id}/full)"
FULL="$RAW/job-full.json"
if curl -sS -m 30 "$BASE_URL/api/jobs/$JOB_ID/full" \
     -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" -o "$FULL" 2>"$RAW/curl.err"; then
  jq -r '
    "| field | value |",
    "|---|---|",
    "| status | `\(.status)` |",
    "| current_stage | `\(.current_stage // "-")` |",
    "| progress | \(.progress // 0) |",
    "| created_at | `\(.created_at)` |",
    "| updated_at | `\(.updated_at)` |",
    "| correlation_id | `\(.correlation_id // "-")` |",
    "| warnings | \(.warnings | if type == "array" then length else 0 end) |"
  ' "$FULL" >>"$REPORT" 2>/dev/null
  RUN_ID="$(jq -r '.result.run_id // empty' "$FULL" 2>/dev/null)"
  CORRELATION_ID="$(jq -r '.correlation_id // empty' "$FULL" 2>/dev/null)"
  CREATED_AT="$(jq -r '.created_at // empty' "$FULL" 2>/dev/null)"
  UPDATED_AT="$(jq -r '.updated_at // empty' "$FULL" 2>/dev/null)"

  say ""
  say "### Where the wall went (canonical Run clock)"
  say ""
  say "| wall_ms | attributed_ms | unattributed_ms | bottleneck_stage | bottleneck_operation | bottleneck_% |"
  say "|---|---|---|---|---|---|"
  jq -r '
    "| \(.timing.wall_ms // 0) | \(.timing.attributed_ms // 0) | \(.timing.unattributed_ms // 0) | \(.timing.bottleneck_stage // "-") | \(.timing.bottleneck_operation // "-") | \(.timing.bottleneck_percent // 0) |"
  ' "$FULL" >>"$REPORT" 2>/dev/null

  say ""
  say "### Timing operations (by work_ms, owner-measured)"
  say ""
  say "| stage | component | operation | calls | work_ms | queue_wait_ms |"
  say "|---|---|---|---|---|---|"
  jq -r '
    (.timing.operations // []) | sort_by(-(.work_ms // 0))[] |
    "| \(.stage) | \(.component) | \(.operation) | \(.calls // 1) | \(.work_ms // 0) | \(.queue_wait_ms // 0) |"
  ' "$FULL" >>"$REPORT" 2>/dev/null

  say ""
  say "### Critical path"
  say ""
  say "| stage | duration_ms | % of wall |"
  say "|---|---|---|"
  jq -r '
    (.timing.critical_path // [])[] |
    "| \(.name) | \(.duration_ms // 0) | \(( .percent // 0 ) * 100 | round | . / 100) |"
  ' "$FULL" >>"$REPORT" 2>/dev/null

  say ""
  say "### Audio"
  say ""
  fence
  jq -c '.result.result.audio_metrics // "NOT AVAILABLE"' "$FULL" >>"$REPORT" 2>/dev/null
  unfence
  say ""
  say "Prefetch outcome (polling projection — \`audio_prefetch\`):"
  say ""
  fence
  jq -c '.result.result.audio_prefetch // "NOT AVAILABLE (prefetch not needed or older build)"' "$FULL" >>"$REPORT" 2>/dev/null
  unfence

  # ── cold-cache prefetch acceptance (P3-11) ──────────────────────
  #
  # The prefetch fix was verified once BY HAND ("at cold cache the prefetch
  # delivered N assets and the synchronous prepare was ~0"). A one-off manual
  # reading is not a gate: it does not fail when a later change reintroduces the
  # synchronous download, and it is not reproducible from the artifact. This
  # block turns that reading into an explicit, printed PASS/FAIL assertion over
  # the run's own evidence, and makes it BLOCKING when CHAIN_ACCEPTANCE=1 so a
  # cold-cache run can be used as a CI/ops gate.
  say ""
  say "### Cold-cache prefetch acceptance (CHAIN_ACCEPTANCE=${CHAIN_ACCEPTANCE:-0})"
  say ""
  PREFETCH_VERDICT="$(jq -r '
    (.result.result.audio_prefetch // {}) as $p
    | (.result.result.audio_metrics // {}) as $m
    | ($p.clip_audio_requested // 0) as $req
    | ($p.clip_audio_ready // 0) as $ready
    | ($m.clip_audio_prepare_ms // 0) as $sync
    | ($m.media_fetch_ms // 0) as $fetch
    | if $req > 0 and $ready >= $req and $sync == 0 and $fetch == 0 then "pass" else "fail" end
  ' "$FULL" 2>/dev/null)"
  [ -z "$PREFETCH_VERDICT" ] && PREFETCH_VERDICT="unknown"
  say "| check | value | expected |"
  say "|---|---|---|" 
  jq -r '
    (.result.result.audio_prefetch // {}) as $p
    | (.result.result.audio_metrics // {}) as $m
    | "| clip assets requested | \($p.clip_audio_requested // 0) | > 0 (a cold cache must have work to do) |",
      "| clip assets prefetched | \($p.clip_audio_ready // 0) | >= requested |",
      "| bgm/sfx resolved | \($p.bgm_sfx_resolved // 0) | >= \((($p.bgm_requested // 0) + ($p.sfx_requested // 0))) |",
      "| prefetch wall | \($p.duration_ms // 0) ms | overlaps TTS, not serial |",
      "| SYNCHRONOUS clip_audio_prepare_ms | \($m.clip_audio_prepare_ms // 0) | 0 (this is the whole point of the prefetch) |",
      "| SYNCHRONOUS media_fetch_ms | \($m.media_fetch_ms // 0) | 0 (no download left on the critical path) |"
  ' "$FULL" >>"$REPORT" 2>/dev/null
  say ""
  say "\`prefetch_acceptance=$PREFETCH_VERDICT\`"
  case "$PREFETCH_VERDICT" in
    pass)
      say ""
      say "PASS: the prefetch delivered every requested asset and the synchronous prepare cost is zero — the download left the critical path."
      ;;
    fail)
      say ""
      say "FAIL: the prefetch did not fully cover the run, or a synchronous prepare/download is still on the critical path. This is the regression the acceptance exists to catch — do NOT read a green job status as a pass."
      if [ "${CHAIN_ACCEPTANCE:-0}" != "0" ]; then ACCEPTANCE_FAILED=1; fi
      ;;
    *)
      say ""
      say "UNKNOWN: the run carries no \`audio_prefetch\` summary (prefetch not needed, not wired, or an older build). Not a pass — the acceptance is simply unevaluated."
      if [ "${CHAIN_ACCEPTANCE:-0}" != "0" ]; then ACCEPTANCE_FAILED=1; fi
      ;;
  esac

  say ""
  say "### Localized renders"
  say ""say "| scene | language | status | source | reused | wall_ms | render_job_id | sha256 | drive_file_id | plan metrics (worker-reported) |"
say "|---|---|---|---|---|---|---|---|---|---|"
jq -r '
    (.result.result.localized_renders // [])[] |
    "| \(.scene_id) | \(.language) | \(.status) | `\(.render_source // "-")` | \(.reused // false) | \(.wall_ms) | `\(.render_job_id // "-")` | `\(.sha256[0:16])` | `\(.drive_file_id)` | `\(( .metrics // {} ) | tojson)` |"
  ' "$FULL" >>"$REPORT" 2>/dev/null
  say ""
  say "Interpretation: \`render_source=deterministic_render_cache\` + \`reused=true\` + \`metrics: null\` is a run that reused already-certified bytes — its short per-clip wall is Drive I/O, NOT a missing GPU measurement."
  say ""
  fence
  jq -c '{render_metrics: .result.result.render_metrics, expected_render_count: .result.result.expected_render_count, localized_render_failures: (.result.result.localized_render_failures // [])}' "$FULL" >>"$REPORT" 2>/dev/null
  unfence
else
  say ""
  say "NOT AVAILABLE: \`$BASE_URL/api/jobs/$JOB_ID/full\` unreachable (\`$(cat "$RAW/curl.err" 2>/dev/null | head -1)\`)."
  RUN_ID=""; CORRELATION_ID=""; CREATED_AT=""; UPDATED_AT=""
fi

# ── 2. time window for the logs ───────────────────────────────────────
SINCE=""
UNTIL=""
if [ -n "$CREATED_AT" ]; then
  SINCE="$(date -u -d "$CREATED_AT - ${LOG_PAD} seconds" +'%Y-%m-%d %H:%M:%S' 2>/dev/null || true)"
fi
if [ -n "$UPDATED_AT" ]; then
  UNTIL="$(date -u -d "$UPDATED_AT + ${LOG_PAD} seconds" +'%Y-%m-%d %H:%M:%S' 2>/dev/null || true)"
fi
[ -z "$SINCE" ] && SINCE="$(date -u -d '-20 minutes' +'%Y-%m-%d %H:%M:%S')"
[ -z "$UNTIL" ] && UNTIL="$(date -u +'%Y-%m-%d %H:%M:%S')"

# ── journal reading, with the failure MODES kept apart ─────────────────
#
# The single biggest lie this script used to tell was "could not read <unit>
# journal (no privileges?)" printed whenever the window was empty. An empty
# window has three completely different causes and only one of them is about
# privileges:
#   * no-unit        — the unit does not exist on this host;
#   * journal-empty  — journald holds no lines for this unit at all;
#   * no-access      — permissions / no journald (the only case worth a "?");
#   * empty-window   — the journal IS readable and holds lines, just none in
#                      this job's window. For a run that reused cached renders
#                      this is the EXPECTED answer, not an error.
# Reporting them as one message sent an operator hunting for sudo instead of
# noticing the run produced no worker work at all.

# journal_run OUTFILE ERRFILE UNIT ARGS... — runs journalctl with the best
# available privilege mode (plain, then sudo -n, then CHAIN_SUDO_PASSWORD) and
# returns its exit status. Never prompts: an unattended collection must not hang.
journal_run() { # outfile errfile unit args...
  local out="$1" err="$2" unit="$3"
  shift 3
  local rc
  journalctl -q -u "$unit" "$@" >"$out" 2>"$err"; rc=$?
  if [ $rc -eq 0 ]; then return 0; fi
  if command -v sudo >/dev/null 2>&1; then
    if sudo -n true >/dev/null 2>&1; then
      if sudo -n journalctl -q -u "$unit" "$@" >"$out" 2>"$err"; then return 0; fi
      rc=$?
    elif [ -n "${CHAIN_SUDO_PASSWORD:-}" ]; then
      if printf '%s\n' "$CHAIN_SUDO_PASSWORD" | sudo -S -p '' journalctl -q -u "$unit" "$@" >"$out" 2>"$err"; then return 0; fi
      rc=$?
    fi
  fi
  return $rc
}

# journal_state UNIT OUTFILE → echoes ok | empty-window | journal-empty |
# no-unit | no-access and writes the window's lines into OUTFILE when ok.
journal_state() { # unit outfile
  local unit="$1" out="$2"
  local err probe
  err="$(mktemp)"; probe="$(mktemp)"
  # Existence is asked of systemd, NOT inferred from journalctl: on this host
  # `journalctl -u <nonexistent>.service` exits 0 with empty output (verified),
  # so an exit-code check alone cannot tell a missing unit from an idle one and
  # would have reported the wrong diagnosis again.
  if command -v systemctl >/dev/null 2>&1 && ! systemctl cat "$unit" >/dev/null 2>&1; then
    rm -f "$err" "$probe"; printf 'no-unit'; return 0
  fi
  journal_run "$out" "$err" "$unit" --since "$SINCE" --until "$UNTIL" --no-pager -o cat
  if [ $? -eq 0 ] && [ -s "$out" ]; then rm -f "$err" "$probe"; printf 'ok'; return 0; fi
  # The window produced nothing. Ask the unit for its newest line at all: a
  # readable non-empty answer means the journal is fine and the window is the
  # problem (the job never used this unit).
  journal_run "$probe" "$err" "$unit" -n 1 --no-pager -o cat
  if [ $? -eq 0 ] && [ -s "$probe" ]; then rm -f "$err" "$probe"; printf 'empty-window'; return 0; fi
  local state="no-access"
  # Only an explicit "nothing recorded" (or silence, since -q suppresses the
  # informational "-- No entries --") is journal-empty; anything else that
  # failed is treated as a real access problem.
  if grep -qiE 'has no entries|no journal files|no entries' "$err" || [ ! -s "$err" ]; then
    state="journal-empty"
  fi
  rm -f "$err" "$probe"
  printf '%s' "$state"
}

# journal_recent UNIT OUTFILE N — the nearest-in-time fallback used when the
# job window is empty, so the report can still show what the unit was doing
# around the run instead of nothing at all.
journal_recent() { # unit outfile n
  local unit="$1" out="$2" n="$3" err
  err="$(mktemp)"
  journal_run "$out" "$err" "$unit" -n "$n" --no-pager -o cat
  local rc=$?
  rm -f "$err"
  return $rc
}

# journal_verdict_line UNIT STATE — the report sentence for a non-ok state.
journal_verdict_line() { # unit state
  local unit="$1" state="$2"
  case "$state" in
    empty-window)
      printf 'WINDOW EMPTY (not a permissions problem): \`%s\` has journal lines, but NONE inside %s → %s, so this run produced no work on that unit — typically a dedup/cached run that reused already-rendered artifacts. The nearest lines in time follow; do not read this as a missing journal.' "$unit" "$SINCE" "$UNTIL"
      ;;
    journal-empty)
      printf 'NO ENTRIES: journald holds no lines at all for \`%s\` (unit never logged since the journal was created, or the journal is not persistent).' "$unit"
      ;;
    no-unit)
      printf 'NOT AVAILABLE: unit \`%s\` does not exist on this host.' "$unit"
      ;;
    *)
      printf 'NOT AVAILABLE: could not read \`%s\` journal (permissions or no journal access). This is the ONE case that is about privileges.' "$unit"
      ;;
  esac
}

# ── env drift (the two sources of truth) ───────────────────────────────
#
# The deployment reads /etc/pipelinegen/pipelinegen.env through systemd's
# EnvironmentFile= while this checkout reads ./.env, and the loader only fills
# in keys the environment does not already define. So the DEPLOY file silently
# wins wherever the two disagree — which is exactly how a destination-folder
# misunderstanding stayed invisible. Print the divergences instead of trusting
# that the two files match.
DEPLOY_ENV_PATH="${CHAIN_DEPLOY_ENV:-/etc/pipelinegen/pipelinegen.env}"

read_deploy_env() { # outfile → 0 when the deploy env could be read
  local out="$1"
  if [ -r "$DEPLOY_ENV_PATH" ]; then
    cat "$DEPLOY_ENV_PATH" >"$out" 2>/dev/null && return 0
  fi
  if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    sudo -n cat "$DEPLOY_ENV_PATH" >"$out" 2>/dev/null && return 0
  elif [ -n "${CHAIN_SUDO_PASSWORD:-}" ]; then
    printf '%s\n' "$CHAIN_SUDO_PASSWORD" | sudo -S -p '' cat "$DEPLOY_ENV_PATH" >"$out" 2>/dev/null && return 0
  fi
  return 1
}

# is_secret_key KEY — true for keys whose VALUE must never be written into a
# report (the drift table would otherwise copy a live worker token, API key or
# service-account blob into ops/benchmarks, which is a durable file that gets
# shared). The FACT of divergence is what matters; the value is not needed to
# act on it.
is_secret_key() { # key
  case "$1" in
    *TOKEN* | *SECRET* | *PASSWORD* | *PASSWD* | *CREDENTIAL* | *API_KEY* | *APIKEY* | *_KEY | *_KEY_* | *SERVICE_ACCOUNT* | *CLIENT_SECRET*) return 0 ;;
  esac
  return 1
}

# mask_value KEY VALUE — VALUE unless KEY names a secret.
mask_value() { # key value
  if is_secret_key "$1"; then
    printf '<redacted>'
  else
    printf '%s' "$2"
  fi
}

# env_value FILE KEY — the last non-comment assignment for KEY, unquoted.
env_value() { # file key
  local file="$1" key="$2"
  sed -n "s/^[[:space:]]*export[[:space:]]\{1,\}$key=//p; s/^[[:space:]]*$key=//p" "$file" 2>/dev/null \
    | tail -1 | tr -d '"' | tr -d "'"
}

# ── 3. master logs (prefetch + materializer evidence) ─────────────────
section "2. Master log window ($SINCE → $UNTIL, $MASTER_UNIT)"
MASTER_LOG="$RAW/master.log"
MASTER_JOURNAL_STATE="$(journal_state "$MASTER_UNIT" "$MASTER_LOG")"
if [ "$MASTER_JOURNAL_STATE" = "ok" ]; then
  say "Owned by this job (job_id / correlation_id / run_id):"
  say ""
  fence
  grep -E "$JOB_ID|${CORRELATION_ID:-___none___}|${RUN_ID:-___none___}" "$MASTER_LOG" 2>/dev/null | cut -c1-400 | head -60 >>"$REPORT"
  unfence
  say ""
  say "Audio prefetch + clip-audio materialization (the stage that hides download cost):"
  say ""
  fence
  grep -iE 'audio prefetch|prefetch-clip-audio|canonical_materializer\.(materialize|drive_download)' "$MASTER_LOG" 2>/dev/null \
    | cut -c1-320 | head -80 >>"$REPORT"
  unfence
  say ""
  say "Render plan revisions seen by the master:"
  say ""
  fence
  grep -o '"plan_revision":"[^"]*"' "$MASTER_LOG" 2>/dev/null | sort -u | head -40 >>"$REPORT"
  unfence
else
  say "$(journal_verdict_line "$MASTER_UNIT" "$MASTER_JOURNAL_STATE")"
  # Fall back to the nearest lines in time so the report still shows what the
  # unit was doing around this run instead of nothing at all.
  if journal_recent "$MASTER_UNIT" "$MASTER_LOG" 400; then
    say ""
    say "Nearest master lines in time (fallback window; may belong to another run):"
    say ""
    fence
    grep -E "${CORRELATION_ID:-___none___}|$JOB_ID" "$MASTER_LOG" 2>/dev/null | cut -c1-400 | head -40 >>"$REPORT" || true
    unfence
  fi
fi

# ── 4. remote queue records ───────────────────────────────────────────
section "3. Remote render queue (GET $QUEUE_URL/jobs/{id})"
say "Workers registered:"
say ""
fence
curl -sS -m 10 "$QUEUE_URL/workers" 2>/dev/null | head -c 2000 >>"$REPORT" || say "NOT AVAILABLE"
unfence
say ""
say "Queue metrics snapshot:"
say ""
fence
curl -sS -m 10 "$QUEUE_URL/metrics" 2>/dev/null | grep -E 'renderinggen_jobs_pending|renderinggen_lease_expired_total|_count' | head -20 >>"$REPORT" || say "NOT AVAILABLE"
unfence
say ""
say "| queue job id | state | worker | queued_at | started_at | completed_at | parent_job_id | chunks | attempts |"
say "|---|---|---|---|---|---|---|---|---|"
QUEUE_ROWS=0
# SOURCE 1 (deterministic): the master stamps parent_job_id on every job it
# enqueues, so GET /jobs?parent_job_id=… lists this run's render jobs without
# knowing any id in advance and WITHOUT scraping the log. The run correlation id
# is what the producer uses as the parent.
PARENT_KEY="${CORRELATION_ID:-${RUN_ID:-${JOB_ID:-}}}"
if [ -n "$PARENT_KEY" ]; then
  PARENT_BODY="$(curl -sS -m 10 "$QUEUE_URL/jobs?parent_job_id=$(printf '%s' "$PARENT_KEY" | jq -sRr @uri)" 2>/dev/null)"
  if printf '%s' "$PARENT_BODY" | jq -e 'type == "array"' >/dev/null 2>&1; then
    printf '%s' "$PARENT_BODY" | jq -c '.' >"$RAW/queue-by-parent.json" 2>/dev/null
    while IFS= read -r row; do
      [ -z "$row" ] && continue
      printf '%s\n' "$row" >>"$REPORT"
      QUEUE_ROWS=$((QUEUE_ROWS + 1))
    done < <(printf '%s' "$PARENT_BODY" | jq -r '[.[]] | sort_by(.queued_at // "")[] | "| `\(.id)` | \(.state) | \(.worker // "-") | \(.queued_at // "-") | \(.started_at // "-") | \(.completed_at // "-") | `\(.parent_job_id // "-")` | \(.chunk_index // 0) | \(.attempts // 0) |"' 2>/dev/null)
  fi
fi
# SOURCE 2 (legacy fallback): the master log's plan_revision values. Kept for
# older binaries whose localized renders carry no render_job_id; this is the
# scrape that made a dedup run report "no queue record resolved".
QUEUE_SOURCE="parent_job_id=$PARENT_KEY"
if [ "$QUEUE_ROWS" -eq 0 ]; then
  QUEUE_SOURCE="master-log plan_revision scrape (legacy)"
  # Prefer the ids the run result now names directly — a dedup run still lists
  # them, where the log window may hold nothing at all.
  RESULT_RENDER_IDS=""
  if [ -s "${FULL:-/nonexistent}" ]; then
    RESULT_RENDER_IDS="$(jq -r '(.result.result.localized_renders // [])[] | .render_job_id // empty' "$FULL" 2>/dev/null | sort -u)"
  fi
  if [ -n "$RESULT_RENDER_IDS" ]; then
    QUEUE_SOURCE="localized_renders[].render_job_id (run result)"
  else
    RESULT_RENDER_IDS="$(grep -o '"plan_revision":"[^"]*"' "$MASTER_LOG" 2>/dev/null | sed 's/.*:"//; s/"$//' | sort -u | head -20)"
  fi
  while IFS= read -r plan_rev; do
    [ -z "$plan_rev" ] && continue
    body="$(curl -sS -m 10 "$QUEUE_URL/jobs/$(printf '%s' "$plan_rev" | jq -sRr @uri)" 2>/dev/null)"
    [ -z "$body" ] && continue
    printf '%s' "$body" | jq -c '.' >"$RAW/queue-$(printf '%s' "$plan_rev" | tr '/' '_').json" 2>/dev/null
    row="$(printf '%s' "$body" | jq -r '"| `\(.id)` | \(.state) | \(.worker // "-") | \(.queued_at // "-") | \(.started_at // "-") | \(.completed_at // "-") | `\(.parent_job_id // "-")` | \(.chunk_index // 0) | \(.attempts // 0) |"' 2>/dev/null)"
    if [ -n "$row" ]; then
      printf '%s\n' "$row" >>"$REPORT"
      QUEUE_ROWS=$((QUEUE_ROWS + 1))
    fi
  done <<<"$RESULT_RENDER_IDS"
fi
say ""
say "Queue record source: \`$QUEUE_SOURCE\`."
if [ "$QUEUE_ROWS" -eq 0 ]; then
  say ""
  say "NO QUEUE RECORD RESOLVED — and this is NOT proof that nothing ran. The run result named no render ids AND the master window held no plan revisions, so the ids had to be scraped from a log we do not have. This is exactly the case a dedup (cached) run hits: \`localized_renders[].render_job_id\` is empty on builds older than the projection fix, while \`metrics: null\` plus a short per-clip wall is the cache signature. Cross-check section 4 before concluding anything."
  say ""
  say "| queue job id | state | worker | queued_at | started_at | completed_at | parent_job_id | chunks | attempts |"
  say "|---|---|---|---|---|---|---|---|---|"
  say "| - | - | - | - | - | - | - | - | unresolved |"
fi

# ── 5. GPU worker logs + workspace ────────────────────────────────────
section "4. GPU worker ($WORKER_UNIT)"
fence
curl -sS -m 10 "$WORKER_URL/health" 2>/dev/null | head -c 1500 >>"$REPORT" || say "NOT AVAILABLE"
unfence
WORKER_LOG="$RAW/worker.log"
WORKER_JOURNAL_STATE="$(journal_state "$WORKER_UNIT" "$WORKER_LOG")"
if [ "$WORKER_JOURNAL_STATE" = "ok" ]; then
  say ""
  say "Asset materialization / warm-up / encode lines for this job's clips:"
  say ""
  fence
  CLIP_IDS="$(jq -r '(.result.result.localized_renders // [])[] | .clip_id' "$FULL" 2>/dev/null | sort -u | paste -sd'|' -)"
  if [ -n "${CLIP_IDS:-}" ]; then
    grep -E "$CLIP_IDS" "$WORKER_LOG" 2>/dev/null | cut -c1-320 | head -80 >>"$REPORT"
  else
    tail -40 "$WORKER_LOG" | cut -c1-320 >>"$REPORT"
  fi
  unfence
  say ""
  say "Telemetry schema actually produced (the master's verifier expects a bounded summary):"
  say ""
  fence
  grep -o '"schema":"[^"]*"' "$WORKER_LOG" 2>/dev/null | sort | uniq -c | head -10 >>"$REPORT"
  grep -c 'composition prediction not verifiable' "$WORKER_LOG" 2>/dev/null | sed 's/^/composition prediction not verifiable: /' >>"$REPORT"
  grep -c 'failed to requeue retry-wait job' "$WORKER_LOG" 2>/dev/null | sed 's/^/retry-wait requeue failures: /' >>"$REPORT"
  unfence
  say ""
  say "Worker HTTP surface (the worker is only interrogable where it answers):"
  say ""
  fence
  for path in /health /metrics /progress; do
    code="$(curl -sS -m 8 -o /dev/null -w '%{http_code}' "$WORKER_URL$path" 2>/dev/null || printf '000')"
    printf '%-10s %s\n' "$path" "$code"
  done >>"$REPORT"
  curl -sS -m 8 "$WORKER_URL/metrics" 2>/dev/null | grep -E '^renderinggen_' | head -40 >>"$REPORT" || true
  unfence
else
  say ""
  say "$(journal_verdict_line "$WORKER_UNIT" "$WORKER_JOURNAL_STATE")"
  # Per-job worker log: the workspace copy is what makes this section readable on
  # a host where the run window is empty (a cached run) or journald access is
  # missing, so it is checked BEFORE the nearest-lines fallback.
  PER_JOB_FOUND=0
  for ws in $(find "${CHAIN_WORKSPACE_ROOT:-/tmp/renderinggen/jobs}" -maxdepth 2 -name 'worker.log' -newermt "-$(( LOG_PAD + 3600 )) seconds" 2>/dev/null | head -5); do
    PER_JOB_FOUND=1
    say ""
    say "Per-job worker log \`$ws\` (job_id + parent_job_id are on every line):"
    say ""
    fence
    head -80 "$ws" | cut -c1-400 >>"$REPORT"
    unfence
  done
  if [ "$PER_JOB_FOUND" -eq 0 ] && journal_recent "$WORKER_UNIT" "$WORKER_LOG" 400; then
    say ""
    say "Nearest worker lines in time (fallback window; may belong to another run):"
    say ""
    fence
    tail -60 "$WORKER_LOG" | cut -c1-320 >>"$REPORT"
    unfence
  fi
fi

# ── 6. worker workspace + telemetry sidecars ──────────────────────────
section "5. Worker workspace (/tmp/renderinggen/jobs)"
WS_ROOT="${CHAIN_WORKSPACE_ROOT:-/tmp/renderinggen/jobs}"
if [ -d "$WS_ROOT" ]; then
  fence
  find "$WS_ROOT" -maxdepth 5 -newermt "-$(( LOG_PAD + 3600 )) seconds" -printf '%TH:%TM:%TS %10s %p\n' 2>/dev/null \
    | sort | tail -60 >>"$REPORT"
  unfence
  say ""
  say "Chronon telemetry sidecars (bounded facts the master can verify against):"
  say ""
  fence
  for sidecar in $(find "$WS_ROOT" -maxdepth 4 -name '*.telemetry-summary.json' -newermt "-2 hours" 2>/dev/null | head -5); do
    printf '%s\n' "── $sidecar"
    jq -c '{schema, job: {execution_path: .job.execution_path, gpu: .job.gpu}, summary: .summary}' "$sidecar" 2>/dev/null | head -c 700
    printf '\n'
  done >>"$REPORT"
  unfence

  # The worker's OWN per-job record. `worker.log` is written live into each job
  # workspace and mirrored to a size-capped durable copy under
  # <jobs root>/.workerlogs that survives the workspace cleanup. This is what
  # makes this section independent of the host's journald retention, and it is
  # the answer when the journal window is empty (a run that reused cached
  # renders has no worker journal lines but still has its own log).
  JOB_LOG_DIR="${CHAIN_JOB_LOG_DIR:-$WS_ROOT/.workerlogs}"
  LIVE_JOB_LOGS=$(find "$WS_ROOT" -maxdepth 2 -name 'worker.log' -newermt "-$(( LOG_PAD + 3600 )) seconds" 2>/dev/null | head -15)
  DURABLE_JOB_LOGS=""
  if [ -d "$JOB_LOG_DIR" ]; then
    DURABLE_JOB_LOGS=$(ls -1t "$JOB_LOG_DIR" 2>/dev/null | head -10)
  fi
  say ""
  say "Worker per-job logs (live \`<job>/worker.log\`; durable copies in \`$JOB_LOG_DIR\`):"
  say ""
  fence
  if [ -z "$LIVE_JOB_LOGS" ] && [ -z "$DURABLE_JOB_LOGS" ]; then
    printf 'none found (no worker.log in %s newer than %ds and no durable copies in %s)\n' \
      "$WS_ROOT" "$(( LOG_PAD + 3600 ))" "$JOB_LOG_DIR" >>"$REPORT"
  else
    for f in $LIVE_JOB_LOGS; do
      printf '%s\n' "── $f"
      tail -25 "$f" | cut -c1-320
    done >>"$REPORT"
    for f in $DURABLE_JOB_LOGS; do
      printf '%s\n' "── $JOB_LOG_DIR/$f"
      tail -25 "$JOB_LOG_DIR/$f" | cut -c1-320
    done >>"$REPORT"
  fi
  unfence
else
  say "NOT AVAILABLE: \`$WS_ROOT\` not present on this host."
fi

# ── 7. local caches and outputs ───────────────────────────────────────
section "6. Local caches and rendered outputs"
for dir in data/tmp/audioassets/assets data/tmp/materialized/assets; do
  say "### \`$dir\`"
  say ""
  fence
  if [ -d "$dir" ]; then
    find "$dir" -maxdepth 2 -type f -printf '%TH:%TM:%TS %10s %p\n' 2>/dev/null | sort | tail -30 >>"$REPORT"
  else
    say "NOT AVAILABLE"
  fi
  unfence
  say ""
done
say "### \`data/tmp/localization\` (rendered videos, newest first)"
say ""
fence
find data/tmp/localization -maxdepth 1 -name '*.mp4' -printf '%TH:%TM:%TS %10s %f\n' 2>/dev/null | sort | tail -25 >>"$REPORT" || say "NOT AVAILABLE"
unfence

# ── 8. environment drift (two sources of truth) ───────────────────────
section "7. Environment drift (\`$DEPLOY_ENV_PATH\` vs \`./.env\`)"
say "systemd loads the deploy file via \`EnvironmentFile=\` before the process starts and the app's dotenv loader only fills keys that are NOT already set, so wherever the two disagree THE DEPLOY FILE WINS. A silent divergence here is how a destination folder ends up different from the one the operator reads in \`.env\`."
say ""
DEPLOY_ENV_RAW="$RAW/deploy.env"
if read_deploy_env "$DEPLOY_ENV_RAW"; then
  DEPLOY_KEYS="$(sed -n 's/^[[:space:]]*export[[:space:]]\{1,\}\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p; s/^[[:space:]]*\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' "$DEPLOY_ENV_RAW" 2>/dev/null | sort -u)"
  LOCAL_KEYS=""
  [ -f .env ] && LOCAL_KEYS="$(sed -n 's/^[[:space:]]*export[[:space:]]\{1,\}\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p; s/^[[:space:]]*\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env 2>/dev/null | sort -u)"
  say "| key | deploy | .env | verdict |"
  say "|---|---|---|---|"
  DRIFT=0
  DEPLOY_ONLY=0
  ENV_ONLY=0
  for key in $(printf '%s\n%s\n' "$DEPLOY_KEYS" "$LOCAL_KEYS" | sed '/^$/d' | sort -u); do
    dv="$(env_value "$DEPLOY_ENV_RAW" "$key")"
    lv="$(env_value .env "$key")"
    if [ -n "$dv" ] && [ -n "$lv" ]; then
      # Defined in BOTH: only here can the deploy file silently override .env.
      if [ "$dv" = "$lv" ]; then continue; fi
      verdict="**DEPLOY WINS (divergent)**"
      if is_secret_key "$key"; then verdict="**DEPLOY WINS (divergent; values redacted)**"; fi
      say "| \`$key\` | $(mask_value "$key" "$dv") | $(mask_value "$key" "$lv") | $verdict |"
      DRIFT=$((DRIFT + 1))
      continue
    fi
    # Defined in only ONE file: no override happens (systemd sets what it lists,
    # and the dotenv loader fills the rest), so it is reported as a count, never
    # as drift. Counting them as drift would bury the real conflicts in noise.
    if [ -n "$dv" ]; then DEPLOY_ONLY=$((DEPLOY_ONLY + 1)); else ENV_ONLY=$((ENV_ONLY + 1)); fi
  done
  say ""
  say "Divergent keys (in both files, different values — the deploy file wins): **$DRIFT**. \`drift_env_count=$DRIFT\`"
  say ""
  say "Deploy-only keys: $DEPLOY_ONLY (deploy supplies them; \`.env\` has no value). \`.env\`-only keys: $ENV_ONLY (the deploy file does not set them, so \`.env\` applies)."
  if [ "$DRIFT" -eq 0 ]; then
    say ""
    say "No conflict between the two files: every key they BOTH define has the same value, so the deploy override cannot change the effective configuration."
  fi
  say ""
  say "Destination-affecting keys (folder ids), the ones whose divergence relocates output:"
  say ""
  fence
  for key in $(printf '%s\n' "$DEPLOY_KEYS" "$LOCAL_KEYS" | sed '/^$/d' | sort -u | grep -E 'FOLDER_ID|DOCS_FOLDER|DRIVE_.*ROOT'); do
    printf '%-48s deploy=[%s] .env=[%s]\n' "$key" \
      "$(mask_value "$key" "$(env_value "$DEPLOY_ENV_RAW" "$key")")" \
      "$(mask_value "$key" "$(env_value .env "$key")")"
  done >>"$REPORT"
  unfence
else
  say "NOT AVAILABLE: \`$DEPLOY_ENV_PATH\` could not be read (root-owned and no sudo). Set \`CHAIN_DEPLOY_ENV\` or run with sudo for this section; the drift question is unanswered, NOT disproven."
fi

# ── 9. journald retention (the collector depends on it for the remote half) ─
#
# Half of this report comes from journald. If the journal is not persistent, or
# its retention is shorter than the time between a run and a triage, the evidence
# for the remote half is simply GONE — which reads exactly like "nothing ran".
# This is a read-only diagnosis: changing journald.conf is a host-level change
# and stays an operator action.
section "8. journald retention (read-only diagnosis)"
say "Journal disk usage:"
say ""
fence
DISK_USAGE="$(journalctl --disk-usage 2>/dev/null | head -1)"
case "$DISK_USAGE" in
  *"take up"*) printf '%s\n' "$DISK_USAGE" >>"$REPORT" ;;
  "") say "NOT AVAILABLE (no journal access)" ;;
  # journalctl answers with a hint instead of a number when the caller cannot
  # see the system journal; say so rather than pasting the hint as if it were a
  # measurement.
  *) say "NOT AVAILABLE to this user (the caller cannot see the system journal; \`journalctl --disk-usage\` returned a permissions hint)" ;;
esac
unfence
say ""
say "Retention configuration (unset keys are commented out → journald defaults apply):"
say ""
fence
JOURNALD_CONF="${CHAIN_JOURNALD_CONF:-/etc/systemd/journald.conf}"
if [ -r "$JOURNALD_CONF" ]; then
  # Active keys win; when NONE is set (the shipped default) the commented lines
  # are shown, because "#SystemMaxUse=" IS the finding: the journal is
  # size-bounded by journald's built-in default and by nothing else.
  if ! grep -E '^[[:space:]]*(Storage|SystemMaxUse|SystemKeepFree|MaxRetentionSec|MaxFileSec|RuntimeMaxUse)=' "$JOURNALD_CONF" >>"$REPORT" 2>/dev/null; then
    grep -E '^[[:space:]]*#?[[:space:]]*(Storage|SystemMaxUse|SystemKeepFree|MaxRetentionSec|MaxFileSec)=' "$JOURNALD_CONF" 2>/dev/null | head -20 >>"$REPORT"
    say "(every key above is COMMENTED OUT — journald is running on its built-in defaults, so nothing bounds retention by time)"
  fi
else
  say "NOT AVAILABLE: $JOURNALD_CONF is not readable."
fi
unfence
say ""
say "If \`Storage=\` is not \`persistent\` the journal lives in /run and is lost on reboot; if \`MaxRetentionSec=\` is unset the journal is size-bounded only, so a busy GPU worker can rotate a run's lines out before triage. \`Storage=persistent\` plus a retention of at least 24–48 h is the host-side fix; the durable in-repo alternative is the worker's own per-job log (see the worker section), which does not depend on journald at all."
say ""

# ── 10. fingerprint of the report ─────────────────────────────────────
section "9. Raw artifacts"
say "Saved next to this report:"
say ""
fence
ls -l "$RAW" | tail -n +2 | awk '{print $5"  "$9}' >>"$REPORT"
unfence

echo "chain report: $REPORT"
echo "raw artifacts: $RAW"
if [ "$ACCEPTANCE_FAILED" -ne 0 ]; then
  echo "acceptance: FAILED (cold-cache prefetch assertion — see the report's acceptance block)" >&2
  exit 1
fi
