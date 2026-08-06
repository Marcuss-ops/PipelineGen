#!/usr/bin/env bash
# stock_e2e_full_battery.sh — STK-E2E-H aggregator probe for STOCK-E2E-BATTERY-2026-07-05.
#
# Per godlike/06 one-canonical-owner-per-fact + godlike/07 NO-FAKE-AVAILABILITY,
# this wrapper is the canonical operator-facing receipt that the stock pipeline
# (search/direct URL → stage → cut → render → Drive → media_assets → outbox →
# Qdrant → search → download MP4) is end-to-end functional against a live
# PipelineGen server.
#
# Per godlike/07 minimum-blast-radius discipline (smoke probes are diagnostic,
# read-only): the wrapper's CONDITIONAL bookkeeping (flip STOCK-E2E-BATTERY-2026-07-05
# `status: pending → status: shipped + exit_signal: true` on 14/14 PASS) is
# GATED on the `WRAPPER_BOOKKEEPING=1` environment variable. Without that env
# var, the wrapper prints the verdict + the exact git commands for the operator
# to copy-paste (the verifier-only audit-pin pattern per AGENTS.md §Recent
# cross-cutting closures).
#
# Per action plan `architecture/action-plans/2026-07-05-stock-e2e-battery.md`:
#   * 14-point checklist = sum of per-probe sub-assertions across A→G
#     (A: 1 + B: 1 + C: 1 + D: 3 + E: 3 + F: 2 + G: 3 = 14)
#   * Each probe's exit 0 is the canonical receipt that all of its
#     sub-assertions PASS (sub-assertions are encoded inside the probe scripts).
#   * Wave-flip ancestor: STOCK-E2E-BATTERY-2026-07-05 flips to
#     `status: shipped` only when this wrapper exits 0 with 14/14 PASS.
#
# Per godlike/06 SSOT one-canonical-owner-per-fact: the wrapper's auto-bookkeeping
# NEVER mutates git state unless explicitly opted-in via WRAPPER_BOOKKEEPING=1.
# The cleanup-on-PASS trap mirrors the STK-E2E-A/B/C/D/E/F/G precedent (preserve
# artifacts on FAIL for diagnostic inspection; clean tmp only on PASS).
#
# Per AGENTS.md git lessons: direct-to-main commit only — no branch, no PR,
# no --force. Booking-keeping commit (when invoked) is a SEPARATE commit on
# origin/main with the canonical Co-authored-by trailer per AGENTS.md Git-Lesson-3.

set -euo pipefail

# Cleanup-on-PASS trap (mirrors STK-E2E-B precedent).
TMP_DIR="$(mktemp -d /tmp/stock-full-battery.XXXXXX)"
cleanup() {
    local exit_code=$?
    if [ "$exit_code" -eq 0 ]; then
        rm -rf "$TMP_DIR" 2>/dev/null || true
    else
        echo "TMP_DIR preserved at $TMP_DIR for diagnostic inspection (exit $exit_code)" >&2
    fi
}
trap cleanup EXIT

# Constants (godlike/06 SSOT one-canonical-owner-per-fact).
PROBE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAVE_ID="STOCK-E2E-BATTERY-2026-07-05"
ARCHITECTURE_FILE="architecture/current.yaml"
CO_AUTHOR="PipelineGen Agent <agent@pipelinegen.local>"

# Per-probe table: script|point-count (sum = 14).
PROBES=(
    "stock_e2e_route_aliveness_smoke.sh|1|STK-E2E-A|route_aliveness_canonical"
    "stock_e2e_search_and_run_smoke.sh|1|STK-E2E-B|search_and_run_loop_succeeded"
    "stock_e2e_direct_url_smoke.sh|1|STK-E2E-C|direct_url_path_succeeded"
    "stock_e2e_db_assets_smoke.sh|3|STK-E2E-D|media_assets_projection_3_sub_points"
    "stock_e2e_db_outbox_smoke.sh|3|STK-E2E-E|outbox_events_emission_3_sub_points"
    "stock_e2e_unified_search_smoke.sh|2|STK-E2E-F|unified_search_hits_2_sub_points"
    "stock_e2e_download_smoke.sh|3|STK-E2E-G|download_endpoint_3_sub_points"
)
TOTAL_POINTS=14

# ------------------------------------------------------------------------------
# Pre-flight: probes exist on disk (godlike/07 fail-closed at prerequisites).
# ------------------------------------------------------------------------------
echo "=== STK-E2E-H: stock_e2e_full_battery.sh aggregator ==="
echo "    Pre-flight: verify all 7 probe scripts on disk"
echo
missing=0
for entry in "${PROBES[@]}"; do
    IFS='|' read -r fname _ _ _ <<<"$entry"
    if [ ! -f "$PROBE_DIR/$fname" ]; then
        echo "    MISSING: $fname (canonical probe missing from disk)"
        missing=$((missing + 1))
    else
        echo "    OK:      $fname"
    fi
done
if [ "$missing" -gt 0 ]; then
    echo
    echo "FAIL: $missing probe script(s) missing from tests/operational/"
    echo "  Per godlike/07 fail-closed at prerequisites + AGENTS.md Pattern 6"
    echo "  diagnostic-first: the missing probe STK-E2E-<X> must ship BEFORE the"
    echo "  wave-flip can land. Canonical forward-pointer for STK-E2E-F (unified_search):"
    echo "  PR-STOCK-OUTBOX-QDRANT-INDEX (per action plan §4)."
    exit 2
fi
echo
echo "Pre-flight PASS: all 7 probe scripts on disk."
echo

# ------------------------------------------------------------------------------
# Per-probe execution + point tally.
# ------------------------------------------------------------------------------
echo "=== STK-E2E-H: per-probe execution (sequential) ==="
echo
declare -A probe_exit
declare -A probe_points

for entry in "${PROBES[@]}"; do
    IFS='|' read -r fname points tag slug <<<"$entry"
    echo "--- $tag: $fname (${points} sub-assertion(s)) ---"
    set +e
    bash "$PROBE_DIR/$fname"
    exit_code=$?
    set -e
    probe_exit[$tag]="$exit_code"
    probe_points[$tag]="$points"
    if [ "$exit_code" -eq 0 ]; then
        echo "PASS: $tag exit 0 -> ${points} sub-assertion(s) PASS"
    else
        echo "FAIL: $tag exit $exit_code -> 0 sub-assertion(s) PASS (per godlike/07 short-circuit on first failure)"
    fi
    echo
done

# Tally: passed_points = sum of per-probe points where exit 0.
# godlike/06 SSOT invariant: probe sub-assertions are encoded inside the probe
# scripts; the wrapper trusts exit 0 as the canonical receipt for all
# sub-assertions within that probe (per action plan §1 + §3).
passed_points=0
for entry in "${PROBES[@]}"; do
    IFS='|' read -r fname points tag slug <<<"$entry"
    if [ "${probe_exit[$tag]}" -eq 0 ]; then
        passed_points=$((passed_points + points))
    fi
done

# ------------------------------------------------------------------------------
# Canonical receipt markers.
# Each marker is owned by the probe that actually exercises that surface. The
# release gate consumes these exact lines rather than inferring coverage from a
# generic PASS string.
# ------------------------------------------------------------------------------
probe_status() {
    local tag=$1
    if [ "${probe_exit[$tag]}" -eq 0 ]; then
        printf 'PASS'
    else
        printf 'FAIL'
    fi
}

echo "=== STK-E2E-H: receipt coverage ==="
echo "RECEIPT: route=$(probe_status STK-E2E-A)"
echo "RECEIPT: job=$(probe_status STK-E2E-B)"
echo "RECEIPT: outbox=$(probe_status STK-E2E-E)"
echo "RECEIPT: qdrant=$(probe_status STK-E2E-F)"
echo "RECEIPT: mp4=$(probe_status STK-E2E-G)"
echo "RECEIPT: ffprobe=$(probe_status STK-E2E-G)"
echo

# ------------------------------------------------------------------------------
# Verdict.
# ------------------------------------------------------------------------------
echo "=== STK-E2E-H: verdict ==="
echo "    Sub-assertions PASS: $passed_points / $TOTAL_POINTS"
echo

if [ "$passed_points" -eq "$TOTAL_POINTS" ]; then
    echo "VERDICT: 14/14 PASS ($WAVE_ID wave-flip eligible)"
    echo
    echo "Per godlike/07 typed-error contract: the wave-flip requires"
    echo "    (a) ALL 14 sub-assertions PASS on a live PipelineGen server,"
    echo "    (b) commit + push via separate bookkeeping closure commit."
    echo

    # ------------------------------------------------------------------------------
    # Conditional bookkeeping: flip architecture/current.yaml parent entry.
    # Gated on WRAPPER_BOOKKEEPING=1 env var (per godlike/07 minimum-blast-radius).
    # ------------------------------------------------------------------------------
    # Recipe = self-contained bash script with pre-substituted values; the operator
    # (or follow-up agent turn) can copy-paste or `bash $RECIPE_FILE` directly.
    # Single-quoted heredoc ('OUTER_EOF') prevents shell interpolation so the recipe
    # stays literal across the wrapper's invocation; pre-substitution via printf
    # makes the recipe copy-pasteable WITHOUT requiring shell vars set externally.
    RECIPE_FILE="$TMP_DIR/bookkeeping_recipe.sh"
    COMMIT_MSG_FILE="$TMP_DIR/bookkeeping_commit_msg.txt"
    printf '%s\n' \
'#!/usr/bin/env bash' \
'# stock_e2e_full_battery.sh bookkeeping recipe — STK-E2E-H conditional wave-flip.' \
'# Per godlike/06 SSOT one-canonical-owner-per-fact, the wave-flip is a SEPARATE' \
'# bookkeeping closure commit (per action plan §9 Closure discipline).' \
'# Triggered ONLY on 14/14 PASS via tests/operational/stock_e2e_full_battery.sh.' \
'set -euo pipefail' \
'' \
"ARCH_FILE='$ARCHITECTURE_FILE'" \
"WAVE_ID='$WAVE_ID'" \
"CO_AUTHOR='$CO_AUTHOR'" \
'' \
"echo BOOKKEEPING_ARCH_FILE=\"\$ARCH_FILE\"" \
"echo BOOKKEEPING_WAVE_ID=\"\$WAVE_ID\"" \
"echo BOOKKEEPING_CO_AUTHOR=\"\$CO_AUTHOR\"" \
'' \
'# 0. Dry-run verification: dump the actual wave entry block to the operator' \
'#    for visual inspection; FAIL-CLOSED if block is missing or wrong shape.' \
'echo "=== Pre-flight visual dump ==="' \
'rg "^- id: ${WAVE_ID}$" "$ARCH_FILE" -A 20 | head -25 || {' \
'    echo "FAIL: wave-tracker block for $WAVE_ID not found"; exit 1; }' \
'' \
'# 1. Pre-flight: yaml parseable?' \
'python3 -c "import yaml; yaml.safe_load(open(\"$ARCH_FILE\"))" || {' \
'    echo "FAIL: yaml parse error"; exit 1; }' \
'' \
'# 2. Surgical flip: status: pending -> status: shipped; add exit_signal: true.' \
"#    Block in $ARCH_FILE#$WAVE_ID matches the canonical slim shape." \
'python3 <<PY_EOF' \
'import re, sys' \
'from pathlib import Path' \
"p = Path('$ARCH_FILE')" \
"text = p.read_text()" \
"WAVE_ID = '$WAVE_ID'" \
'' \
"# Block detection: the wave entry starts with `- id: WAVE_ID` at column 0." \
"# We extract the block by walking forward from the wave id line to the next" \
"# top-level `- id:` (or EOF). Within that block we substitute status:" \
"# `pending` -> `shipped` and add `exit_signal: true` (only if absent)." \
"lines = text.splitlines(keepends=True)" \
"start_idx, end_idx = None, len(lines)" \
'for i, ln in enumerate(lines):' \
"    if start_idx is None and re.match(r'^-\\s+id:\\s+' + re.escape(WAVE_ID) + r'\\s*\\n?$', ln):" \
'        start_idx = i' \
'        continue' \
'    if start_idx is not None and re.match(r"^-\\s+id:\\s+", ln):' \
'        end_idx = i' \
'        break' \
'if start_idx is None:' \
'    print(f"FAIL: wave-tracker block for {WAVE_ID} not found", file=sys.stderr)' \
'    sys.exit(1)' \
"block = ''.join(lines[start_idx:end_idx])" \
"new_block = re.sub(r'(  )status:\\s+pending\\b', r'\\1status: shipped', block, count=1)" \
'if "exit_signal: true" not in new_block:' \
"    new_block = re.sub(r'(  )status:\\s+shipped\\b', r'\\1status: shipped\\n  exit_signal: true', new_block, count=1)" \
'if new_block == block:' \
"    print(f'FAIL: no flip applied in block for {WAVE_ID} (regex did not match \\'status: pending\\')', file=sys.stderr)" \
'    sys.exit(1)' \
"new_text = ''.join(lines[:start_idx]) + new_block + ''.join(lines[end_idx:])" \
'p.write_text(new_text)' \
"print(f'OK: wave-flip applied in block for {WAVE_ID}: status: pending -> status: shipped + exit_signal: true')" \
'PY_EOF' \
'' \
'# 3. YAML re-parse + verify the flip.' \
"python3 -c \"import yaml; d = yaml.safe_load(open('$ARCH_FILE')); blk = [e for e in d if e.get('id') == '$WAVE_ID'][0]; assert blk['status'] == 'shipped', 'flip did not land'; assert blk.get('exit_signal') is True, 'exit_signal not set'; print('OK: yaml re-parse + flip verified')\"" \
'' \
'# 4. Commit (file-based -F flag per codebase precedent; clean whitespace).' \
"git -c user.email='agent@pipelinegen.local' -c user.name='\$CO_AUTHOR' add \\\$ARCH_FILE" \
"git -c user.email='agent@pipelinegen.local' -c user.name='\$CO_AUTHOR' commit -F \"\$COMMIT_MSG_FILE\"" \
'' \
'# 5. Race-protect per AGENTS.md Git-Lesson-4.' \
'git fetch origin 2>&1 | grep -v "gc.log\|unreachable" || true' \
'log_unpushed=$(git log --oneline HEAD..@{u})' \
'if [ -n "$log_unpushed" ]; then' \
'    echo "FAIL: parallel-agent activity detected; refuse to push."' \
'    echo "$log_unpushed"' \
'    exit 1' \
'fi' \
'' \
'# 6. Push direct-to-main (no branch, no PR, no --force).' \
'git push origin main' \
    > "$RECIPE_FILE"
    chmod +x "$RECIPE_FILE"

    # Commit message template (single-quoted heredoc for literal whitespace).
    cat > "$COMMIT_MSG_FILE" <<'OUTER_EOF'
chore(architecture): STOCK-E2E-BATTERY-2026-07-05 wave-flip closure (bookkeeping commit)

Conditional wave-flip triggered by STK-E2E-H 14/14 PASS per
tests/operational/stock_e2e_full_battery.sh (operator-facing receipt).
Per godlike/07 NO-FAKE-AVAILABILITY: the wave flips ONLY when ALL 14
sub-assertions PASS on a live PipelineGen server.

status: pending -> status: shipped;
exit_signal: true added (canonical slim-shape per action plan §9 Closure discipline).

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
OUTER_EOF

    if [ "${WRAPPER_BOOKKEEPING:-0}" = "1" ]; then
        echo "[WRAPPER_BOOKKEEPING=1] Executing canonical bookkeeping closure recipe..."
        bash "$RECIPE_FILE"
    else
        echo "[WRAPPER_BOOKKEEPING not set] Below is the canonical bookkeeping recipe for the"
        echo "operator/follow-up agent to run as a SEPARATE bookkeeping closure commit (per"
        echo "verifier-only audit-pin pattern per AGENTS.md §Recent cross-cutting closures):"
        echo
        echo "RECIPE FILE: $RECIPE_FILE  (also printed below)"
        echo
        cat "$RECIPE_FILE"
        echo
        echo "Per AGENTS.md §Recent cross-cutting closures (PR-VO-COMPLETION closure precedent):"
        echo "the operator OR a follow-up agent turn runs the recipe above as a SEPARATE"
        echo "bookkeeping closure commit (bash $RECIPE_FILE). The verifier-only pattern keeps"
        echo "smoke probes read-only (no surprise git mutations) while still honoring the user's"
        echo "literal 'via un commit separato di chiusura bookkeeping' directive."
    fi

    exit 0
else
    echo "VERDICT: FAIL ($passed_points/$TOTAL_POINTS sub-assertions PASS; need $TOTAL_POINTS/$TOTAL_POINTS for wave-flip)"
    echo
    echo "Per godlike/07 NO-FAKE-AVAILABILITY: the wave-flip is BLOCKED until ALL 14 sub-assertions PASS."
    echo "Per action plan §4 failure diagnosis table: each probe FAIL maps to a canonical PR-<STOCK>-*"
    echo "forward-pointer that must ship BEFORE the wave-flip can land."
    echo
    echo "Per-probe FAIL signals (per godlike/06 SSOT one-canonical-owner-per-fact):"
    for entry in "${PROBES[@]}"; do
        IFS='|' read -r fname points tag slug <<<"$entry"
        if [ "${probe_exit[$tag]}" -ne 0 ]; then
            case "$tag" in
                STK-E2E-A) echo "  $tag FAIL -> PR-STOCK-ROUTE-REGISTRATION (registry mount seam)" ;;
                STK-E2E-B) echo "  $tag FAIL -> multi-PR mapping (404 route -> PR-STOCK-ROUTE-REGISTRATION; SUCCEEDED unreachable -> PR-STOCK-COMPOSITION-WIRE; job FAILED -> PR-STOCK-STAGER-BOUND; per action plan §4)" ;;
                STK-E2E-C) echo "  $tag FAIL -> PR-STOCK-DIRECT-URLS-FLOW" ;;
                STK-E2E-D) echo "  $tag FAIL -> PR-STOCK-FINALIZER-COMPLETE" ;;
                STK-E2E-E) echo "  $tag FAIL -> PR-STOCK-DELIVERY-RETRY" ;;
                STK-E2E-F) echo "  $tag FAIL -> PR-STOCK-OUTBOX-QDRANT-INDEX" ;;
                STK-E2E-G) echo "  $tag FAIL -> PR-STOCK-DOWNLOAD-RESOLVER" ;;
                *)         echo "  $tag FAIL -> see action plan §4 for canonical PR mapping" ;;
            esac
        fi
    done
    exit 1
fi
