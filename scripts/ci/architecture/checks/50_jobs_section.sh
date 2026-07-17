#!/usr/bin/env bash
# 50_jobs sub-check (verbatim-extracted section of the original monolithic
# scripts/ci/architecture/checks/50_jobs.sh — see
# scripts/ci/architecture/checks/lib/50_jobs_section_map.json for the
# byte-precise line range, and the lib/50_jobs_profile.sh for the
# analysis that produced this split). Do NOT hand-edit body to fix
# checks; edit the original 50_jobs.sh and re-run the splitter (or
# move body content out-of-line manually here with a corresponding
# orchestrator update).

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve sub-check directory from BASH_SOURCE[0]=" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

# ── Verbatim section body extracted from the original monolithic ────────
# ── Check 44 (P1-2 application size cap + types_aliases.go filename ban) ──
# Action P1-2 of cleanup plan (June 2026): promoted from `current_state: deferred` to active.
# Slot was reserved in the original Check 45 commit per the now-removed
# `NOTE: Check 44 ... monotone-ratchetable.` comment above (see git history).
# Companion `arch(current):` commit in this PR flips wave_status.P1-2.current_state.
# SSOT (target + transitional_cap + current_state) read live from
# architecture/current.yaml::doc[1].wave_status.P1-2 per AGENTS.md §8 SSOT discipline.
bash "${SCRIPT_DIR}/44_application_size_cap_and_aliases_ban.sh" || { echo "Check 44 (P1-2 application size cap + types_aliases.go filename ban) failed"; exit 1; }

bash "${SCRIPT_DIR}/46_inline_clips_repository_map_ban.sh" || { echo "Check 46 (inline ClipsRepository map ban) failed"; exit 1; }
