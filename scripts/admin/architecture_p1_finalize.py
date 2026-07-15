#!/usr/bin/env python3
"""Normalize P1 architecture ratchet metadata before verification.

This helper is intentionally idempotent. It removes duplicated live baseline
numbers from the legacy CI runner and split capability baseline while retaining
dated targets.
"""

from pathlib import Path


def normalize_ci_runner() -> None:
    path = Path("scripts/ci-architectural-checks.sh")
    lines = path.read_text(encoding="utf-8").splitlines()
    out: list[str] = []
    in_c47 = False
    skip_c47_bootstrap = False
    skip_c47_remediation = 0
    skip_c48_bootstrap = False

    for line in lines:
        if "# ── Check 47:" in line:
            in_c47 = True
        if "# ── Check 48:" in line:
            in_c47 = False

        if in_c47 and line.startswith("# Transitional baseline"):
            out.extend(
                [
                    "# Monotonic ratchet: current is scanned from the working tree and",
                    "# the ceiling is scanned from HEAD^. Non-zero manual baselines are",
                    "# forbidden; dated intermediate caps live in the gate implementation.",
                    'echo "=== Check 47: C2-C no-source-switch-outside-catalog (Blocco C2, June 2026) ==="',
                    "c2c_out=$(go run -tags=c2_source_catalog_only -- ./scripts/archcheck/gates/gate_c2_source_catalog_only_main.go . 2>&1) || c2c_rc=$?",
                ]
            )
            skip_c47_bootstrap = True
            continue
        if skip_c47_bootstrap:
            if "c2c_out=$(" in line:
                skip_c47_bootstrap = False
            continue

        if in_c47 and 'echo "To advance the transitional baseline' in line:
            out.extend(
                [
                    '    echo "The ceiling is derived from HEAD^; reduce violations in code."',
                    '    echo "Do not edit a duplicated baseline number to make this gate pass."',
                ]
            )
            skip_c47_remediation = 2
            continue
        if skip_c47_remediation > 0:
            skip_c47_remediation -= 1
            continue
        if "with remaining-allowance info if --baseline" in line:
            out.append("# Print the gate's code-derived current/parent/target line verbatim.")
            continue

        if line.startswith("fi    # PR-CHECK-5-FOLLOWUP"):
            out.extend(
                [
                    "fi",
                    "# Current drift is derived from the artifacts and the ceiling from",
                    "# HEAD^. Non-zero manual baselines are forbidden.",
                    'c2e_out=$(go run -tags=c2_route_manifest -- ./scripts/archcheck/gates/gate_c2_route_manifest_main.go --root="${REPO_ROOT}" 2>&1) || c2e_rc=$?',
                ]
            )
            skip_c48_bootstrap = True
            continue
        if skip_c48_bootstrap:
            if "c2e_out=$(" in line:
                skip_c48_bootstrap = False
            continue

        out.append(line)

    path.write_text("\n".join(out) + "\n", encoding="utf-8")


def normalize_inventory() -> None:
    path = Path("architecture/capability_inventory/baseline.yaml")
    lines = path.read_text(encoding="utf-8").splitlines()
    out: list[str] = []
    gate = ""
    inserted: set[str] = set()

    for line in lines:
        stripped = line.strip()
        if stripped.startswith("- gate_id:"):
            gate = stripped.split(":", 1)[1].strip()

        if gate in {"C2-C", "C2-E"}:
            if stripped.startswith("current:") or stripped.startswith("baseline_initial:"):
                continue
            if stripped.startswith("baseline_target:"):
                out.append('    target_deadline: "2026-09-15"')
                continue
            if stripped.startswith("current_source:") or stripped.startswith("ceiling_source:"):
                continue
            if stripped.startswith("milestones:"):
                continue
            if stripped.startswith("enforcing_test:"):
                target = (
                    "scripts/archcheck/gates/gate_c2_source_catalog_only_main.go"
                    if gate == "C2-C"
                    else "scripts/archcheck/gates/gate_c2_route_manifest_main.go"
                )
                out.append(
                    f"    enforcing_test: {target} (current and parent ceiling are scanned directly; no duplicated manual baseline)"
                )
                continue
            if stripped.startswith("ratchet_status:"):
                out.append("    ratchet_status: code_derived_monotonic")
                continue
            if stripped.startswith("enforce_zero:") and gate not in inserted:
                out.append(line)
                out.append("    current_source: runtime_code_or_artifact_scan")
                out.append("    ceiling_source: previous_commit_scan")
                out.append("    milestones:")
                caps = (
                    [("2026-08-01", 36), ("2026-08-15", 24), ("2026-09-01", 12), ("2026-09-15", 0)]
                    if gate == "C2-C"
                    else [("2026-08-01", 128), ("2026-08-15", 86), ("2026-09-01", 43), ("2026-09-15", 0)]
                )
                for due, cap in caps:
                    out.append(f'      - {{ due: "{due}", max_violations: {cap} }}')
                inserted.add(gate)
                continue

        if gate in {"C2-C", "C2-E"} and stripped.startswith("- { due:"):
            continue
        out.append(line)

    path.write_text("\n".join(out) + "\n", encoding="utf-8")


def normalize_debt_policy() -> None:
    path = Path("architecture/policy.yaml")
    policy = path.read_text(encoding="utf-8")
    start = policy.index("# ── DEBT BUDGET")
    end = policy.index("# Wave-22 hard-gate promotion", start)
    replacement = """# ── DEBT BUDGET (weighted, code-enforced) ────────────────────────────────
# The strict archcheck scores PRE-EXISTING-* entries directly from
# architecture/issues.yaml. open=1.0 and in_progress=0.5. in_progress requires
# concrete implementation evidence, and an open -> in_progress transition must
# accompany a non-governance code or operational change.
debt_budget:
  max_pre_existing_open: 9
  catalog_file: architecture/issues.yaml
  counter_id_prefix: "PRE-EXISTING-"
  counter_status_includes: ["open", "in_progress"]
  counter_status_weights: "open=1.0,in_progress=0.5"
  in_progress_requires_evidence: true
  ci_gate: cmd/archcheck/debt_budget.go

"""
    path.write_text(policy[:start] + replacement + policy[end:], encoding="utf-8")


def main() -> None:
    normalize_ci_runner()
    normalize_inventory()
    normalize_debt_policy()


if __name__ == "__main__":
    main()
