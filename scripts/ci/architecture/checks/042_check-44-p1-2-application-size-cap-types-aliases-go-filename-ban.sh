# ── Check 44 (P1-2 application size cap + types_aliases.go filename ban) ──
# Action P1-2 of cleanup plan (June 2026): promoted from `current_state: deferred` to active.
# Slot was reserved in the original Check 45 commit per the now-removed
# `NOTE: Check 44 ... monotone-ratchetable.` comment above (see git history).
# Companion `arch(current):` commit in this PR flips wave_status.P1-2.current_state.
# SSOT (target + transitional_cap + current_state) read live from
# architecture/current.yaml::doc[1].wave_status.P1-2 per AGENTS.md §8 SSOT discipline.
bash "$(dirname "$0")/ci/architecture/checks/44_application_size_cap_and_aliases_ban.sh" || { echo "Check 44 (P1-2 application size cap + types_aliases.go filename ban) failed"; exit 1; }

bash "$(dirname "$0")/ci/architecture/checks/46_inline_clips_repository_map_ban.sh" || { echo "Check 46 (inline ClipsRepository map ban) failed"; exit 1; }

