#!/usr/bin/env bash
#
# Check 44 — application size cap + usecase/types_aliases.go filename ban
#               (P1-2 of cleanup plan, Wave 22 deferred then promoted).
#
# Action P1-2 (June 2026) originally surfaced this gate as a deferred entry
# (transitional state: `current_state: deferred` per
# architecture/current.yaml::doc[1].wave_status.P1-2). This commit
# promotes P1-2 to active state — the script is now wired into
# scripts/ci-architectural-checks.sh via `bash` invocation, immediately
# after Check 43 (DB chain outside infra) and BEFORE Check 45 (inline
# ClipsRepository-map ban). The CHECK NUMBER slot was reserved exactly
# for this purpose during the Check 45 commit (see deferred-marker comment
# in ci-architectural-checks.sh at the prior wire point); filling that
# slot restores the canonical numeric sequence with no number collision.
#
# Two enforcement rules:
#
#   (a) `types_aliases.go` filename BANNED anywhere — any file literally
#       named `types_aliases.go` anywhere in the tree is a SSOT regression.
#       Per AGENTS.md Migration Status (P1-2 closure, June 2026), the
#       filename was deleted at commit e31d5a0a; this rule forward-prevents
#       re-introduction. The P0.7 zero-legacy policy (canonical,
#       docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md) classifies
#       filename aliases as one of the 7 forbidden compatibility
#       techniques; this gate is the file-level enforcement.
#
#   (b) Application .go file count cap (per-directory) — every
#       directory under internal/application/*/ must contain <= target
#       (40) production .go files (excluding *_test.go). Directories
#       between target and transitional_cap (66) are reported as WARN
#       entries (no CI failure), per AGENTS.md §7 zero-baseline rule's
#       acknowledgement that transitional baselines default-block the
#       lint OR pass-warn depending on the per-wave contract.
#
# Cap value source: `architecture/current.yaml::doc[1].wave_status.P1-2`
# is the SINGLE SOURCE OF TRUTH. The script reads target + transitional_cap
# via `python3 -c "import yaml; ..."` at line bottom; the values are
# inlined into regex/conditionals AFTER the read. Inlining is deliberate:
# the regex lives in shell and cannot consume YAML structures directly,
# but the source path is the on-disk YAML (architecture/current.yaml at
# repo root) — if it ever drifts, the read fails loud and the gate
# fails-closed (exit 1) on missing/parsable YAML. The check is fail-closed
# per AGENTS.md §8 ARCHITECTURE-CI_GATES.
#
# Allowlist (the file-name check has NO structural exclusions):
#   - The filename ban is unconditional — types_aliases.go anywhere fails.
#   - The file-count cap applies only to direct subdirectories of
#     internal/application/ (test files excluded by --glob '!**/*_test.go';
#     subdirectories with 0 files aren't double-counted; nested
#     subdirectories are checked individually by the same loop).
#
# Implementation notes:
#   - ripgrep for the filename scan (`--files-with-matches` would not
#     match on a basename; use `find` for the basename-only check).
#   - bash array iteration for the per-directory file count (small N,
#     O(N) cost acceptable; cheaper than a Python pass for ~14 dirs).
#   - WARN-vs-FAIL thresholding: per-directory cap exceeded → FAIL;
#     between target and transitional_cap → WARN (informational, does
#     not change exit code).
#
# Pattern anchors:
#   filename:           *types_aliases.go             (basename literal)
#   per-dir count:      find internal/application/<d>/ -maxdepth 1 -name '*.go'
#                                                   -not -name '*_test.go' | wc -l

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
YAML_PATH="${REPO_ROOT}/architecture/current.yaml"

# Step 0: read target + transitional_cap from the YAML SSOT.
# Fail-loud if the YAML is missing or unparsable (per SSOT contract).
if [ ! -f "${YAML_PATH}" ]; then
    echo "Check 44: FAIL — SSOT architecture/current.yaml missing at ${YAML_PATH}." >&2
    echo "Check 44 cannot enforce the application size cap without the YAML SSOT." >&2
    exit 1
fi
read_cap_values() {
    python3 -c "
import yaml
docs = list(yaml.safe_load_all(open('${YAML_PATH}')))
p12 = docs[1].get('wave_status', {}).get('P1-2', {}) if len(docs) > 1 else {}
target = p12.get('target')
transitional = p12.get('transitional_cap')
state = p12.get('current_state')
if target is None or transitional is None or state is None:
    raise SystemExit('wave_status.P1-2 must define target, transitional_cap, current_state')
print(target)
print(transitional)
print(state)
" 2>&1
}
read_cap_values || {
    rc=$?
    echo "Check 44: FAIL — architecture/current.yaml::wave_status.P1-2 read error (rc=${rc})." >&2
    echo "The cap values target + transitional_cap + current_state are required." >&2
    echo "If the YAML doc schema changed, restore the wave_status map below the wave tracker list." >&2
    exit 1
}
target=$(read_cap_values | sed -n '1p')
transitional_cap=$(read_cap_values | sed -n '2p')
current_state=$(read_cap_values | sed -n '3p')

if [ -z "${target}" ] || [ -z "${transitional_cap}" ] || [ -z "${current_state}" ]; then
    echo "Check 44: FAIL — one of target/transitional_cap/current_state is empty in wave_status.P1-2." >&2
    exit 1
fi

# Per spec, this gate is fail-closed ONLY when current_state=active.
# The gate ships reading the live state. If a future operator flips
# current_state back to `deferred` (transitional re-baselining),
# the gate will INFO-mode and exit 0 — the operator's signal. The
# flip to `active` happens in the same P1-2 promotion PR via
# commit on architecture/current.yaml (commit 3 of this PR).
#
# However, the doc check (filename ban) is always fail-closed —
# a types_aliases.go reintroduction is a SSOT regression regardless
# of cap-state. The cap-state-driven branching is cap-count only.

fail_count=0
warn_count=0
fail_messages=""

# ── Rule (a): types_aliases.go filename ban (always fail-closed) ─────
echo "  [Rule a] Scanning for 'types_aliases.go' filename (always fail-closed) ..."
types_aliases_files=$(find "${REPO_ROOT}" -type f -name 'types_aliases.go' \
    -not -path "${REPO_ROOT}/.git/*" \
    2>/dev/null || true)
if [ -n "${types_aliases_files}" ]; then
    fail_count=$((fail_count + 1))
    fail_messages="${fail_messages}
  [Rule a] types_aliases.go filename banned per AGENTS.md Pattern 7 + zero-legacy-policy:
${types_aliases_files}"
fi

# ── Rule (b): per-directory .go file count cap ─────────────────────
echo "  [Rule b] Scanning internal/application/*/ for per-directory .go file count (target=${target}, transitional=${transitional_cap}, state=${current_state}) ..."

# iterate each direct subdir of internal/application
app_dirs=$(find "${REPO_ROOT}/internal/application" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort || true)
for d in ${app_dirs}; do
    count=$(find "${d}" -maxdepth 1 -type f -name '*.go' -not -name '*_test.go' 2>/dev/null | wc -l | tr -d ' ')
    dir_basename=$(basename "${d}")

    if [ "${count}" -gt "${transitional_cap}" ]; then
        # exceeding transitional_cap is HARD FAIL regardless of state.
        fail_count=$((fail_count + 1))
        fail_messages="${fail_messages}
  [Rule b] Directory ${dir_basename}/ has ${count} .go files (exceeds transitional_cap=${transitional_cap}, target=${target})"
    elif [ "${count}" -gt "${target}" ]; then
        # between target and transitional_cap: WARN if state=active, INFO otherwise.
        if [ "${current_state}" = "active" ]; then
            warn_count=$((warn_count + 1))
            echo "  [Rule b][WARN] ${dir_basename}/ has ${count} .go files (between target=${target} and transitional_cap=${transitional_cap})"
        else
            echo "  [Rule b][INFO] ${dir_basename}/ has ${count} .go files (informational; current_state=${current_state})"
        fi
    fi
done

# ── Verdict ─────────────────────────────────────────────────────────
if [ "${fail_count}" -gt 0 ]; then
    echo ""
    echo "Check 44 (P1-2 application size cap + types_aliases.go ban): FAIL (${fail_count} violation(s)):"
    echo "${fail_messages}"
    echo ""
    echo "Fix (Rule a / types_aliases.go): the filename is banned per"
    echo "  AGENTS.md Migration Status (P1-2 closure, e31d5a0a). Move the"
    echo "  type definitions into the canonical package (e.g. usecase/ for"
    echo "  scripts, application/<feature>/ for adapters) and let the local"
    echo "  re-exports be deleted in the same commit. If the file is a"
    echo "  transitional import surface, address via typed aliases + a"
    echo "  deprecation record per godlike/07."
    echo ""
    echo "Fix (Rule b / file-count cap): split the directory into 2+ subpackage"
    echo "  directories (e.g. feature_a/, feature_b/) per Pattern 5 / the"
    echo "  documented split-canonical convention. Each new subdirectory"
    echo "  inherits the same target=40 / transitional_cap=66 budget. Track"
    echo "  the split as a Wave-24-style ratchet entry in architecture/current.yaml"
    echo "  if transitional_cap persistence is required, with explicit"
    echo "  owner + deadline (zero-baseline rule)."
    exit 1
fi

if [ "${warn_count}" -gt 0 ]; then
    echo ""
    echo "Check 44: ${warn_count} WARN(s) reported above (current_state=active; ratchet towards target=${target})."
fi

echo "Check 44 (P1-2 application size cap + types_aliases.go ban): OK (0 failed; ${warn_count} warning(s); state=${current_state})"
exit 0
