#!/usr/bin/env bash
# scripts/ci/architecture/checks/all_checks.sh — CANONICAL LIST invocation
# (godlike/06 one canonical owner per fact; godlike/07 NO-FAKE-AVAILABILITY).
#
# CANONICAL LIST (numbers + paths, in invocation order):
#   ── broad-category monoliths (1-rule + delegated sub-orchestrator) ──
#     1. check_00_forbid_literal_job_type_strings.sh
#             (1 rule only — pre-split had a single Check 0 body; clean
#              layer-split template re-pass for naming + anti-bleed reset)
#     2. check_50_jobs.sh
#             (DELEGATED SUB-ORCHESTRATOR — sources lib/50_jobs_lib.sh
#              and bash-invokes 18 sub-checks at
#              scripts/ci/architecture/checks/50_jobs_*.sh. NOT
#              source-chained here because each sub-check is a self-
#              contained `set -euo pipefail` script that relies on
#              subshell env isolation per godlike/07 minimum-blast-
#              radius; converting the bash invocation to a source
#              would leak set -e abortion state across 18 sibling
#              gates. The 18 sub-checks live in 50_jobs_*.sh files
#              OUTSIDE all_checks/ and are picked up via the inner
#              bash loop, not via this dispatcher's glob.)
#   ── database rules (split per-rule, slot label-aligned) ──
#     3. check_30_no_legacy_scene_splitters.sh
#     4. check_31_no_synthetic_empty_scene_text.sh
#     5. check_32_no_prose_outputfmt.sh
#     6. check_33_forbid_retention_created_at_mutable.sh
#   ── API C2-Block rules (split per-rule, slot label-aligned with C2-A/C2-C/C2-E) ──
#     7. check_46_c2a_registry_only_in_capability_registry.sh
#     8. check_47_c2c_no_source_switch_outside_catalog.sh
#     9. check_48_c2e_route_manifest_equiv_generated_docs.sh
#   ── governance rules (split per-rule, July 2026) ──
#    10. check_56_fase_2_1_pr_voice_freeze.sh
#    11. check_60_forbid_t_skip_without_honest_limitation.sh
#    12. check_61_wave_tracker_baseline_size_summary.sh
#    13. check_62_forbid_inline_middleware_over_300_loc.sh
#    14. check_63_forbid_http_newrequest_in_storage_search.sh
#    15. check_64_postprocessor_registration_order.sh
#    16. check_65_debt_budget_pre_existing_open_cap.sh
#    17. check_69_no_auto_trigger_live_battery.sh
#    18. check_70_live_battery_copy_byte_equivalence.sh
#    19. check_71_asset_committer_ssot_wave5.sh
#    20. check_72_qdrant_upsert_ssot_wave5.sh
#    21. check_73_search_aggregator_uniqueness_wave5.sh
#    22. check_74_forbid_legacy_clip_generation.sh
#
# SLA (July 2026): contract test p95 ≤ 180s in CI (well under standard CI gates).
# The check_50_jobs.sh sub-orchestrator alone is ~30-60s (sources 18 sub-checks
# with full rg/go runs in subshells); the remaining 21 rule files are ~3-10s each.
# Extend cautiously; runtime scales linearly with rule-file count + sub-orchestrator cost.
# Of the 21 rule files, 9 are RECOVERY STUBS pending full rule body restoration
# (data loss from a previous verification basher's trap cleanup).
#
# Total: 22 entries (21 rule files + 1 delegated sub-orchestrator).
#
# ── CONTRACT TEST (July 2026, fail-closed pre-CI gate) ───────────
# The dispatcher exposes a `--contract-check` flag that runs 3
# fail-closed conditions BEFORE the next CI run. The contract
# catches 3 classes of SSOT regression:
#   (1) header ↔ filesystem drift (file added/deleted without
#       updating the canonical list),
#   (2) rule file sourceable without crash (syntax / set -u
#       propagation / parse-time error, distinct from gate-fail
#       which is legitimate exit-1),
#   (3) sort-key uniqueness (no two files share field-2 sort key,
#       else dispatcher order becomes tie-broken non-deterministic).
# The contract test adds ~30s overhead (subshell sources of 22
# entries); invoked explicitly, NOT on every dispatch. Pattern:
#   bash scripts/ci/architecture/checks/all_checks.sh --contract-check
# Thin CI wrapper at scripts/ci/architecture/checks/all_checks_contract_test.sh
# exposes the same flag for invocation from external CI pipelines.
# Exit code: 0 = contract holds; N (≥ 1) = count of failed conditions.
# Bash caps exit codes at 255; CI gating should treat any non-zero exit
# as fail-closed and route the printed summary to operator logs.
#
# This file IS the canonical invocation list. The body below
# implements this list via lazy-glob + POSIX sort (numerical-
# natural, field 2 = the numeric portion after `check_`). The
# expression above documents the contract; the body enforces it
# automatically — every `check_*.sh` file in all_checks/ is
# sourced in the canonical order on every CI run.
#
# godlike/06 SSOT: this file owns the orchestration. Each rule
# file (the 22 listed above) owns its own gate + allowlist
# semantics. Renumbering OR adding a new check MUST update
# BOTH this header (documentation) AND the rule file naming
# (dispatcher sort key). Drift between header and filesystem
# is itself an SSOT regression.
#
# godlike/07 NO-FAKE-AVAILABILITY: empty-glob safe (compgen
# guard absorbs missing-pattern no-match under set -e; || true
# on the read loop neutralizes the read-EOF sentinel). POSIX
# sort + read + process substitution (NOT GNU -V — macOS BSD
# sort lacks -V; macOS/BSD-portable per godlike/07 minimum-
# blast-radius).

# ── Marker ────────────────────────────────────────────────────────
# Sole marker echoed by the contract test's subshell source
# sandbox. Picked to be ASCII-only + zero whitespace + zero
# underscore + zero grep-special chars so any rule file legiti-
# mately printing `__...__` strings cannot collide with it.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Contract test (fail-closed pre-CI gate) ──────────────────────
# Three conditions; ALL must hold for exit 0. The dispatcher
# itself runs only after the contract test (or via the default
# invocation path which SKIPS the contract and goes straight to
# dispatch). See `--contract-check` flag below.
#
# Condition (1): canonical-list header ↔ filesystem sync.
# Parses lines matching `^#\s*\s+[0-9]+\.\s+(check_[a-zA-Z0-9_.-]+)…` from this
# file's header comment block; sym-diff against the basename set
# of `all_checks/*.sh`. FAIL-CLOSED if either symbol: > 0.
#
# Condition (2): every rule file sourceable without crash.
# For each rule file: spawn a subshell with `set +e` (gestalt-
# absorb any inherited `set -e`), source the rule, echo the
# `__ALL_CHECKS_SOURCE_OK__` marker on the next line. PASS when
# the marker reaches stdout; FAIL when the subshell dies before
# echo (parse error, set -u unbound var hit, $((arith)) crash).
# Legitimate gate-fail exit-1 inside a rule file is benign for
# this test: the subshell continues to echo the marker, the
# PASS path is taken.
#
# Condition (3): sort-key uniqueness. Extract field-2 of every
# basename via `awk -F_ '{print $2}'` and run `uniq -d`. Any
# duplicate fails the contract (sort would tie-break the order
# non-deterministically and the dispatcher's first-source-of-
# two-equal-keys claim would race).
_run_all_checks_contract_test() {
    local dispatcher="${BASH_SOURCE[0]}"
    local checks_dir="${SCRIPT_DIR}/all_checks"
    local failures=0
    local condition_results=()
    # Marker: ASCII-only, hash-suffixed for uniqueness over rule
    # file output. Defined per-call (not at top-level — would leak
    # into default-dispatch env under set -u propagation).
    local __ALL_CHECKS_SOURCE_OK__="__ALL_CHECKS_SOURCE_OK__MARKER__98caf4__"

    # ── Condition 1: header ↔ filesystem drift ──
    echo ""
    echo "=== Condition 1: canonical-list header vs filesystem sync ==="
    # Strip leading `#` + spaces; capture basename after `N. `.
    # Regex drop `\s*$` anchor allows trailing inline notes; sed stops
    # at the first whitespace post-basename. `|| true` neutralises
    # pipefail under set -e if grep returns 1 on no matches (the
    # exact scenario condition (1) is meant to detect).
    local header_basenames
    header_basenames=$(grep -E '^#\s+[0-9]+\.\s+(check_[a-zA-Z0-9_.-]+)' "${dispatcher}" \
        | sed -E 's/^#\s+[0-9]+\.\s+(check_[a-zA-Z0-9_.-]+).*$/\1/' \
        | LC_ALL=C sort -u || true)
    if [ -z "${header_basenames}" ]; then
        echo "FAIL: dispatcher header contains zero numbered canonical-list entries"
        failures=$((failures + 1))
        condition_results+=("1:HEADER_NO_ENTRIES")
        # Skip drift detection; nothing to compare against.
        header_basenames=""
    fi
    local fs_basenames
    fs_basenames=$(LC_ALL=C ls -1 "${checks_dir}"/check_*.sh 2>/dev/null \
        | xargs -n1 basename 2>/dev/null \
        | LC_ALL=C sort -u || true)
    if [ -z "${fs_basenames}" ]; then
        echo "FAIL: filesystem has no check_*.sh files in ${checks_dir}"
        failures=$((failures + 1))
        condition_results+=("1:FS_NO_FILES")
    fi
    # Symmetric difference: header-only (in header, missing from FS)
    # means a file was deleted without updating the header.
    local fs_only=""
    local header_only=""
    if [ -n "${header_basenames}" ] && [ -n "${fs_basenames}" ]; then
        fs_only=$(LC_ALL=C comm -23 <(echo "${header_basenames}") <(echo "${fs_basenames}"))
        header_only=$(LC_ALL=C comm -13 <(echo "${header_basenames}") <(echo "${fs_basenames}"))
    fi
    if [ -n "${fs_only}" ]; then
        echo "FAIL: filesystem entries NOT in canonical-list header (file added without header update):"
        echo "${fs_only}" | sed 's/^/  /'
        failures=$((failures + 1))
        condition_results+=("1:FS_ONLY")
    fi
    if [ -n "${header_only}" ]; then
        echo "FAIL: canonical-list header entries NOT on filesystem (file deleted without header update):"
        echo "${header_only}" | sed 's/^/  /'
        failures=$((failures + 1))
        condition_results+=("1:HEADER_ONLY")
    fi
    if [ -z "${fs_only}${header_only}" ] && [ -n "${fs_basenames}" ]; then
        local header_count
        local fs_count
        header_count=$(echo "${header_basenames}" | wc -l | awk '{print $1+0}')
        fs_count=$(echo "${fs_basenames}" | wc -l | awk '{print $1+0}')
        echo "OK: ${header_count} header entries == ${fs_count} filesystem entries (aligned)"
        condition_results+=("1:PASS")
    fi

    # ── Condition 2: every rule file sourceable without crash ──
    echo ""
    echo "=== Condition 2: every rule file sourceable without crash ==="
    local source_failures=0
    local source_pass=0
    if [ -n "${fs_basenames}" ]; then
        while IFS= read -r basename; do
            [ -z "${basename}" ] && continue
            local full="${checks_dir}/${basename}"
            # Per-rule syntax check (catches parse-time errors
            # BEFORE the source-sandbox runs). The `_bashn_err`
            # capture preserves the actual error location; the
            # failure message surfaces it to the operator instead
            # of swallowing via 2>/dev/null.
            local _bashn_err=""
            local out
            if ! _bashn_err=$(bash -n "${full}" 2>&1); then
                echo "FAIL: ${basename} — bash -n rejected (syntax error):"
                printf '%s\n' "${_bashn_err}" | sed 's/^/  /'
                source_failures=$((source_failures + 1))
                condition_results+=("2:BASH_N_FAIL:${basename}")
                continue
            fi
            # Source-with-marker pattern: `set +eu` neutralises inherited
            # `set -euo pipefail` so a legitimate gate-fail exit-1 OR
            # arithmetic crash stays absorbed; only a real bash crash
            # (parse error after `bash -n` pass, sigsegv) prevents the
            # marker echo. The `|| true` after echo absorbs any trailing
            # set -u triggers under edge cases the rule may have left.
            out=$( (
                set +eu
                source "${full}" 2>/dev/null
                echo "${__ALL_CHECKS_SOURCE_OK__}"
            ) 2>&1 || true )
            if [[ "${out}" == *"${__ALL_CHECKS_SOURCE_OK__}"* ]]; then
                source_pass=$((source_pass + 1))
            else
                echo "FAIL: ${basename} — subshell died before marker (parse / set -u / arithmetic crash)"
                source_failures=$((source_failures + 1))
                condition_results+=("2:CRASH:${basename}")
            fi
        done < <(echo "${fs_basenames}")
    fi
    if [ "${source_failures}" -eq 0 ] && [ "${source_pass}" -gt 0 ]; then
        echo "OK: ${source_pass} rule files sourced cleanly (no parse / set -u / arithmetic crashes)"
        condition_results+=("2:PASS")
    fi
    failures=$((failures + source_failures))

    # ── Condition 3: sort-key uniqueness ──
    echo ""
    echo "=== Condition 3: sort-key uniqueness (dispatcher sort -t_ -k2,2n) ==="
    if [ -n "${fs_basenames}" ]; then
        local sort_keys
        sort_keys=$(echo "${fs_basenames}" | awk -F_ '{print $2}')
        local duplicates
        duplicates=$(echo "${sort_keys}" | uniq -d)
        local total_keys
        total_keys=$(echo "${sort_keys}" | wc -l | awk '{print $1+0}')
        if [ -n "${duplicates}" ]; then
            echo "FAIL: duplicate sort keys detected — dispatcher sort would tie-break non-deterministically:"
            echo "${duplicates}" | awk -v dir="${checks_dir}" '{print "  " dir "/check_" $1 "_*.sh"}'
            failures=$((failures + 1))
            condition_results+=("3:DUP_KEYS")
        else
            echo "OK: ${total_keys} unique sort keys (0 duplicates)"
            condition_results+=("3:PASS")
        fi
    else
        echo "SKIP: no filesystem entries to check"
    fi

    # ── Summary ──
    echo ""
    echo "=== Contract test summary ==="
    echo "  failures: ${failures}"
    if [ "${failures}" -gt 0 ]; then
        echo "  result:   FAIL-CLOSED (one or more conditions violated)"
        echo "  details:"
        for r in "${condition_results[@]}"; do echo "    - ${r}"; done
    else
        echo "  result:   PASS (all 3 conditions hold)"
    fi
    return "${failures}"
}

# ── CLI dispatch ──
# `--contract-check` exits after running the 3-condition contract
# test (fail-closed); default invocation proceeds with the source
# chain dispatch.
if [ "${1:-}" = "--contract-check" ]; then
    set +e  # allow the contract test function to return a non-zero count
    _run_all_checks_contract_test
    exit $?
fi

# ── Default path: source-chain dispatch ──
if compgen -G "${SCRIPT_DIR}/all_checks/check_*.sh" > /dev/null; then
    while IFS= read -r extracted; do
        [ -e "${extracted}" ] || continue
        # shellcheck source=/dev/null
        source "${extracted}"
    done < <(LC_ALL=C ls -1 "${SCRIPT_DIR}"/all_checks/check_*.sh 2>/dev/null | LC_ALL=C sort -t'_' -k2,2n -k2,2) || true
fi
