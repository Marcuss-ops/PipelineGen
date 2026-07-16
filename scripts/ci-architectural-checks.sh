#!/usr/bin/env bash
# scripts/ci-architectural-checks.sh — CI gate for the architectural checker.
#
# PR-A (June 2026): adds the `--future-ratchet` flag so the 5 Phase 0 rules
# (interface{}/any growth, setter detector, cross-package type alias,
# fake route, handler-to-DB) run in baseline-on-baseline ratchet mode.
# During the minor cycle (this script's current state) the gate fails ONLY
# on regressions vs scripts/archcheck/phase0_baseline.json; existing
# entries in the baseline are accepted.
#
# PR-B (June 2026): adds Check 0 — forbid literal job-type strings outside
# the canonical domain/job/job.go decl. Four canonical constants live there
# (TypeBatchScriptGenerate, TypeClipScriptGenerate, TypeCatalogScriptGenerate,
# TypeMediaCurate); every consumer should reference them by name. New
# quoted-string occurrences of those values outside the canonical decl are
# a SSOT regression and fail this gate.
#
# PR-D (June 2026): adds Check 4 — migration version uniqueness lint.
# Fails when two or more files in `migrations/sqlite/` share the leading
# numeric version prefix. Historically observed as the `069_*.sql` × 2
# incident (surface: composition-test panic at server startup) — the
# duplicate-prefix collision silently picks one candidate at runtime.
# Renumbered to Check 4 because Checks 0/1/2/3 were already claimed by
# PR-B, QDRANT-002, QDRANT-001, and PR 6 (engine.Generate SSOT gate)
# respectively.
#
# PR 6 (June 2026): adds Check 3 — forbid `engine.Generate()` outside
# the canonical `GenerateOneUseCase`. Engine access must flow through
# the typed pipeline orchestrator; any direct call is a SSOT regression.
# Check 3 was introduced on origin/main after this branch forked; the
# merge here places PR-D's lint as Check 4 to avoid the collision.
#
# Promote-to-required checklist (separate follow-up PR):
#   1. Drop `--future-ratchet` from the command line below.
#   2. Fold runPhase0Checks() into runRatchetChecks() in
#      scripts/archcheck/main.go.
#   3. Update docs/architecture/godlike/14_INITIAL_BACKLOG.md — mark
#      Block 1 + the 5 Phase 0 rules as verified_zero: true.
set -euo pipefail

# ── Dispatcher-local REPO_ROOT resolution ─────────────────────────────────────
# Moved verbatim out of the wave-tracker section. The dispatcher needs
# SCRIPT_DIR + REPO_ROOT set BEFORE sourcing any sub-script because
# BASH_SOURCE[0] inside a sourced file refers to the sourced file (not
# the dispatcher). Computing these here in the dispatcher keeps the
# BASH_SOURCE[0]-fail-fast behaviour byte-identical to the original.
if [ -n "${BASH_SOURCE[0]:-}" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  echo "CI: cannot resolve script directory from BASH_SOURCE[0]=" >&2
  echo "    (process substitution / bash -c \"source ...\" invocation)." >&2
  echo "    Run the script as: bash scripts/ci-architectural-checks.sh" >&2
  echo "    or set MIGRATIONS_ROOT=/abs/path/to/migrations/sqlite explicitly." >&2
  exit 1
fi
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ── Dispatcher: source every sub-check in alphabetical order. ─────────────
# Variables defined by lib/01_wave_tracker.sh (sourced first because
# of its numeric prefix) and consumed downstream:
#   KNOWN_ACCEPTABLE_IDS, WAVE_BASELINE_SIZE,
#   is_known_acceptable, extract_known_acceptable_ids_from_yaml.
# REPO_ROOT / SCRIPT_DIR are resolved above (in this dispatcher).
#
# Sourcing (.) preserves the shared in-process state required by the
# wave-tracker allowlist, by --self-check, and by checks that read
# KNOWN_ACCEPTABLE_IDS / WAVE_BASELINE_SIZE.
LIB_DIR="${SCRIPT_DIR}/ci/architecture/checks/lib"
if [ ! -d "${LIB_DIR}" ]; then
  echo "CI: lib dir ${LIB_DIR} missing (ci-architectural-checks.sh split incomplete)" >&2
  exit 1
fi
for f in "${LIB_DIR}"/*.sh; do
  [ -f "${f}" ] || continue
  # shellcheck source=/dev/null
  source "${f}" "$@"
done
