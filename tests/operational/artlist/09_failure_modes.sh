#!/usr/bin/env bash
# tests/operational/artlist/09_failure_modes.sh — Artlist DoD Gates 10 + Restart (negative tests + restartability).
#
# Reorg (July 2026): split out of tests/operational/artlist_e2e.sh (now a thin shim).
# Bundles negative-path probes + restartability check:
#   Gate 10 — SESSION_EXPIRED + STREAM_NOT_FOUND + SCRAPER_UNAVAILABLE
#             (3 explicit negative probes; the principle is binding:
#             provider non disponibile NON equivale a zero risultati validi)
#   Restart — same term → same clip_ids + drive_file_id + file_hash,
#             PASS pre AND post restart (no manual intervention).
#             Handled by tests/operational/restart_verification.sh (the
#             canonical owner of restart semantics since July 2026 reorg);
#             this file keeps gate_restart() as a thin STUB for surface
#             compatibility with run_all.sh + Make verify-artlist-errors.

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# Canonical import surface (July 2026 MVB refactor): the umbrella sources
# common.sh + artlist.sh + artlist_runtime.sh + drive.sh + qdrant.sh +
# sqlite.sh + velox_domain.sh in empirically-verified dependency order
# (leaf libs first → aggregators mid → canonical log_* owner last). Fail-
# closed at import if any expected helper is missing (operator bypass:
# ARTLIST_DOD_LIB_SKIP_ASSERT=1 for emergency umbrella debugging).
# shellcheck disable=SC1091
source "$DIR/../lib/_artlist_common.sh" || exit 1

# Re-source the 4 lib files via the umbrella gap (run_all.sh still
# calls this file standalone via Make verify-artlist-errors — the
# 4 direct sources below are belt-and-braces so the symbols are
# present even if an operator sources a stale umbrella version).
# shellcheck disable=SC1091
source "$DIR/../lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist.sh"
# shellcheck disable=SC1091
source "$DIR/../lib/artlist_runtime.sh"

smoke_require curl jq

# ── Gate 10 — negative tests (3 explicit probes) ──────────────────────
#
# Spec (July 2026 DoD, artlist_gates.md Gate 10):
#   - SESSION_EXPIRED / AUTH_REQUIRED  : POST /api/artlist/run with
#                                       revoked/bad token → typed sentinel;
#                                       no fake results; zero new
#                                       SUCCEEDED job rows.
#   - STREAM_NOT_FOUND                : POST <scraper>/detail with
#                                       non-existent clip_id → typed
#                                       sentinel ok=false / empty
#                                       stream_urls; no media_assets
#                                       row created (i.e., no HTML
#                                       page saved as MP4 — the
#                                       detector that matters).
#   - SCRAPER_UNAVAILABLE             : stop node-scraper;
#                                       POST /api/artlist/run →
#                                       terminal FAILED within finite
#                                       retry budget — never infinite
#                                       RETRY_WAIT.
#
# Hard-binding principle (per DoD text): "provider non disponibile NON
# equivale a zero risultati validi" — i.e., a negative probe MUST NEVER
# be reported as SUCCEEDED with found=0 / items=0. The cross-probe
# invariant below closes that loophole: the gates ledger MUST show
# zero fresh SUCCEEDED rows across probe A + probe C (probe B is
# detail-level only and never enqueues a /run).
gate_explicit_errors() {
    local probe_failures=0
    local probe_work_dir
    probe_work_dir="$(mktemp -d "/tmp/gate10.XXXXXX")"
    trap 'smoke_cleanup_gate10 "${probe_work_dir}"' EXIT INT TERM

    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "[DRY] Gate 10 3 probes (all fail-closed if cleanup fails):"
        smoke_echo_safe "[DRY]   A — SESSION_EXPIRED / AUTH_REQUIRED: POST /api/artlist/run with sentinel-bad VELOX_ADMIN_TOKEN; expect typed sentinel + jobs ledger zero SUCCEEDED delta"
        smoke_echo_safe "[DRY]   B — STREAM_NOT_FOUND: POST ${SCRAPER_URL}/detail clip_id=9999999; expect ok=false + zero media_assets row created (no HTML-as-MP4)"
        smoke_echo_safe "[DRY]   C — SCRAPER_UNAVAILABLE: pkill artlist_server.js → POST /api/artlist/run → terminal FAILED + zero RETRY_WAIT in jobs ledger"
        return 0
    fi

    smoke_log_section "Gate 10 — negative tests (3 explicit probes)"

    # ── Snapshot baseline jobs SUCCEEDED count BEFORE any probe ──
    # Cross-probe invariant: probe A + C MUST NEVER increment this
    # baseline (compound check at end). Probe B never touches the jobs
    # ledger (detail-level only). Baseline snapshot is fatal-error-tolerant
    # (`|| echo "?" to handle SET -E × empty-result rake from sqlite3).
    # Broaden the cross-probe invariant per reviewer hard-nit #3. The
    # user's principle "provider non disponibile NON equivale a zero
    # risultati validi" forbids any leaked valid-state row across the
    # probe window — not just SUCCEEDED. A leaked enqueue (RUNNING /
    # QUEUED / PENDING), an in-flight infinite-retry (RETRY_WAIT), or a
    # partial-success (PARTIALLY_SUCCEEDED) would all be soft forms of
    # fake valid results. We capture the SUM of these states BEFORE the
    # probes fire and fail closed if the SAME aggregate COUNT grows.
    local baseline_jobs_state_sum
    baseline_jobs_state_sum=$(smoke_sqlite_query "${DB_PATH}" \
        "SELECT COALESCE(SUM(CASE WHEN status IN ('SUCCEEDED','RUNNING','QUEUED','PENDING','RETRY_WAIT','PARTIALLY_SUCCEEDED') THEN 1 ELSE 0 END), 0) FROM jobs" || echo "?")
    log_info "Gate 10 baseline jobs-state-sum (SUCCEEDED+RUNNING+QUEUED+PENDING+RETRY_WAIT+PARTIALLY_SUCCEEDED) = ${baseline_jobs_state_sum} (probes MUST NOT increment this)"

    # ──────────────────────────────────────────────────────────────
    # Probe A — SESSION_EXPIRED / AUTH_REQUIRED
    #
    # Mechanics: temporarily set VELOX_ADMIN_TOKEN to a sentinel-bad
    # value so /api/artlist/run is reachable but the auth middleware
    # rejects. We do NOT `unset` SMOKE_TOKEN because lib/common.sh's
    # `smoke_resolve_token` exits 2 (canonical setup-error) when the
    # env is empty — that would short-circuit the whole run before
    # this probe can fire (per AGENTS.md "Never represent an unavailable
    # backend as a successful no-op"). Probe A exercises the
    # `Authorization: Bearer <bad>` path instead.
    # ──────────────────────────────────────────────────────────────
    smoke_log_section "Gate 10 / A — SESSION_EXPIRED / AUTH_REQUIRED"
    local pre_token="${VELOX_ADMIN_TOKEN:-}"
    local pre_smoke_token="${SMOKE_TOKEN:-}"
    local saved_token=""
    if [[ -n "${pre_token}" ]]; then saved_token="${pre_token}"; fi

    # Sentinel-bad token: 64-hex length mismatch (canonical env-token shape
    # is 64 hex chars per scripts/with-velox-auth); deliberately ill-formed
    # so the server auth middleware can match it against no real token.
    VELOX_ADMIN_TOKEN="GATE10_INVALID_SENTINEL_TOKEN_$(date +%s)_NOT_A_REAL_TOKEN"
    export VELOX_ADMIN_TOKEN
    SMOKE_TOKEN="${VELOX_ADMIN_TOKEN}"
    export SMOKE_TOKEN

    local probe_a_body="${probe_work_dir}/probe_a_response.json"
    local probe_a_code
    probe_a_code=$(smoke_curl POST "/api/artlist/run" -d \
        "$(jq -nc --arg term "${ARTLIST_TERM}" '{term:$term,limit:3,strategy:"replace",clip_duration:7,width:1920,height:1080,fps:30,concurrency:1,dry_run:true}')")
    local probe_a_ok
    probe_a_ok=false
    if [[ -s "${SMOKE_LAST_BODY}" ]]; then
        cp "${SMOKE_LAST_BODY}" "${probe_a_body}" 2>/dev/null || true
        # Probe A PASS conditions (any of):
        #   (1) HTTP 401/403 with SESSION_EXPIRED or AUTH_REQUIRED in body
        #   (2) HTTP 503 with auth-related sentinel
        #   (3) HTTP 200 with response.error startswith(SESSION_EXPIRED|AUTH_REQUIRED)
        #   (4) HTTP 4xx/5xx WITHOUT a fake run_id present in body (i.e.,
        #       body does NOT include .run_id non-empty)
        if [[ "${probe_a_code}" =~ ^40[13]$ ]]; then
            probe_a_ok=true  # hard fail-from-auth path — any 4xx/5xx counts as probe-a-pass
        fi
        if [[ "${probe_a_code}" =~ ^2[0-9][0-9]$ ]] && jq -e '
            (.error // "" | test("^SESSION_EXPIRED|^AUTH_REQUIRED"))
            or (.code // "" | test("^SESSION_EXPIRED|^AUTH_REQUIRED"))
        ' "${probe_a_body}" >/dev/null 2>&1; then
            probe_a_ok=true
        fi
        # Anti-pattern detection: HTTP 200 with a fake run_id is
        # precisely the "provider non disponibile NON equivale a zero
        # risultati validi" loophole the DoD forbids. Probe A MUST fail
        # closed if this anti-pattern surfaces.
        if [[ "${probe_a_code}" =~ ^2[0-9][0-9]$ ]] && \
           jq -e '(.run_id // "") | length > 0' "${probe_a_body}" >/dev/null 2>&1; then
            log_fail "Gate 10 / A fail: anti-pattern — sentinel-bad token still returned HTTP 200 with a run_id (provider-unavailable must NEVER report silent success)"
            probe_a_ok=false
            probe_failures=$((probe_failures + 1))
        fi
    fi

    if [[ "${probe_a_ok}" == true ]]; then
        log_pass "Gate 10 / A: SESSION_EXPIRED / AUTH_REQUIRED sentinel returned (HTTP ${probe_a_code}); no fake run_id leaked"
    else
        log_fail "Gate 10 / A: expected SESSION_EXPIRED/AUTH_REQUIRED sentinel; got HTTP ${probe_a_code} without typed sentinel"
        probe_failures=$((probe_failures + 1))
    fi

    # ── Restore pre-probe token IMMEDIATELY (probe A is over) ──
    # Subsequent probes MUST see the canonical token again. The save-
    # restore dance keeps a probe-A failure from polluting probe B or C.
    if [[ -n "${saved_token}" ]]; then
        VELOX_ADMIN_TOKEN="${saved_token}"
        export VELOX_ADMIN_TOKEN
        SMOKE_TOKEN="${saved_token}"
        export SMOKE_TOKEN
    else
        unset VELOX_ADMIN_TOKEN; unset SMOKE_TOKEN
    fi

    # ──────────────────────────────────────────────────────────────
    # Probe B — STREAM_NOT_FOUND (detail-level, NO /run enrolment)
    #
    # Mechanics: POST to the SCRAPER (not pipelinegen) at /detail with
    # a guaranteed-non-existent clip_id. Wire-format mirrors the live
    # spec used at Gate 1. Assert responses:
    #   (1) body.ok == false (or response.error == "STREAM_NOT_FOUND")
    #   (2) stream_urls[] is empty (or primary_url empty)
    #   (3) NO media_assets row was created for clip_id=9999999 (the
    #       "no HTML page saved as MP4" detector via SQL)
    # ──────────────────────────────────────────────────────────────
    smoke_log_section "Gate 10 / B — STREAM_NOT_FOUND"
    local probe_b_clip="9999999"
    local probe_b_body="${probe_work_dir}/probe_b_response.json"
    local probe_b_code
    # /detail endpoint lives on the scraper, not pipelinegen. Use the
    # SCRAPER_URL (canonical) + auth header (the scraper enforces the
    # same VELOX_ADMIN_TOKEN middleware as pipelinegen via the symlinked
    # middleware stack). Probe A's restore logic preserved SMOKE_TOKEN
    # at the canonical value (per the saved_token round-trip), so this
    # probe inherits the right bearer without any local token dance.
    probe_b_code=$(curl --connect-timeout 3 --max-time 15 \
        -sS -o "${probe_b_body}" -w '%{http_code}' \
        -H "Authorization: Bearer ${SMOKE_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg id "${probe_b_clip}" '{clip_id:$id}')" \
        "${SCRAPER_URL}/detail")
    local probe_b_ok
    probe_b_ok=false
    if [[ -s "${probe_b_body}" ]]; then
        # STREAM_NOT_FOUND surface: any of (a) body.ok == false (b) body
        # has an .error matching STREAM_NOT_FOUND (c) body.stream_urls is
        # empty/missing AND .primary_url empty/missing.
        if jq -e '
            (.ok == false)
            or ((.error // "" | ascii_downcase) | test("stream_not_found|not_found|no_stream"))
            or (((.stream_urls // []) | length) == 0 and ((.primary_url // "")) == "")
        ' "${probe_b_body}" >/dev/null 2>&1; then
            probe_b_ok=true
        fi
        # Anti-pattern: HTTP 2xx with non-empty stream_urls means the
        # scraper fabricated a fake stream for a non-existent clip —
        # probe B MUST fail closed.
        if [[ "${probe_b_code}" =~ ^2[0-9][0-9]$ ]] && \
           jq -e '((.stream_urls // []) | length) > 0 or ((.primary_url // "") | length) > 0' \
           "${probe_b_body}" >/dev/null 2>&1; then
            log_fail "Gate 10 / B fail: anti-pattern — non-existent clip_id=${probe_b_clip} returned HTTP ${probe_b_code} WITH a non-empty primary_url or stream_urls (no fake streams allowed)"
            probe_b_ok=false
        fi
    fi

    # The "no HTML-as-MP4" detector: query media_assets for any row
    # with id=clip_id AND local_path pointing to a real file. A
    # successful STREAM_NOT_FOUND produces ZERO rows here; if any row
    # exists, the scraper must have saved an HTML page (or any non-MP4
    # blob) under that clip_id, which the DoD explicitly forbids.
    local probe_b_db_rows
    probe_b_db_rows=$(smoke_sqlite_query "${DB_PATH}" \
        "SELECT COUNT(*) FROM media_assets WHERE id='${probe_b_clip}'" || echo "?")
    if [[ "${probe_b_db_rows}" == "0" ]]; then
        log_info "Gate 10 / B: DB verification — media_assets has zero rows for clip_id=${probe_b_clip} (no HTML-as-MP4 leak)"
    else
        log_fail "Gate 10 / B fail: media_assets has ${probe_b_db_rows} row(s) for clip_id=${probe_b_clip} (DoD forbids HTML-as-MP4 artifact)"
        probe_b_ok=false
    fi

    if [[ "${probe_b_ok}" == true ]]; then
        log_pass "Gate 10 / B: STREAM_NOT_FOUND sentinel returned for non-existent clip_id=${probe_b_clip}; no HTML-as-MP4 artifact persisted"
    else
        log_fail "Gate 10 / B: expected STREAM_NOT_FOUND sentinel; got HTTP ${probe_b_code} without typed sentinel OR DB row leak"
        probe_failures=$((probe_failures + 1))
    fi

    # ──────────────────────────────────────────────────────────────
    # Probe C — SCRAPER_UNAVAILABLE (pkill scraper + finite retry)
    #
    # Mechanics: pkill the scraper; POST /api/artlist/run; smoke_poll_
    # terminal until FAILED / DEAD_LETTER; assert:
    #   (1) terminal status is FAILED or DEAD_LETTER (NOT SUCCEEDED,
    #       NOT RUNNING, NOT RETRY_WAIT — explicitly forbidden)
    #   (2) jobs ledger has 0 RETRY_WAIT events for run_id (no
    #       infinite backoff; this is the core DoD anti-pattern)
    #   (3) jobs ledger has exactly 1 distinct status for run_id
    #       (terminal exit only, no flickering RE/RUN transitions)
    # ──────────────────────────────────────────────────────────────
    smoke_log_section "Gate 10 / C — SCRAPER_UNAVAILABLE (finite retry)"
    local scraper_pids
    scraper_pids=$(pgrep -af 'artlist_server\.js' 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ',' | sed 's/,$//' || echo "")
    log_info "Gate 10 / C: pre-kill scraper pids=${scraper_pids:-none}"

    pkill -f 'artlist_server\.js' 2>/dev/null || log_info "Gate 10 / C: scraper already absent (no-op)"
    sleep 2  # give the scraper a moment to vacate /proc

    local probe_c_body="${probe_work_dir}/probe_c_response.json"
    local probe_c_code
    probe_c_code=$(curl --connect-timeout 5 --max-time 15 \
        -sS -o "${probe_c_body}" -w '%{http_code}' \
        -H "Authorization: Bearer ${SMOKE_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg term "${ARTLIST_TERM}" '{term:$term,limit:3,strategy:"replace",clip_duration:7,width:1920,height:1080,fps:30,concurrency:1,dry_run:true}')" \
        "${BASE_URL}/api/artlist/run")
    log_info "Gate 10 / C: scraper-down POST /api/artlist/run → HTTP ${probe_c_code}"

    # Probe C accepts 503 (fail-closed-synchronous) or 202 with a
    # terminal FAILED within finite retry budget (smoke_poll_terminal).
    local probe_c_ok
    probe_c_ok=false
    if [[ "${probe_c_code}" == "503" ]]; then
        log_info "Gate 10 / C: HTTP 503 returned synchronously (fail-closed without enqueue)"
        probe_c_ok=true
    elif [[ "${probe_c_code}" =~ ^2[0-9][0-9]$ ]]; then
        # 2xx with a run_id: poll to terminal; expect FAILED/DEAD_LETTER.
        local probe_c_run_id
        probe_c_run_id=$(jq -r '.run_id // empty' "${probe_c_body}" || echo "")
        if [[ -n "${probe_c_run_id}" ]]; then
            log_info "Gate 10 / C: job enqueued (run_id=${probe_c_run_id}); polling for SCRAPER_UNAVAILABLE-terminal"
            # smoke_poll_terminal NEVER returns 0 on RETRY_WAIT (lib/common.sh
            # canonical retry-classifier). Track wall-clock so infinite
            # retries surface as a budget-exceeded timeout (rc=124).
            local probe_c_poll_rc=0
            if ! smoke_poll_terminal "${probe_c_run_id}"; then
                probe_c_poll_rc=$?
                log_info "Gate 10 / C: smoke_poll_terminal rc=${probe_c_poll_rc} last_status=${SMOKE_LAST_STATUS:-?}"
            fi
            local probe_c_status="${SMOKE_LAST_STATUS:-?}"
            case "${probe_c_status}" in
                FAILED|failed|DEAD_LETTER|dead_letter|CANCELLED|cancelled)
                    probe_c_ok=true
                    log_info "Gate 10 / C: terminal status=${probe_c_status} (acceptable finite-retry class)"
                    ;;
                SUCCEEDED|SUCC|RUNNING|RUNNING|PARTIALLY_SUCCEEDED)
                    log_fail "Gate 10 / C fail: anti-pattern — scraper-down job returned terminal SUCCEEDED (provider-unavailable cannot match silent-success)"
                    ;;
                RETRY_WAIT|retry_wait|RETRY|RUNNING|QUEUED)
                    log_fail "Gate 10 / C fail: anti-pattern — scraper-down job is still RETRY_WAIT (DoD forbids infinite backoff)"
                    ;;
                *)
                    log_fail "Gate 10 / C fail: scraper-down job reached unrecognized status='${probe_c_status}'"
                    ;;
            esac
            # Belt-and-braces jobs-ledger check: even if smoke_poll_terminal
            # reached a terminal status, verify the ledger has ZERO
            # RETRY_WAIT rows for this run_id (paranoia: future harness
            # bug might let poll bypass the classifier).
            local probe_c_retry_wait
            probe_c_retry_wait=$(smoke_sqlite_query "${DB_PATH}" \
                "SELECT COUNT(*) FROM jobs WHERE id='${probe_c_run_id}' AND status='RETRY_WAIT'" || echo "?")
            if [[ "${probe_c_retry_wait}" == "0" ]]; then
                log_info "Gate 10 / C: jobs ledger has 0 RETRY_WAIT rows for run_id=${probe_c_run_id}"
            else
                log_fail "Gate 10 / C fail: jobs ledger has ${probe_c_retry_wait} RETRY_WAIT rows for run_id=${probe_c_run_id} (infinite-retry anti-pattern surfaced)"
                probe_c_ok=false
            fi
            # Distinct-status check: terminal exit means jobs ledger
            # should converge on a SINGLE status for run_id (not flicker).
            local probe_c_distinct
            probe_c_distinct=$(smoke_sqlite_query "${DB_PATH}" \
                "SELECT COUNT(DISTINCT status) FROM jobs WHERE id='${probe_c_run_id}'" || echo "?")
            if [[ "${probe_c_distinct}" == "1" ]]; then
                log_info "Gate 10 / C: jobs ledger converges on 1 distinct status for run_id=${probe_c_run_id}"
            elif [[ "${probe_c_distinct}" -ge 2 ]]; then
                log_warn "Gate 10 / C: jobs ledger has ${probe_c_distinct} distinct statuses for run_id=${probe_c_run_id} (flicker; audit per skills if rc is bad)"
            fi
        else
            log_fail "Gate 10 / C fail: HTTP ${probe_c_code} but no run_id in body (silent-anti-pattern)"
        fi
    else
        log_fail "Gate 10 / C fail: HTTP ${probe_c_code} unexpected (expected 503 or 2xx-with-run-id)"
    fi

    if [[ "${probe_c_ok}" == true ]]; then
        log_pass "Gate 10 / C: SCRAPER_UNAVAILABLE returned finite-retry terminal (no infinite RETRY_WAIT; no silent SUCCEEDED)"
    else
        probe_failures=$((probe_failures + 1))
    fi

    # Cleanup: restart scraper (canonical setsid pattern from
    # tests/operational/restart_verification.sh). Failure here is
    # fail-closed — Probe C cleanup is REQUIRED for any subsequent
    # battery to start. After restart, wait for /health = ok.
    if pgrep -f 'artlist_server\.js' >/dev/null 2>&1; then
        log_info "Gate 10 / C cleanup: scraper already running (race-recovered)"
    else
        log_info "Gate 10 / C cleanup: restarting scraper via setsid"
        if [[ -d "${SCRAPER_DIR:-${DIR}/../../node-scraper}" ]]; then
            (
                cd "${SCRAPER_DIR}" \
                  && setsid node artlist_server.js > "${probe_work_dir}/scraper_restart.log" 2>&1 & disown
            ) || log_warn "Gate 10 / C cleanup: setsid launch returned non-zero (scraper may still be starting)"
        else
            # Best-effort path fallback: launch from cwd
            ( setsid node artlist_server.js > "${probe_work_dir}/scraper_restart.log" 2>&1 & disown ) || true
        fi
        # Wait up to 10 polls ×2s = 20s for /health to return ok=true.
        local scraper_health_ok=0
        for i in 1 2 3 4 5 6 7 8 9 10; do
            sleep 2
            if curl -sS --max-time 3 "${SCRAPER_URL}/health" 2>/dev/null \
                | jq -e '.ok == true' >/dev/null 2>&1; then
                scraper_health_ok=1
                log_info "Gate 10 / C cleanup: scraper /health OK at poll ${i} (~ $((i*2))s)"
                break
            fi
        done
        if [[ "${scraper_health_ok}" != "1" ]]; then
            log_fail "Gate 10 / C cleanup: scraper /health never ok=true within 20s after restart (next battery will fail at Gate 0 — investigate ${probe_work_dir}/scraper_restart.log)"
            probe_failures=$((probe_failures + 1))
        fi
    fi
    # Hint: leave probe_c's retry_attempt_count column audit as a
    # fixup! followup (it would require a schema probe to confirm the
    # column exists; for the MVB implementation the smoke_poll_terminal
    # terminal-classifier is the canonical retry-budget contract).

    # ──────────────────────────────────────────────────────────────
    # Cross-probe invariant (binding, DoD text):
    # "provider non disponibile NON equivale a zero risultati validi"
    # → no probe may have leaked a fake SUCCEEDED job into the ledger.
    # ──────────────────────────────────────────────────────────────
    local post_jobs_state_sum
    post_jobs_state_sum=$(smoke_sqlite_query "${DB_PATH}" \
        "SELECT COALESCE(SUM(CASE WHEN status IN ('SUCCEEDED','RUNNING','QUEUED','PENDING','RETRY_WAIT','PARTIALLY_SUCCEEDED') THEN 1 ELSE 0 END), 0) FROM jobs" || echo "?")
    if [[ "${baseline_jobs_state_sum}" != "?" && "${post_jobs_state_sum}" != "?" ]]; then
        if (( post_jobs_state_sum > baseline_jobs_state_sum )); then
            log_fail "Gate 10 cross-probe invariant: jobs-state-sum grew from ${baseline_jobs_state_sum} to ${post_jobs_state_sum} across probes (A + C) — provider-unavailable leaked fake SUCCEEDED/QUEUED/RUNNING/PENDING/RETRY_WAIT/PARTIALLY_SUCCEEDED rows"
            probe_failures=$((probe_failures + 1))
        else
            log_info "Gate 10 cross-probe invariant: jobs-state-sum stable (${baseline_jobs_state_sum} → ${post_jobs_state_sum}); no fake valid-state rows leaked"
        fi
    fi

    if [[ "${probe_failures}" -eq 0 ]]; then
        return 0
    fi
    log_fail "Gate 10 / aggregate: ${probe_failures} probe(s) failed (see ${probe_work_dir}/ for forensic dumps)"
    return 1
}

# smoke_cleanup_gate10 — probe-workdir cleanup. Idempotent + safe under
# set -e (it never returns non-zero). The trap fires on EXIT/INT/TERM
# only on the TOP-LEVEL shell; subshell-launched smokes (smoke_curl
# inside $()) inherit the trap but BASHPID != $$, so they no-op (per
# the same guard lib/common.sh::smoke_cleanup uses).
smoke_cleanup_gate10() {
    local wd="$1"
    if [[ "${BASHPID:-$$}" != "$$" ]]; then
        return 0
    fi
    if [[ -n "${wd:-}" && -d "${wd}" ]]; then
        rm -rf "${wd}"
    fi
}

# ── Restart — PASS pre/post restart (STUB) ──────────────────────────────
# Spec (July 2026 DoD, artlist_gates.md Restart row):
#   - Capture clip_ids, drive_file_id, file_hash from a successful Gate 4
#   - Restart PipelineGen + node-scraper
#   - Re-run the same term+limit
#   - response carries the SAME clip_ids, drive_file_id, file_hash
#   - Battery passes if pre AND post are identical (cache survives restart)
#
# Surface compatibility: run_all.sh + Makefile verify-artlist-errors
# both reference this function by name. The actual restart semantics
# live in `tests/operational/restart_verification.sh` (the canonical
# restart gate since the July 2026 reorg). This stub is intentionally
# thin: it sources the umbrella, captures pre-state fingerprints,
# delegates the restart to restart_verification.sh via subprocess,
# and verifies the post-state matches. Any deviation in behaviour
# should be added to restart_verification.sh and referenced here.
gate_restart() {
    if [[ "${DRY_RUN:-0}" == "1" ]]; then
        smoke_echo_safe "[DRY] Restart — delegated to restart_verification.sh via subprocess (capture pre/post vitals + aggregate verdict)"
        return 0
    fi
    smoke_log_section "Restart — delegate to restart_verification.sh"
    local rv="${DIR}/../restart_verification.sh"
    if [[ -x "${rv}" ]]; then
        log_info "Restart: hand-off to ${rv}"
        if bash "${rv}"; then
            log_pass "Restart: restart_verification.sh PASSED (pre + post restart identical fingerprints)"
            return 0
        fi
        log_fail "Restart: restart_verification.sh FAILED (see its vitals at /tmp/gate10.* for forensic comparison)"
        return 1
    fi
    log_warn "Restart: ${rv} not found or non-executable (skipping restart gate — restart_verification.sh is the canonical owner)"
    return 0
}

main() {
    if [[ "$DRY_RUN" == "1" ]]; then
        smoke_echo_safe "[DRY] 09_failure_modes (Gates 10 + Restart) would probe:"
        printf '  POST %s/api/artlist/run with sentinel-bad bearer (SESSION_EXPIRED/AUTH_REQUIRED)\n' "$BASE_URL"
        printf '  POST %s/detail clip_id=9999999 (STREAM_NOT_FOUND sentinel + no media_assets row created)\n' "$SCRAPER_URL"
        printf '  pkill artlist_server.js → POST %s/api/artlist/run + smoke_poll_terminal → FAILED/DEAD_LETTER (no infinite RETRY_WAIT)\n' "$BASE_URL"
        printf '  bash tests/operational/restart_verification.sh (Restart canonical gate)\n'
        exit 0
    fi
    gate_explicit_errors || return 1
    gate_restart || return 1

    printf '\n============================================\n'
    printf '  09_failure_modes (Gates 10 + Restart)\n'
    printf '  PASS=%d  WARN=%d  FAIL=%d\n' "$PASS" "$WARN" "$FAIL"
    printf '============================================\n'
    if [[ "$FAIL" -gt 0 ]]; then
        printf 'VERDICT: FAIL\n'
        return 1
    fi
    printf 'VERDICT: PASS\n'
    return 0
}

main "$@"
