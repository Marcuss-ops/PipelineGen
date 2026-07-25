#!/usr/bin/env bash
# tests/operational/lib/_artlist_common.sh — canonical one-import umbrella
# for the Artlist DoD operational surface.
#
# ============================================================================
# Why this file exists (July 2026 refactor)
# ============================================================================
# Multiple operational batteries that touch the Artlist surface have had to
# hand-roll their own import blocks because the canonical lib surface was
# distributed across 3 separate files (`lib/common.sh` for HTTP/SQLite/curl
# primitives, `lib/artlist_runtime.sh` for ARTLIST_-prefixed env vars +
# the canonical log_pass/log_warn/log_fail/log_info family, `lib/velox_domain.sh`
# for the Qdrant/Drive/PipelineRun typed helpers like `velox_qdrant_assert`).
#
# The user's DoD refactor directive (replayable single-source-of-truth with
# no duplicated logic) makes this distribution fragile: every script that
# wants the full Artlist surface has to know all 3 paths AND source them
# in the correct order (artlist_runtime depends on common; velox_domain
# depends on common; artlist_runtime must come last so log counters are
# the canonical version). A canonical `_artlist_common.sh` collapses
# that knowledge into ONE import: `source _artlist_common.sh`.
#
# ============================================================================
# AGENTS.md single-focus rule honoured
# ============================================================================
# This umbrella does NOT introduce any new helper. It is a pure import
# surface: 7 `. $DIR/<lib>.sh` lines followed by one self-validation block.
# All actual business logic stays in the underlying lib files where it
# already lives. The only NEW content is `artlist_dod_assert_helpers_loaded`
# which fails closed if any expected helper is missing (so a future
# refactor that removes `velox_qdrant_assert` from `velox_domain.sh` will
# surface immediately at import time instead of silently breaking
# artlist_e2e.sh or vidrush_media_e2e.sh at first call site).
#
# ============================================================================
# godlike/06 SSOT canonical chain
# ============================================================================
# Every gate_* primitive in tests/operational/artlist/{01..09}_*.sh and
# tests/operational/05_pipeline_fresh.sh depends on this exact set of
# exports. Importing them through the umbrella guarantees that a future
# change to a lib file's function signature or env-var-binding contract
# propagates to ALL callers via a single edit (this umbrella file would
# still source the lib; the lib itself is the SSOT — the umbrella is
# the SHORTCUT to the SSOT).
#
# ============================================================================
# Usage (verbatim)
# ============================================================================
#   #!/usr/bin/env bash
#   DIR=$(cd "$(dirname "$0")" && pwd)
#   source "$DIR/lib/_artlist_common.sh"
#
# ============================================================================
# Refactor sequencing (binding)
# ============================================================================
# This MVB (July 2026) leaves the 9 granular sub-batteries + restart_verification.sh
# untouched. Future refactor waves (per the 5-step plan) will:
#   - thin out artlist/{01..09}/*.sh into 5-line stubs that call `gate_*`
#     helpers extracted into this umbrella's sibling namespace;
#   - unify restart semantics across restart_verification.sh and
#     artlist/09_failure_modes.sh::gate_restart;
#   - delete the lib/{common,artlist_runtime,velox_domain}.sh no-op
#     re-exports once every caller sources the umbrella instead.
# Until those waves land, this file is the canonical import surface for
# the TWO consumers named in the user's directive (artlist_e2e.sh,
# vidrush_media_e2e.sh); subsequent consumers migrate as the gate_*
# primitives migrate.

# Source order matters. The empirical dependency chain (verified by the
# lib-file inspection at the time this umbrella was last touched) is:
#   * common.sh       — base primitives (smoke_curl / smoke_require /
#                       BASE_URL / DB_PATH / SCRAPER_URL)
#   * drive.sh / qdrant.sh / sqlite.sh — pure leaf libs with NO inter-
#                       dependencies (no overlapping helper names).
#                       Sourced BEFORE artlist.sh / artlist_runtime.sh so
#                       an aggregator cannot accidentally trigger an
#                       override on a leaf helper.
#   * artlist.sh      — Artlist API helpers (search/detail/download/run).
#                       Can override common.sh if it defines overlapping
#                       names; sourced BEFORE artlist_runtime.sh so the
#                       canonical log_* family wins via later override.
#   * velox_domain.sh — velox_qdrant_assert / velox_drive_resolve /
#                       velox_artlist_pipeline_run. Depends on common
#                       primitives only; sourced BEFORE artlist_runtime
#                       for the same override-safety reason.
#   * artlist_runtime.sh — LAST. Canonical owner of log_pass/log_warn/
#                       log_fail/log_info family per godlike/06 SSOT.
#                       Must come AFTER all other libs so its log_*
#                       family overrides any duplicates they may define.
#                       Also initializes PASS=0/WARN=0/FAIL=0 at top
#                       level so the canonical counters reset on every
#                       top-of-file import.
#
# Belt-and-braces idempotency: bash `source` of an already-loaded file is
# a no-op for functions but RE-RUNS top-level non-function statements
# (e.g., PASS=0 reset). The umbrella is intended to be sourced EXACTLY
# ONCE per top-level script invocation so the PASS=0/WARN=0/FAIL=0
# reset happens at canonical-script-start (the desired semantics).

# Resolve this umbrella's own directory via BASH_SOURCE rather than
# inheriting the caller's `$DIR`. Multiple consumers source us:
#   * artlist_e2e.sh            — sets $DIR=/…/tests/operational
#   * vidrush_media_e2e.sh      — sets $DIR=/…/tests/operational
#   * artlist/{01..09}/*.sh     — may set $DIR=/…/tests/operational/artlist
# In all cases the lib files live next to THIS file, not next to the
# caller. BASH_SOURCE[0] is the canonical bash idiom for "where am I" and
# is unaffected by caller state — it makes the umbrella path-invariant.
THIS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${THIS_DIR}/common.sh"
source "${THIS_DIR}/drive.sh"
source "${THIS_DIR}/qdrant.sh"
source "${THIS_DIR}/sqlite.sh"
source "${THIS_DIR}/artlist.sh"
source "${THIS_DIR}/velox_domain.sh"
source "${THIS_DIR}/artlist_runtime.sh"

# ── artlist_dod_assert_helpers_loaded — defensive self-validation ──
# Pure-function check (no side-effects on caller state) that fires
# ONCE at import time and fails fast if any expected helper from
# the canonical chain is missing. This is the godlike/06 SSOT
# enforcement: a refactor that removes a helper from a lib file
# would otherwise silently break consumers at first invocation;
# this guard surfaces the regression at IMPORT time which is the
# earliest observable point for downstream tests / CI.
#
# Implementation note: `type -t <fn>` returns "function" when
# defined, "alias" when aliased, "file" when it's a sourced external,
# and the empty string when the name is unbound. We only accept
# "function" (canonical SSOT). Helper names are the canonical set
# the 9 sub-batteries + 05_pipeline_fresh.sh + restart_verification.sh
# depend on. Adding a new name here forces every consumer to either
# source this umbrella (preferred) or define a thin stub that
# forwards to the canonical lib.
artlist_dod_assert_helpers_loaded() {
    local missing=()
    for fn in \
        smoke_curl \
        smoke_sqlite_query \
        smoke_poll_terminal \
        smoke_ffprobe_check \
        smoke_echo_safe \
        smoke_log_section \
        log_pass \
        log_fail \
        log_info \
        log_warn \
        velox_qdrant_assert \
        velox_drive_resolve \
        velox_artlist_pipeline_run \
        velox_artlist_detail \
    ; do
        # Prefer `declare -F` for bash 4+ native function introspection
        # (returns 0 if defined as function, 1 otherwise) — doesn't
        # accidentally count aliases as functions. Idempotent: safe
        # to call multiple times within one script invocation.
        if ! declare -F "${fn}" >/dev/null 2>&1; then
            missing+=("${fn}")
        fi
    done
    if (( ${#missing[@]} > 0 )); then
        printf '❌ _artlist_common.sh: missing canonical helpers: %s\n' \
            "$(printf '%s ' "${missing[@]}")" >&2
        printf '   One of the lib/ files below did not define the expected helper.\n' >&2
        printf '   This guard enforces godlike/06 SSOT — investigate the failing helper in lib/.\n' >&2
        return 1
    fi
    return 0
}

# ── authoritative version-ID for downstream consumers ──
# External callers can `source _artlist_common.sh && echo
# "$ARTLIST_DOD_LIB_VERSION"` to detect a refactor wave that broke
# their assumption. Bumped when:
#   (a) a NEW helper is added to the canonical chain (a future gate_*
#       primitive extraction)
#   (b) a helper is REMOVED from the chain
#   (c) a helper's signature changes (parameter count or semantics)
readonly ARTLIST_DOD_LIB_VERSION="1.1.0-gate1-detail-helper-july-2026"

# ── auto-validate at import time unless explicitly skipped ──
# The env var ARTLIST_DOD_LIB_SKIP_ASSERT=1 lets an operator debug
# the umbrella itself without the guard masking the underlying issue
# (e.g., probing which specific helper is missing). Default behaviour
# is to validate at import — fail-closed per AGENTS.md "Never
# represent an unavailable backend as a successful no-op."
if [[ "${ARTLIST_DOD_LIB_SKIP_ASSERT:-0}" != "1" ]]; then
    if ! artlist_dod_assert_helpers_loaded; then
        printf '   Hint: re-source with ARTLIST_DOD_LIB_SKIP_ASSERT=1 to bypass the guard.\n' >&2
        # Exit (not return) — the umbrella is documented to be sourced at
        # top-level only. Per AGENTS.md Simplicity & Minimalism: a single
        # exit path is clearer than a belt-and-braces return||exit dual
        # form for a top-level-only surface.
        exit 1
    fi
fi
