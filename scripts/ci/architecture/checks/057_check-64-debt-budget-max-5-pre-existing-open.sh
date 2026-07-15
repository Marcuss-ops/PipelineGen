# ── Check 64: DEBT BUDGET (max 5 PRE-EXISTING open) ─────────────
# Caps the cumulative open carry-forward surface in
# architecture/issues.yaml. Per architecture/policy.yaml::debt_budget
# (`max_pre_existing_open: 5`) + docs/operations/debt-budget.md, every
# entry whose id starts with `PRE-EXISTING-` AND status == "open"
# counts toward the cap. `in_progress` and `resolved` are deliberately
# NOT counted: transitioning an `open` entry to `in_progress` unblocks
# CI, incentivising operators to start work immediately rather than
# letting debt rot. Cap-increase requires a documented SSOT-marker PR
# (godlike/06 one-owner-per-fact); AGENTS.md YAGNI doctrine + godlike/07
# fail-closed: there is NO env-flag to flip the gate off.
#
# Pattern anchors:
#   `kind == "PRE-EXISTING-*"` (literal prefix on `id`)
#   `status == "open"` (literal exact-match)
#
# Fail mode: godlike/07 fail-closed. If the YAML is unparseable, the
# gate falls back to fail-closed too (no silent pass-through) — the
# canonical godlike/07 contract: never let a missing/invalid artefact
# represent itself as a passing validation.
#
# YAML reader reuses the python3 heredoc pattern from
# extract_known_acceptable_ids_from_yaml (this file's top section) so
# the parser-surrogate lives at a single canonical site.
echo "=== Check 64: DEBT BUDGET (max 5 PRE-EXISTING open) ==="
debt_budget_output=""
debt_budget_rc=0
debt_budget_output=$(python3 -c '
import sys, yaml
try:
    with open("architecture/issues.yaml", "r", encoding="utf-8") as f:
        docs = yaml.safe_load(f)
    issues = docs.get("issues", []) if isinstance(docs, dict) else []
    cap = 5
    offenders = [
        str(it.get("id", ""))
        for it in issues
        if isinstance(it, dict)
        and str(it.get("id", "")).startswith("PRE-EXISTING-")
        and it.get("status") == "open"
    ]
    if len(offenders) > cap:
        sys.stderr.write("FAIL: PRE-EXISTING open count = %d > %d (DEBT BUDGET cap=%d)\n" % (len(offenders), cap, cap))
        for oid in offenders:
            sys.stderr.write("  - %s\n" % oid)
        sys.stderr.write("\n")
        sys.stderr.write("Fix: follow docs/operations/debt-budget.md procedure:\n")
        sys.stderr.write("  1. Migrate one of the offenders to `resolved` (preferred;\n")
        sys.stderr.write("     evidence_filename MUST cite the fix artifact) OR\n")
        sys.stderr.write("     `in_progress` (valid intermediate; unblocks CI).\n")
        sys.stderr.write("  2. Do NOT rename id to drop the PRE-EXISTING prefix.\n")
        sys.stderr.write("  3. Do NOT env-gate the gate off (no DEBT_BUDGET_STRICT\n")
        sys.stderr.write("     flag by design — YAGNI + godlike/07 fail-closed).\n")
        sys.stderr.write("  4. Lifting the cap requires a SSOT-marker PR (see\n")
        sys.stderr.write("     architecture/policy.yaml::debt_budget+lint_gates rationale).\n")
        sys.exit(2)
    print("PRE-EXISTING open count = %d (cap = %d)" % (len(offenders), cap))
except (yaml.YAMLError, OSError, UnicodeDecodeError) as e:
    # godlike/07 fail-closed: a missing/unreadable catalogue MUST not
    # silently pass the gate. Surface the failure to stderr + exit 2
    # so the wrapper below propagates exit 1.
    sys.stderr.write("FAIL: architecture/issues.yaml is broken or unreadable (godlike/07 fail-closed): %s\n" % e)
    sys.exit(2)
' 2>&1) || debt_budget_rc=$?
if [ "${debt_budget_rc}" -ne 0 ]; then
    printf '%s\n' "${debt_budget_output}" >&2
    exit 1
fi
echo "OK: DEBT BUDGET respected -- ${debt_budget_output}"

