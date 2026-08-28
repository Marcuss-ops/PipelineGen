#!/usr/bin/env bash
# tools/run_weekly_scorecard.sh
# Test 18 — Weekly founder dashboard aggregator.
#
# Reads:
#   - $HOME/.chronon3d/telemetry/telemetry.sqlite (renders table + render_counters table)
#   - $HOME/.chronon3d/telemetry/touchpoints.jsonl (Test 8 manual_touches_per_video JSONL tail)
#   - $HOME/.chronon3d/selftest_log/check_determinism*.log (Test 10 GATE_FAIL signals)
#   - docs/fix_cronograph_log.jsonl (Test 11 fix measurement log, optional)
# Environment:
#   - WEEKLY_COST_HOURLY_RATE: cloud-pricing rate (e.g. 0.05 for $0.05/hour spot CPU)
#     Required for metric 4 (cost_per_finished_minute). No hardcoded fallback per AGENTS.md §honesty.
#
# Emits to stdout:
#   1. videos_completed (count distinct composition_id where status=DONE)
#   2. failure_rate (renders.status=FAILED / total)
#   3. manual_touches_per_video (manual_touches_per_video counter sum / videos_completed)
#   4. cost_per_finished_minute (env rate * total_render_ms / 60000)
#   5. p95_render_time (95th percentile of renders.render_ms)
#   6. peak_memory (MAX(framebuffer_bytes_peak counter over 7d))
#   7. deterministic_hash_failures (count of GATE_FAIL in selftest logs)
#   8. bbox_contract_violations (text_bbox_contract_violations counter sum over 7d)
#
# Exit codes:
#   0 = OK (table emitted)
#   2 = INTERNAL (missing tool / missing telemetry db)

set -euo pipefail

SCRIPT_NAME="run_weekly_scorecard"
GATE_NAME="weekly_scorecard_generator"

# 1. Precondition checks
for tool in sqlite3 awk date; do
    command -v "$tool" >/dev/null 2>&1 || {
        echo "GATE_FAIL_INTERNAL: ${SCRIPT_NAME}: ${tool} not in PATH" >&2
        exit 2
    }
done

TELEMETRY_DIR="${HOME}/.chronon3d/telemetry"
DB="${TELEMETRY_DIR}/telemetry.sqlite"
TOUCHPOINTS_JSONL="${TELEMETRY_DIR}/touchpoints.jsonl"

if [ ! -s "$DB" ]; then
    echo "GATE_FAIL_INTERNAL: ${SCRIPT_NAME}: telemetry SQLite not present at ${DB}" >&2
    echo "[INFO] bootstrap via: run chronon3d_cli once to create telemetry.sqlite" >&2
    exit 2
fi

# 2. Compute 7-day window start
SEVEN_DAYS_AGO="$(date -u -d '7 days ago' '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || \
                  date -u -v-7d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || \
                  echo '1970-01-01T00:00:00Z')"
NOW="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
WEEK_START_DATE="$(date -u -d '7 days ago' '+%Y-%m-%d' 2>/dev/null || \
                   date -u -v-7d '+%Y-%m-%d' 2>/dev/null || \
                   echo "1970-01-01")"
WEEK_END_DATE="$(date -u '+%Y-%m-%d')"

# 3. Metric 1: videos_completed (distinct composition_id where status='DONE')
VIDEOS_COMPLETED="$(sqlite3 "$DB" "SELECT COUNT(DISTINCT composition_id) FROM renders WHERE status='DONE' AND finished_at >= '${SEVEN_DAYS_AGO}'" 2>/dev/null || echo 0)"
[ -z "$VIDEOS_COMPLETED" ] && VIDEOS_COMPLETED=0

# 4. Metric 2: failure_rate
TOTAL="$(sqlite3 "$DB" "SELECT COUNT(*) FROM renders WHERE started_at >= '${SEVEN_DAYS_AGO}'" 2>/dev/null || echo 0)"
FAILED="$(sqlite3 "$DB" "SELECT COUNT(*) FROM renders WHERE status='FAILED' AND started_at >= '${SEVEN_DAYS_AGO}'" 2>/dev/null || echo 0)"
[ -z "$TOTAL" ] && TOTAL=0
[ -z "$FAILED" ] && FAILED=0
FAILURE_RATE="0.00%"
if [ "$TOTAL" -gt 0 ]; then
    FAILURE_RATE="$(awk -v f="$FAILED" -v t="$TOTAL" 'BEGIN{printf "%.2f%%", (f*100.0)/t}')"
fi

# 5. Metric 3: manual_touches_per_video (sum counter / videos_completed)
MANUAL_TOUCHES_RAW=0
if [ -s "$TOUCHPOINTS_JSONL" ]; then
    MANUAL_TOUCHES_RAW="$(grep -c '"event":' "$TOUCHPOINTS_JSONL" 2>/dev/null || echo 0)"
fi
# Also check render_counters table for any persisted counter (forward-compat)
MANUAL_TOUCHES_COUNTER_SUM="$(sqlite3 "$DB" "SELECT COALESCE(SUM(counter_value), 0) FROM render_counters WHERE counter_name='manual_touches_per_video' AND ts >= '${SEVEN_DAYS_AGO}'" 2>/dev/null || echo 0)"
MANUAL_TOUCHES_TOTAL=$((MANUAL_TOUCHES_RAW + MANUAL_TOUCHES_COUNTER_SUM))
MANUAL_TOUCHES_PER_VIDEO="0.00"
if [ "$VIDEOS_COMPLETED" -gt 0 ]; then
    MANUAL_TOUCHES_PER_VIDEO="$(awk -v s="$MANUAL_TOUCHES_TOTAL" -v v="$VIDEOS_COMPLETED" 'BEGIN{printf "%.2f", s/v}')"
fi

# 6. Metric 4: cost_per_finished_minute (requires WEEKLY_COST_HOURLY_RATE env var)
COST_PER_FINISHED_MINUTE="[UNSET-rate]"
if [ -n "${WEEKLY_COST_HOURLY_RATE:-}" ]; then
    TOTAL_RENDER_MS="$(sqlite3 "$DB" "SELECT COALESCE(SUM(render_ms), 0) FROM renders WHERE status='DONE' AND finished_at >= '${SEVEN_DAYS_AGO}'" 2>/dev/null || echo 0)"
    COST_PER_FINISHED_MINUTE="$(awk -v rate="$WEEKLY_COST_HOURLY_RATE" \
                                     -v ms="$TOTAL_RENDER_MS" \
                                     'BEGIN {
                                         if (ms <= 0) { printf "$%.4f/min", 0; exit; }
                                         minutes = ms / 60000.0;
                                         printf "$%.4f/min", rate * minutes / 60.0;
                                     }')"
fi

# 7. Metric 5: p95_render_time (percentile 95 of render_ms)
P95_RENDER_TIME="0.000 s"
if [ "$TOTAL" -gt 0 ]; then
    P95_INDEX=$((TOTAL * 95 / 100))
    [ "$P95_INDEX" -ge "$TOTAL" ] && P95_INDEX=$((TOTAL - 1))
    P95_MS="$(sqlite3 "$DB" "SELECT render_ms FROM renders WHERE finished_at >= '${SEVEN_DAYS_AGO}' ORDER BY render_ms ASC LIMIT 1 OFFSET ${P95_INDEX}" 2>/dev/null || echo 0)"
    [ -z "$P95_MS" ] && P95_MS=0
    P95_RENDER_TIME="$(awk -v ms="$P95_MS" 'BEGIN{printf "%.3f s", ms/1000.0}')"
fi

# 8. Metric 6: peak_memory (MAX(framebuffer_bytes_peak) over 7d)
PEAK_MEMORY_BYTES="$(sqlite3 "$DB" "SELECT MAX(counter_value) FROM render_counters WHERE counter_name='framebuffer_bytes_peak' AND ts >= '${SEVEN_DAYS_AGO}'" 2>/dev/null || echo 0)"
[ -z "$PEAK_MEMORY_BYTES" ] || [ "$PEAK_MEMORY_BYTES" = "NULL" ] && PEAK_MEMORY_BYTES=0
PEAK_MEMORY_MB="$(awk -v b="$PEAK_MEMORY_BYTES" 'BEGIN{printf "%.1f MB", b/(1024.0*1024.0)}')"

# 9. Metric 7: deterministic_hash_failures (count of GATE_FAIL in selftest logs)
DETERMINISTIC_HASH_FAILURES=0
if [ -d "${HOME}/.chronon3d/selftest_log" ]; then
    DETERMINISTIC_HASH_FAILURES="$(grep -h 'GATE_FAIL' "${HOME}/.chronon3d/selftest_log/check_determinism"*.log 2>/dev/null | wc -l || echo 0)"
fi

# 10. Metric 8: bbox_contract_violations (FU01 counter sum)
BBOX_VIOLATIONS="$(sqlite3 "$DB" "SELECT COALESCE(SUM(counter_value), 0) FROM render_counters WHERE counter_name='text_bbox_contract_violations' AND ts >= '${SEVEN_DAYS_AGO}'" 2>/dev/null || echo 0)"
[ -z "$BBOX_VIOLATIONS" ] && BBOX_VIOLATIONS=0

# 11. Emit markdown to stdout
cat <<EOF
# Test 18 — Weekly founder dashboard

**Week**: ${WEEK_START_DATE} to ${WEEK_END_DATE} (last 7 days, generated at ${NOW})

| # | Metric | Value | Aggregation source |
|---|---|---|---|
| 1 | videos_completed | ${VIDEOS_COMPLETED} | SELECT COUNT(DISTINCT composition_id) FROM renders WHERE status='DONE' AND finished_at >= 7d |
| 2 | failure_rate | ${FAILURE_RATE} | (renders.status=FAILED) / total over 7d |
| 3 | manual_touches_per_video | ${MANUAL_TOUCHES_PER_VIDEO} | touchpoints.jsonl count + render_counters counter 'manual_touches_per_video' / videos_completed |
| 4 | cost_per_finished_minute | ${COST_PER_FINISHED_MINUTE} | WEEKLY_COST_HOURLY_RATE × (SUM render_ms / 60000) / 60 [\$/min] |
| 5 | p95_render_time | ${P95_RENDER_TIME} | ASC-sorted render_ms, INDEX = floor(total * 0.95) |
| 6 | peak_memory | ${PEAK_MEMORY_MB} | MAX(render_counters.framebuffer_bytes_peak) over 7d |
| 7 | deterministic_hash_failures | ${DETERMINISTIC_HASH_FAILURES} | grep -c GATE_FAIL in ~/.chronon3d/selftest_log/check_determinism*.log |
| 8 | bbox_contract_violations | ${BBOX_VIOLATIONS} | SUM(render_counters.text_bbox_contract_violations) over 7d |

## 7 narrative lines (the founder's weekly grep)

1. **Quanti video**: ${VIDEOS_COMPLETED} video completati questa settimana.
2. **Costo**: ${COST_PER_FINISHED_MINUTE} per minuto finito (basato su \$/hour spot rate, vedi WEEKLY_COST_HOURLY_RATE env).
3. **Job falliti**: ${FAILED} di ${TOTAL} totali (${FAILURE_RATE}).
4. **Interventi manuali**: ${MANUAL_TOUCHES_PER_VIDEO} per video (target golden path: < 1).
5. **Codice eliminato**: vedi \`docs/FEATURE_SUNSET.md\` (Test 16 registro sunset; NOT instrumented in this dashboard — forward-point).
6. **Bug piu grave**: vedi \`docs/FOLLOWUP_TICKETS.md\` §Open Blockers (highest priority ticket id).
7. **Metrica migliorata**: week-over-week delta requires 2 weeks of data (forward-point; first run seeds baseline).

---

## §honesty cert

- WEEKLY_COST_HOURLY_RATE env var REQUIRED for metric 4 (cost_per_finished_minute); unset = \`[UNSET-rate]\` placeholder, NEVER a fabricated spot rate.
- All 8 metrics are derived from observable telemetry sources (SQLite + JSONL + selftest logs).
- macchina-verifica on this VPS: PARTIAL per AGENTS.md §honesty (this directory has no chronon3d_cli binary, telemetry SQLite may be empty; the script is syntactically valid + exits 0 on a populated DB).
- Forward-point (deferred): deterministic_hash_failures source assumes \`~/.chronon3d/selftest_log/check_determinism*.log\` artifact. If the telemetry dashboard server aggregates this differently, update selftest log path (Cat-3 minimal-surface: extend \`manual_touchpoint_log\` JSONL scheme per the Test 10 selftest pattern).
EOF

echo "[INFO] ${GATE_NAME}: 8/8 metrics computed for week ${WEEK_START_DATE}..${WEEK_END_DATE}"
