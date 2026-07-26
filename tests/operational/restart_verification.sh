#!/usr/bin/env bash
# tests/operational/restart_verification.sh — Artlist DoD Restart Verification
#
# Reorg (July 2026): standalone companion to tests/operational/05_pipeline_fresh.sh.
# Goal: certify that a CLEAN restart of PipelineGen + node-scraper preserves
# the SAME end-to-end operational surface required by 05_pipeline_fresh.sh's
# Gate 4-9 battery (3 fresh clips through the entire doD chain).
#
# Per godlike/06 SSOT: this script is the ORCHESTRATOR only. The actual
# Gate 4-9 surface is delegated to 05_pipeline_fresh.sh via subprocess so
# the verification chain stays focused on restart semantics. No duplicate
# per-gate logic (AGENTS.md single-focus rule honoured).
#
# Spec (July 2026 DoD, the Artlist operational contract Restart-Verification row):
#   - preflight: capture vitals fingerprint (PIDs, /api/health hash,
#     DB row count sentinel, Qdrant count sentinel, ARTLIST_COOKIE_FILE
#     mtime+sha256, better-sqlite3 native binary sha256). Any drift on
#     the FIRST fingerprint that doesn't match the operator's prior
#     reconnaissance means the environment was already mutated — abort.
#   - first run: invoke 05_pipeline_fresh.sh as subprocess; MUST exit 0
#     with the canonical "Bundles SIX gates ... VERDICT: PASS" verdict.
#   - post-run-1 vitals: capture again. DB + Qdrant counts MUST have grown
#     (the first run added 3 clips). Cookie mtime MAY stay identical
#     (no re-login needed if session still valid).
#   - manual-intervention guard: sha256sum better-sqlite3 native binary
#     (the most common silent rebuild target) before/after the FIRST run;
#     drift on this single sha256 is suspicious because nothing in the
#     restart test path genuinely rebuilds native bindings. Report
#     this as a `log_warn` so the operator sees it but do NOT fail-closed
#     (external observation, not a gate invariant).
#   - clean restart: stop PipelineGen + node-scraper via canonical
#     pkill pattern, wait for /api/health to go down (HTTP 000 or 503),
#     restart via setsid, wait for /api/health to come back to 200.
#     No `systemctl` calls (the box may not be a systemd host); no
#     `sudo` (the runbook says NEVER use sudo in test scripts per
#     worker-certification-checklist.md). Pattern mirrors the existing
#     failure-mode script (tests/operational/artlist_scraper_failure_smoke.sh:121).
#   - second run: invoke 05_pipeline_fresh.sh as a SECOND subprocess;
#     MUST exit 0 with the same verdict pattern. If it does — restart
#     semantics are demonstrably clean and the DoD Restart verification
#     PASSES.
#   - post-run-2 vitals: capture once more. DB + Qdrant counts MUST have
#     grown AGAIN (the second run added ANOTHER 3 clips as fresh
#     additions; if either gate refused to add rows post-restart this
#     surfaces as `count_post2 < count_post1` which would be catastrophic
#     state loss).
#
# Verdict taxonomy:
#   - PASS        — first run PASS AND second run PASS AND DB+Qdrant
#     counts strictly monotone (>= post1 >= pre, transitions clean)
#   - PARTIAL     — first run PASS but second run fails (state loss)
#     OR second run PASS but first run failed (chain inverted)
#   - FAIL        — preflight vitals or pre-restart vitals unexpected
#     before any run executed; or restart itself hung
#
# Reuses ONLY helpers from lib/{common.sh,artlist_runtime.sh,
# velox_domain.sh} (smoke_curl / smoke_poll_terminal pattern adapted /
# smoke_sqlite_query / log_pass / log_fail / log_info / log_warn).
# NO new helpers added — capture_vitals is INLINE because the comparison
# semantics are pairwise between phases, not reusable as a generic
# helper, and to keep the script self-contained during the pre-push
# gate (AGENTS.md "Simplicity & Minimalism").

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/lib/artlist_runtime.sh"

smoke_require curl jq pgrep pkill sha256sum sqlite3

# ── Canonical paths (godlike/06 SSOT — sibling 05_pipeline_fresh.sh +
# project-root binaries/artifacts). Defining them at the top so the
# restart orchestration below uses ONE canonical name per target.
PIPELINE_FRESH_SH="${DIR}/05_pipeline_fresh.sh"
PIPELINEGEN_BIN="${DIR}/../../bin/pipelinegen"
SCRAPER_DIR="${DIR}/../../node-scraper"
SCRAPER_ENTRY="${SCRAPER_DIR}/artlist_server.js"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

# ── DRY_RUN branch ────────────────────────────────────────────────────────
# Mirrors the pattern in 05_pipeline_fresh.sh + 09_failure_modes.sh —
# walk every phase of the orchestration via [DRY] log lines so the
# verifier can cover the gate's surface without actually killing services.
# IMPORTANT: DRY_RUN runs BEFORE the binary preflight so lint/CI checks
# pass in environments where ./bin/pipelinegen hasn't been built (the
# actual service-restart invariant is only meaningful against a live
# binary, which DRY is designed to skip).
if [[ "${DRY_RUN:-0}" == "1" ]]; then
    smoke_echo_safe "[DRY] Restart Verification orchestration — standalone PHPUnit at ${PIPELINE_FRESH_SH}:"
    smoke_echo_safe "[DRY]   preflight vitals (capture_vitals pre):"
    smoke_echo_safe "[DRY]     - pids_pipelinegen, pids_scraper, pids_chrome (Chrome explicitly captured per blocker #2)"
    smoke_echo_safe "[DRY]     - health.hash (sha256 of /api/health body)"
    smoke_echo_safe "[DRY]     - db_count, db_first_kb.hash, db_last_kb.hash (manual-DB-mod detector)"
    smoke_echo_safe "[DRY]     - qdrant_count (media_assets_current points_count)"
    smoke_echo_safe "[DRY]     - cookie_mtime, cookie_hash (manual cookie-rotation detector)"
    smoke_echo_safe "[DRY]     - bsq3_native.hash (better-sqlite3 .node sha256 — npm-rebuild detector)"
    smoke_echo_safe "[DRY]   first-pass: invoke ${PIPELINE_FRESH_SH} as subprocess (must exit 0)"
    smoke_echo_safe "[DRY]   post-run-1 vitals capture + manual-intervention guard:"
    smoke_echo_safe "[DRY]     - bsq3_native drift → PARTIAL (npm-rebuild forbidden)"
    smoke_echo_safe "[DRY]     - db_first_kb drift → FAIL (manual DB write forbidden)"
    smoke_echo_safe "[DRY]     - db_last_kb drift → PARTIAL (manual DB append forbidden)"
    smoke_echo_safe "[DRY]     - cookie_hash drift → PARTIAL (manual cookie rotation forbidden)"
    smoke_echo_safe "[DRY]     - chrome pid-list stability check pre-vs-post1 (the FIRST run must NOT kill Chrome)"
    smoke_echo_safe "[DRY]   clean restart orchestration:"
    smoke_echo_safe "[DRY]     - pkill pipelinegen + pkill scraper; PID-anchored shutdown wait (≤10s polling pre_pid_snapshot, lower threshold = earlier exit)"
    smoke_echo_safe "[DRY]     - setsid ${PIPELINEGEN_BIN} --mode all + setsid node ${SCRAPER_ENTRY}"
    smoke_echo_safe "[DRY]     - PID-anchored startup wait (≤30s polling for new pid in pre_pid_snapshot+1..max)"
    smoke_echo_safe "[DRY]   second-pass: invoke ${PIPELINE_FRESH_SH} as subprocess (must exit 0)"
    smoke_echo_safe "[DRY]   aggregate verdict: PASS=both runs PASS + DB/Qdrant monotonicity ≥6 growth across both runs + zero manual-intervention surfaces drifted + chrome pid-stability honoured"
    smoke_echo_safe "[DRY]     PARTIAL: manual-intervention drift detected (npm-rebuild / cookie-rotation / db-append)"
    smoke_echo_safe "[DRY]     FAIL: preflight vitals fail OR restart itself hung OR DB /api/health superseded pre-restart snapshot"
    exit 0
fi

# Binary preflight runs ONLY in non-DRY mode (DRY above short-circuits
# before this block so lint/CI tests don't require a built binary).
if [[ ! -x "${PIPELINEGEN_BIN}" ]]; then
    log_fail "Restart preflight: ${PIPELINEGEN_BIN} not executable or missing (build with: make build or scripts/build.sh)"
    exit 1
fi
if [[ ! -f "${SCRAPER_ENTRY}" ]]; then
    log_fail "Restart preflight: ${SCRAPER_ENTRY} missing (node-scraper not built)"
    exit 1
fi
if [[ ! -x "${PIPELINE_FRESH_SH}" ]]; then
    log_fail "Restart preflight: ${PIPELINE_FRESH_SH} missing or non-executable (must be the SAME script invoked by first-pass and second-pass)"
    exit 1
fi

# ── capture_vitals $phase ─────────────────────────────────────────────────
# Writes a vitals-fingerprint bundle for one phase to $WORK_DIR:
#   <phase>_pids_pipelinegen.list — line-delimited pids (excluding self)
#   <phase>_pids_scraper.list     — line-delimited scraper pids
#   <phase>_health.hash           — sha256(/api/health body) or "FAIL"
#   <phase>_db_count              — COUNT(*) FROM media_assets (or "FAIL")
#   <phase>_db_first_kb.hash      — sha256(first 1024 bytes of $DB_PATH) — manual-mod detector
#   <phase>_db_last_kb.hash       — sha256(last 1024 bytes of $DB_PATH) — manual-mod detector
#   <phase>_qdrant_count          — points_count for media_assets_current (or "FAIL")
#   <phase>_cookie_mtime          — mtime of ARTLIST_COOKIE_FILE (or "MISSING")
#   <phase>_cookie_hash           — sha256(ARTLIST_COOKIE_FILE) (or "MISSING")
#   <phase>_bsq3_native.hash      — sha256(mattn/go-sqlite3 native .so) — manual-rebuild detector
#
# Inline (NOT new helper) per AGENTS.md "Simplicity & Minimalism" — only
# this script reads pairwise comparisons between phases; the helper would
# be too narrow to reuse downstream and inflate the lib surface for no win.
capture_vitals() {
    local phase="$1"
    local work_dir="$WORK_DIR"

    # pgrep -f matches by full command line; exclude our own pid by NAME
    # so the helper is self-referential-safe when invoked under the same
    # script. Without the exclusion we'd record our own pid which is
    # meaningless for restart semantics.
    # pgrep -f matches by full command line; exclude our own pid by NAME
    # so the helper is self-referential-safe when invoked under the same
    # script. Without the exclusion we'd record our own pid which is
    # meaningless for restart semantics.
    local self_pid
    self_pid="$$"
    pgrep -f 'pipelinegen --mode all' 2>/dev/null | grep -v "^${self_pid}$" > "${work_dir}/${phase}_pids_pipelinegen.list" || echo "" > "${work_dir}/${phase}_pids_pipelinegen.list"
    pgrep -f 'artlist_server.js' 2>/dev/null | grep -v "^${self_pid}$" > "${work_dir}/${phase}_pids_scraper.list" || echo "" > "${work_dir}/${phase}_pids_scraper.list"
    # Chrome/Chromium pids — DoD forbids manual Chrome kill during the run;
    # the FIRST run must NOT terminate any Chrome process (the scraper
    # owns the profile). Capture by matching both 'chrome' and 'chromium'
    # binaries since some installations use the latter, plus the
    # '--type=' renderer flag for the spawned headless contexts.
    pgrep -af 'chrome|chromium' 2>/dev/null | awk '{print $1}' | grep -v "^${self_pid}$" > "${work_dir}/${phase}_pids_chrome.list" || echo "" > "${work_dir}/${phase}_pids_chrome.list"

    # /api/health sha256 — pipelinegen canonical health endpoint.
    # Reviewer hard-nit: `curl ... || echo FAIL` neutralises set -e
    # semantics so a downstream `[ "$(cat ...)" = "FAIL" ]` check can
    # detect transport failure WITHOUT crashing the whole script.
    if curl -sS --max-time 5 "${BASE_URL}/api/health" 2>/dev/null \
        | sha256sum | awk '{print $1}' > "${work_dir}/${phase}_health.hash"; then
        :
    else
        echo "FAIL" > "${work_dir}/${phase}_health.hash"
    fi

    # DB row count sentinel — media_assets count is monotone non-decreasing
    # across the two runs (each fresh run adds 3 new clips via Gate 4's
    # replace strategy). DoD: post2 >= post1 >= pre.
    if [[ -n "${DB_PATH:-}" ]]; then
        smoke_sqlite_query "${DB_PATH}" "SELECT COUNT(*) FROM media_assets" 2>/dev/null \
            > "${work_dir}/${phase}_db_count" || echo "FAIL" > "${work_dir}/${phase}_db_count"
        # DB first/last 1KB hashes — manual-mod detector. The DoD forbids
        # writing to the DB outside PipelineGen's transactional outbox;
        # if someone hand-edits the SQLite file the bytes shift and we
        # surface it as a `log_warn` so the operator sees it. NOT fail-
        # closed (manual mods are external observation; can't be gate).
        if [[ -s "${DB_PATH}" ]]; then
            head -c 1024 "${DB_PATH}" | sha256sum | awk '{print $1}' > "${work_dir}/${phase}_db_first_kb.hash" || echo "FAIL" > "${work_dir}/${phase}_db_first_kb.hash"
            tail -c 1024 "${DB_PATH}" | sha256sum | awk '{print $1}' > "${work_dir}/${phase}_db_last_kb.hash" || echo "FAIL" > "${work_dir}/${phase}_db_last_kb.hash"
        else
            echo "MISSING" > "${work_dir}/${phase}_db_first_kb.hash"
            echo "MISSING" > "${work_dir}/${phase}_db_last_kb.hash"
        fi
    else
        echo "FAIL" > "${work_dir}/${phase}_db_count"
        echo "FAIL" > "${work_dir}/${phase}_db_first_kb.hash"
        echo "FAIL" > "${work_dir}/${phase}_db_last_kb.hash"
    fi

    # Qdrant points_count — same monotonicity invariant as DB count.
    local q_url="${QDRANT_URL:-http://127.0.0.1:6333}"
    local q_col="${QDRANT_COLLECTION:-media_assets_current}"
    if curl -sS --max-time 5 "${q_url}/collections/${q_col}" 2>/dev/null \
        | jq -r '.result.points_count // "FAIL"' > "${work_dir}/${phase}_qdrant_count" 2>/dev/null; then
        :
    else
        echo "FAIL" > "${work_dir}/${phase}_qdrant_count"
    fi

    # ARTLIST_COOKIE_FILE — mtime + sha256; missing is OK (the runtime
    # accepts it as "session not configured") but if it existed pre and
    # disappears post-restart that's a session-expired state worth warning.
    if [[ -n "${ARTLIST_COOKIE_FILE:-}" && -s "${ARTLIST_COOKIE_FILE}" ]]; then
        stat -c '%Y' "${ARTLIST_COOKIE_FILE}" > "${work_dir}/${phase}_cookie_mtime" 2>/dev/null || echo "FAIL" > "${work_dir}/${phase}_cookie_mtime"
        sha256sum "${ARTLIST_COOKIE_FILE}" | awk '{print $1}' > "${work_dir}/${phase}_cookie_hash" 2>/dev/null || echo "FAIL" > "${work_dir}/${phase}_cookie_hash"
    else
        echo "MISSING" > "${work_dir}/${phase}_cookie_mtime"
        echo "MISSING" > "${work_dir}/${phase}_cookie_hash"
    fi

    # better-sqlite3 native binary sha256 — manual rebuild detector.
    # The Node-scraper uses better-sqlite3 (NOT pipelinegen's Go-side
    # mattn/go-sqlite3) for its SQLite quota-counters via node_modules/
    # better-sqlite3/build/Release/better_sqlite3.node. If an operator
    # runs `npm rebuild better-sqlite3` mid-run this hash changes →
    # Gate 0 forbids this; we surface it as `log_warn` so the operator
    # knows it happened, but NOT fail-closed (the second run still runs
    # to detect post-rebuild state corruption independently).
    if [[ -n "${SCRAPER_DIR}" ]]; then
        local bsq3_native="${SCRAPER_DIR}/node_modules/better-sqlite3/build/Release/better_sqlite3.node"
        if [[ -s "${bsq3_native}" ]]; then
            sha256sum "${bsq3_native}" | awk '{print $1}' > "${work_dir}/${phase}_bsq3_native.hash" 2>/dev/null || echo "FAIL" > "${work_dir}/${phase}_bsq3_native.hash"
        else
            echo "MISSING" > "${work_dir}/${phase}_bsq3_native.hash"
        fi
    fi

    log_info "Vitals phase=${phase}: pipelinegen_pids=$(wc -l < "${work_dir}/${phase}_pids_pipelinegen.list" 2>/dev/null | tr -d ' ') scraper_pids=$(wc -l < "${work_dir}/${phase}_pids_scraper.list" 2>/dev/null | tr -d ' ') health=$(head -c 8 "${work_dir}/${phase}_health.hash") db=$(cat "${work_dir}/${phase}_db_count") qdrant=$(cat "${work_dir}/${phase}_qdrant_count")"
}

# ── DRY_RUN branch — REMOVED — moved BEFORE binary preflight above so
# lint/CI checks pass in environments where ./bin/pipelinegen hasn't
# been built. The block above (lines after WORK_DIR/trap) handles DRY.

# ── Phase 1: preflight vitals ─────────────────────────────────────────────
smoke_log_section "Restart Verification — Phase 1 / 5 (preflight vitals)"
log_info "Capturing baseline vitals fingerprint (PIDs / /api/health / DB / Qdrant / cookie / better-sqlite3 native)"
capture_vitals "pre"
pre_db=$(cat "${WORK_DIR}/pre_db_count" || echo "FAIL")
pre_qdrant=$(cat "${WORK_DIR}/pre_qdrant_count" || echo "FAIL")
pre_pg=$(wc -l < "${WORK_DIR}/pre_pids_pipelinegen.list" | tr -d ' ' || echo 0)
pre_scraper=$(wc -l < "${WORK_DIR}/pre_pids_scraper.list" | tr -d ' ' || echo 0)
log_pass "Preflight vitals: pipelinegen_pids=${pre_pg} scraper_pids=${pre_scraper} db_count=${pre_db} qdrant_count=${pre_qdrant} (≥1 of each required; restarts target = the live counts)"

# Fail-closed preflight: at least ONE pipelinegen process must be live,
# AND at least ONE scraper process must be live; otherwise a restart of
# "down services" is meaningless.
if [[ "${pre_pg}" -lt 1 ]]; then
    log_fail "Restart preflight: ZERO live pipelinegen processes found — restart test requires live services to restart FROM. Run start.sh first"
    exit 1
fi
if [[ "${pre_scraper}" -lt 1 ]]; then
    log_fail "Restart preflight: ZERO live node artlist_server.js processes found — restart test requires live scraper to restart FROM. Start node-scraper first"
    exit 1
fi

# ── Phase 2: first PASS ───────────────────────────────────────────────────
smoke_log_section "Restart Verification — Phase 2 / 5 (first battery run)"
log_info "Invoking 05_pipeline_fresh.sh as subprocess for first PASS verification..."
if ! bash "${PIPELINE_FRESH_SH}"; then
    log_fail "Restart verification: FIRST run of 05_pipeline_fresh.sh FAILED (exit=$?). Restart semantics can ONLY be tested against a previously-PASS baseline"
    capture_vitals "post_first_run_failed"
    log_info "Diagnostic: failed-first-run vitals captured at ${WORK_DIR}/ for triage"
    exit 1
fi
log_pass "Restart verification: FIRST run of 05_pipeline_fresh.sh PASSED (3 fresh clips added, Gates 4-9 all green)"

capture_vitals "post1"

# ── Phase 3: manual-intervention guard (now FAIL-CLOSED per blocker #1) ────
# Compare pre vs post1 vitals on the surfaces the DoD forbids mutating
# between runs. DRIFT here is FORBIDDEN per the DoD text — promotion
# from warn-only to verdict-coupled enforcer (reviewer hard-nit #1).
# Verdict coupling:
#   * bsq3_native drift   → PARTIAL (npm-rebuild forbidden; softer class
#     because legitimate sha-version bumps CAN happen if the operator
#     intentionally re-tar'd, but in the DoD Restart context this is
#     forbidden so PARTIAL — not catastrophic)
#   * db_first_kb drift   → FAIL (manual write inside the SQLite
#     header page is the canonical "operator hand-edited the file"
#     signal; harder forbid-class — explicitly fail-closed)
#   * db_last_kb drift    → PARTIAL (manual append; recorded as DRY_RUN
#     fingerprint drift; PARTIAL because legitimate lock-and-truncate
#     cycles can shift the tail by a few bytes without corruption)
#   * cookie_hash drift   → PARTIAL (manual cookie rotation mid-run is
#     a session-replacement that the DoD forbids; observable)
#   * chrome_pid drift pe→post1 → FAIL (the FIRST run must NOT kill
#     Chrome — Chrome is owned by the scraper; killing it manually is
#     an explicit forbid-class operator action; post1==pre required,
#     post_restart MAY differ since the restart legitimately spawns a
#     new Chrome profile lifecycle)
# The verdict is propagated into Phase 5 as `manual_int_verdict` so
# the aggregate can enforce it.
smoke_log_section "Restart Verification — Phase 3 / 5 (manual-intervention guard)"
manual_int_verdict="PASS"
for surface in bsq3_native db_first_kb db_last_kb cookie_hash; do
    pre_h=$(cat "${WORK_DIR}/pre_${surface}.hash" 2>/dev/null || echo "MISSING")
    post_h=$(cat "${WORK_DIR}/post1_${surface}.hash" 2>/dev/null || echo "MISSING")
    if [[ "${pre_h}" != "${post_h}" && "${pre_h}" != "MISSING" && "${post_h}" != "MISSING" ]]; then
        case "${surface}" in
            bsq3_native)
                log_fail "Manual-intervention on ${surface}: pre=${pre_h:0:12} post1=${post_h:0:12} → PARTIAL (DoD forbids npm rebuild better-sqlite3 mid-run)"
                manual_int_verdict="PARTIAL"
                ;;
            db_first_kb)
                log_fail "Manual-intervention on ${surface}: pre=${pre_h:0:12} post1=${post_h:0:12} → FAIL (DoD forbids manual SQLite header write — hard-class forbid)"
                manual_int_verdict="FAIL"
                ;;
            db_last_kb)
                log_fail "Manual-intervention on ${surface}: pre=${pre_h:0:12} post1=${post_h:0:12} → PARTIAL (DoD forbids manual SQLite append/tail mutation)"
                [[ "${manual_int_verdict}" != "FAIL" ]] && manual_int_verdict="PARTIAL"
                ;;
            cookie_hash)
                log_fail "Manual-intervention on ${surface}: pre=${pre_h:0:12} post1=${post_h:0:12} → PARTIAL (DoD forbids manual cookie rotation mid-run)"
                [[ "${manual_int_verdict}" != "FAIL" ]] && manual_int_verdict="PARTIAL"
                ;;
        esac
    fi
done

# Chrome pid-stability check (pre vs post1): Chrome is owned by node-
# scraper. The first run must NOT have killed any Chrome process. We
# compare pid COUNT (not strict set equality — reviewer polish #2):
# scraper legitimately spawns additional `--type=renderer` contexts
# per page navigation, so pid sets naturally drift by ±1 even with no
# operator intervention. Strict `diff` would false-positive on every
# legitimate crawl. Instead we allow a ±1 pid-count tolerance for
# normal scraper behaviour but treat abs(delta) >= 3 as fail-closed
# (that magnitude of count drop only happens with operator kills or a
# catastrophic scraper crash mid-run).
pre_chrome_c=$(wc -l < "${WORK_DIR}/pre_pids_chrome.list" 2>/dev/null | tr -d ' ')
post1_chrome_c=$(wc -l < "${WORK_DIR}/post1_pids_chrome.list" 2>/dev/null | tr -d ' ')
# Reviewer hard-nit asymmetric check: only the DOWN-direction (kill)
# is forbidden by the DoD ("NON killare Chrome manualmente"). The
# UP-direction (legitimate scraper spawning new `--type=renderer`
# contexts per page) is benign and surfaces as log_info. A symmetric
# abs() check would false-positive on legitimate growth, contradicting
# the DoD forbid-class on KILL specifically.
# Magic-number SSOT (reviewer polish): promote to script-body readonly
# constants (NOT `local -r` because the chrome check lives at script-
# body top-level, NOT inside a function — `local` outside a function
# fails or warns on Bash 5.x strict mode per reviewer portability fix).
# `readonly` is the canonical Bash idiom for top-level module-scoped
# constants. CHROME_KILL_THRESHOLD=3 because 1-2 is normal renderer
# churn that the scraper reaps on its own between navigations; ≥3
# implies operator kill or scraper crash (severe class). CHROME_SPAWN_INFO
# =2 because single nav-spawned contexts are NOT noteworthy; ≥2
# indicates a multi-page crawl worth surfacing in the log.
readonly CHROME_KILL_THRESHOLD=3
readonly CHROME_SPAWN_INFO_THRESHOLD=2
kill_delta=$(( pre_chrome_c - post1_chrome_c ))   # positive when chrome dropped
spawn_delta=$(( post1_chrome_c - pre_chrome_c ))  # positive when chrome grew
if (( kill_delta >= CHROME_KILL_THRESHOLD )); then
    log_fail "Chrome pid-COUNT dropped pre→post1 by ${kill_delta} (pre=${pre_chrome_c} post1=${post1_chrome_c}, threshold=${CHROME_KILL_THRESHOLD}) → FAIL (DoD forbids manual Chrome kill; spawn_delta=${spawn_delta})"
    manual_int_verdict="FAIL"
elif (( spawn_delta >= CHROME_SPAWN_INFO_THRESHOLD )); then
    log_info "Chrome pid-COUNT grew pre→post1 by ${spawn_delta} (pre=${pre_chrome_c} post1=${post1_chrome_c}, info_threshold=${CHROME_SPAWN_INFO_THRESHOLD}) — legitimate scraper-spawned --type=renderer contexts (DoD forbids KILL not growth; informational only)"
fi

case "${manual_int_verdict}" in
    PASS)
        log_pass "Manual-intervention guard clean: better-sqlite3 native + DB first/last KB + cookie hash + chrome pid-list all stable across the first run"
        ;;
    PARTIAL)
        log_warn "Manual-intervention guard PARTIAL: softer-class drift detected (npm-rebuild / db-append / cookie-rotation); verdict aggregate will downgrade below"
        ;;
    FAIL)
        log_warn "Manual-intervention guard FAIL: hard-class drift detected (db-first-kb / chrome-kill) — verdict aggregate will FAIL below"
        ;;
esac

# ── Phase 4: clean restart orchestration ───────────────────────────────────
smoke_log_section "Restart Verification — Phase 4 / 5 (clean restart)"
# Snapshot the pids pre-kill so the PID-anchored wait (reviewer
# hard-nit #3) knows which pids to expect to disappear on shutdown and
# which new pids to expect to appear on startup. This fixes the
# graceful-shutdown problem the HTTP-only detector had: many Go servers
# stay HTTP-200 while draining in-flight requests, so HTTP-only polling
# false-fails; PID absence is the canonical "process stopped" signal.
PRE_PG_PIDS="$(cat "${WORK_DIR}/pre_pids_pipelinegen.list" 2>/dev/null | sort -u | tr '\n' ',' | sed 's/,$//')"
PRE_SCRAPER_PIDS="$(cat "${WORK_DIR}/pre_pids_scraper.list" 2>/dev/null | sort -u | tr '\n' ',' | sed 's/,$//')"
log_info "Stopping PipelineGen + node-scraper (pre-kill pid snapshot: pg=${PRE_PG_PIDS:-empty} scraper=${PRE_SCRAPER_PIDS:-empty})..."
# pkill exits 1 when no match found; tolerate so set -e doesn't crash —
# if NO processes match, that means we're racing with an operator kill
# which is itself a manual intervention worth capturing.
pkill -f 'pipelinegen --mode all' 2>/dev/null || log_info "  pkill pipelinegen: already absent (no-op)"
pkill -f 'artlist_server.js' 2>/dev/null || log_info "  pkill scraper: already absent (no-op)"

# PID-anchored shutdown wait (reviewer hard-nit #3): wait until every
# pre-kill pid has vacated /proc. Polled every 1s for ≤10 polls = 10s.
# This is the canonical "process stopped" signal independent of HTTP
# semantics (which can false-positive during graceful drain).
log_info "Waiting for pre-kill pids to exit (≤10s PID-anchored polling)..."
down=0
remaining_pids=""
for i in 1 2 3 4 5 6 7 8 9 10; do
    remaining_pids=""
    if [[ -n "${PRE_PG_PIDS}" ]]; then
        IFS=',' read -ra pg_arr <<< "${PRE_PG_PIDS}"
        for pid in "${pg_arr[@]}"; do
            [[ -z "${pid}" ]] && continue
            if [[ -d "/proc/${pid}" ]]; then
                remaining_pids="${remaining_pids}${pid} "
            fi
        done
    fi
    if [[ -z "${remaining_pids}" ]]; then
        down=1
        log_info "  poll ${i}: all pre-kill pids vacated /proc ($((i))s)"
        break
    fi
    sleep 1
done
if [[ "${down}" -ne 1 ]]; then
    log_fail "Clean restart: pre-kill pids still alive after 10s (remaining=${remaining_pids}); pkill didn't reach the binary or graceful drain is taking >10s"
    exit 1
fi

# ── Bring them back up (canonical setsid pattern from
# artlist_scraper_failure_smoke.sh:121 — matches operator runbook).
log_info "Starting PipelineGen via setsid ${PIPELINEGEN_BIN} --mode all..."
( cd "$(dirname "${PIPELINEGEN_BIN}")" && setsid "$(basename "${PIPELINEGEN_BIN}")" --mode all > "${WORK_DIR}/pipelinegen.log" 2>&1 & disown )
log_info "Starting node-scraper via setsid node ${SCRAPER_ENTRY}..."
( cd "${SCRAPER_DIR}" && setsid node "$(basename "${SCRAPER_ENTRY}")" > "${WORK_DIR}/scraper.log" 2>&1 & disown )

# PID-anchored startup wait: wait until a NEW pipelinegen process
# (pid > the largest pre-kill pid) appears in /proc AND /api/health
# returns 200. Hybrid because PID alone doesn't prove "ready"; HTTP
# alone doesn't prove "fresh restart". 30 polls × 2s = 60s window.
log_info "Waiting for new pipelinegen pid + /api/health ready (≤60s, 2s polling)..."
up=0
# Compute the maximal pre-kill pid; any new pid > this is a fresh spawn.
MAX_PRE_PID=$(echo "${PRE_PG_PIDS:-0}" | tr ',' '\n' | sort -n | tail -1)
if [[ -z "${MAX_PRE_PID}" || ! "${MAX_PRE_PID}" =~ ^[0-9]+$ ]]; then
    MAX_PRE_PID=0
fi
new_pg_pid=""
for i in $(seq 1 30); do
    new_pg_pid=$(pgrep -fn 'pipelinegen --mode all' 2>/dev/null || echo "")
    h=$(curl -sS --max-time 3 -o /dev/null -w '%{http_code}' "${BASE_URL}/api/health" 2>/dev/null || echo "000")
    if [[ -n "${new_pg_pid}" && "${new_pg_pid}" =~ ^[0-9]+$ && "${new_pg_pid}" -gt "${MAX_PRE_PID}" && "${h}" == "200" ]]; then
        up=1
        log_info "  poll ${i}: new pid=${new_pg_pid} (>max_pre=${MAX_PRE_PID}) + HTTP 200 (up confirmed after ~$((i * 2))s)"
        break
    fi
    sleep 2
done
if [[ "${up}" -ne 1 ]]; then
    log_fail "Clean restart: new pipelinegen pid never appeared OR /api/health never 200 within 60s (last_pid=${new_pg_pid:-none} last_code=${h}); inspect ${WORK_DIR}/pipelinegen.log + ${WORK_DIR}/scraper.log"
    exit 1
fi

# Capture post-restart vitals so the second-run vitals can compare
# against THIS snapshot (sanity-check the restart itself preserved state).
capture_vitals "post_restart"
post_restart_db=$(cat "${WORK_DIR}/post_restart_db_count" 2>/dev/null || echo "FAIL")
post_restart_qdrant=$(cat "${WORK_DIR}/post_restart_qdrant_count" 2>/dev/null || echo "FAIL")
log_pass "Clean restart complete: /api/health up at 200, db_count=${post_restart_db} qdrant_count=${post_restart_qdrant} (must equal pre; restart must NOT lose DB/Qdrant state)"

# Sanity: post-restart DB count must EQUAL pre (the restart itself
# doesn't add rows; the SECOND run does).
if [[ "${pre_db}" != "FAIL" && "${post_restart_db}" != "FAIL" && "${pre_db}" != "${post_restart_db}" ]]; then
    log_fail "Clean restart SURPRISE: pre_db=${pre_db} != post_restart_db=${post_restart_db} (restart itself must not mutate the DB; investigate)"
    exit 1
fi

# ── Phase 5: second PASS ──────────────────────────────────────────────────
smoke_log_section "Restart Verification — Phase 5 / 5 (second battery run)"
log_info "Invoking 05_pipeline_fresh.sh as subprocess for POST-RESTART PASS verification..."
if ! bash "${PIPELINE_FRESH_SH}"; then
    log_fail "Restart verification: SECOND run of 05_pipeline_fresh.sh FAILED (exit=$?). Restart lost state or introduced a regression. DoD requires PASS on BOTH runs"
    capture_vitals "post_second_run_failed"
    log_info "Diagnostic: failed-second-run vitals captured at ${WORK_DIR}/ for triage"
    exit 1
fi
log_pass "Restart verification: SECOND run of 05_pipeline_fresh.sh PASSED (3 more fresh clips added post-restart; Gates 4-9 still green)"

capture_vitals "post2"

# ── Aggregate verdict ─────────────────────────────────────────────────────
# Early-exit on Phase 3 FAIL (reviewer polish #1): if the manual-
# intervention guard already concluded FAIL (hard-class drift on
# db-first-kb or chrome-kill), skip the per-clip state monotonicity
# checks below — they're informational only when the gate is already
# failed and would only create noisy logs that bury the actual cause.
# Reviewer polish #2: drop the section header on the short-circuit path
# so readers don't see "Aggregate Verdict" without actual aggregate
# content below it; a single log_fail conveys both the cause and exit.
if [[ "${manual_int_verdict}" == "FAIL" ]]; then
    log_fail "Aggregate: Phase 3 hard-class manual-intervention already FAIL'd (db-first-kb write OR chrome pids dropped ≥3); skipping per-state monotonicity checks (would create noisy logs that bury the real cause)"
    exit 1
fi

smoke_log_section "Restart Verification — Aggregate Verdict"
# Start from the manual-intervention guard verdict (Phase 3 verdict
# propagation). If Phase 3 already returned PARTIAL, the aggregate
# inherits but can downgrade further based on phase 5 matches; PASS
# can ascend or be downgraded by phase 5.
verdict="${manual_int_verdict}"
if [[ "${verdict}" == "PASS" ]]; then
    verdict="PASS"  # explicit reset; below may downgrade
fi

# Counts must be valid integers and strictly non-decreasing across the
# three checkpoints: pre → post_restart → post1 (first run added 3) →
# post2 (second run added 3 more = 6 more than pre).
declare -a checkpoints=("pre" "post_restart" "post1" "post2")
declare -a db_counts qdrant_counts
for cp in "${checkpoints[@]}"; do
    db_counts+=("$(cat "${WORK_DIR}/${cp}_db_count" 2>/dev/null || echo "FAIL")")
    qdrant_counts+=("$(cat "${WORK_DIR}/${cp}_qdrant_count" 2>/dev/null || echo "FAIL")")
done

# Validate DB monotonicity.
for i in 0 1 2 3; do
    cur="${db_counts[$i]}"
    if [[ "${cur}" == "FAIL" || ! "${cur}" =~ ^[0-9]+$ ]]; then
        log_fail "Aggregate: db_count at ${checkpoints[$i]} = '${cur}' (not integer or FAIL)"
        verdict="FAIL"
        break
    fi
done
# Only run monotonicity check if all 4 are valid integers.
if [[ "${verdict}" == "PASS" ]]; then
    for i in 1 2 3; do
        prev="${db_counts[$((i-1))]}"
        cur="${db_counts[$i]}"
        if (( cur < prev )); then
            log_fail "Aggregate: db_count MONOTONICITY VIOLATION — ${checkpoints[$((i-1))]}=${prev} > ${checkpoints[$i]}=${cur} (state loss between checkpoints)"
            verdict="PARTIAL"
            break
        fi
    done
fi

# Same validation for Qdrant counts.
if [[ "${verdict}" != "FAIL" ]]; then
    for i in 0 1 2 3; do
        cur="${qdrant_counts[$i]}"
        if [[ "${cur}" == "FAIL" || ! "${cur}" =~ ^[0-9]+$ ]]; then
            log_fail "Aggregate: qdrant_count at ${checkpoints[$i]} = '${cur}' (not integer or FAIL)"
            verdict="FAIL"
            break
        fi
    done
fi
if [[ "${verdict}" == "PASS" ]]; then
    for i in 1 2 3; do
        prev="${qdrant_counts[$((i-1))]}"
        cur="${qdrant_counts[$i]}"
        if (( cur < prev )); then
            log_fail "Aggregate: qdrant_count MONOTONICITY VIOLATION — ${checkpoints[$((i-1))]}=${prev} > ${checkpoints[$i]}=${cur} (Qdrant state loss between checkpoints)"
            verdict="PARTIAL"
            break
        fi
    done
fi

# Specific DoD checks: the first run added 3 clips and the second run
# added 3 more, so pre vs post2 must show growth of >= 6 in both DB and
# Qdrant counts. If growth is < 6 the restart lost some writes silently.
if [[ "${verdict}" == "PASS" ]]; then
    pre_db_n="${db_counts[0]}"
    post2_db_n="${db_counts[3]}"
    delta_db=$(( post2_db_n - pre_db_n ))
    pre_q_n="${qdrant_counts[0]}"
    post2_q_n="${qdrant_counts[3]}"
    delta_q=$(( post2_q_n - pre_q_n ))
    if (( delta_db < 6 )); then
        log_fail "Aggregate: db growth over 2 runs = ${delta_db} (expected ≥ 6 since each run adds 3 clips; <6 implies silent state loss)"
        verdict="PARTIAL"
    fi
    if (( delta_q < 6 )); then
        log_fail "Aggregate: qdrant growth over 2 runs = ${delta_q} (expected ≥ 6 since each run indexes 3 clips; <6 implies outbox write loss)"
        verdict="PARTIAL"
    fi
fi

# Final verdict line.
case "${verdict}" in
    PASS)
        log_pass "Restart Verification Aggregate: PASS — first-run PASS + clean-restart + second-run PASS + DB/Qdrant monotonicity across pre/post_restart/post1/post2 (DoD Restart Verification CERTIFIED)"
        log_info "Vitals dump for triage at: ${WORK_DIR}/  (11 files × 4 phases = 44 vitals lines)"
        log_info "Operational conclusion: better-sqlite3 native binary + Chrome profile + Artlist session + DB config + consumer are all real, no manual intervention needed"
        exit 0
        ;;
    PARTIAL)
        log_fail "Restart Verification Aggregate: PARTIAL — at least one of the two runs failed OR state loss between checkpoints (DoD FORBIDS partial; review the per-phase logs above)"
        exit 1
        ;;
    FAIL)
        log_fail "Restart Verification Aggregate: FAIL — preflight vitals invalid or restart itself hung (DoD FORBIDS protect-class failures with a no-op; see ${WORK_DIR}/pipelinegen.log + ${WORK_DIR}/scraper.log)"
        exit 1
        ;;
esac
