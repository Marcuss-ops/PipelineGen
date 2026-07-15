# DEBT BUDGET — Operator Procedure

> **Doctrine:** unresolved carry-forward debt must remain bounded and visible. Changing a status label is not a fix.
>
> **Canonical cap:** `architecture/policy.yaml::debt_budget.max_pre_existing_open`. Enforcement runs in `go run ./cmd/archcheck --strict`, which is part of `make verify-main`.

## What counts

Every `architecture/issues.yaml` entry whose `id` starts with `PRE-EXISTING-` contributes to the weighted score:

- `status: open` contributes **1.0**.
- `status: in_progress` contributes **0.5**.
- `resolved` and `wontfix` contribute zero and should be removed from the active catalog after closure according to the repository lifecycle rule.

The configured integer cap is interpreted as the maximum weighted score. For example, a cap of 9 allows nine open entries, or eight open plus two valid in-progress entries.

## `in_progress` is evidence-gated

An entry may use `status: in_progress` only when it has concrete implementation evidence. The strict gate accepts either:

1. a resolvable commit SHA referenced by `implementation_ref`, `tracking_issue`, or `evidence_filename`; or
2. a non-empty evidence file that exists in the repository.

A transition from `open` to `in_progress` is rejected when the same commit changes only governance metadata. The transition commit must contain a non-governance code or operational modification. This prevents a status-only edit from unblocking CI.

Recommended explicit form:

```yaml
- id: PRE-EXISTING-N-EXAMPLE
  status: in_progress
  implementation_ref: "<landed-or-active-commit-sha>"
  evidence_filename: "tests/operational/example_evidence.md"
```

## Failure output

The gate reports the weighted score and lists entries with their contribution:

```text
debt budget: weighted PRE-EXISTING score 9.5 exceeds cap 9.0
PRE-EXISTING-1-...(open=1.0)
PRE-EXISTING-2-...(in_progress=0.5)
```

It also fails independently when an `in_progress` entry lacks concrete evidence, even when the total score remains below the cap.

## Triage order

1. Sort by severity, then by oldest `opened_date`.
2. Resolve the underlying issue and land the code or operational change.
3. Attach a concrete commit/evidence reference.
4. Move to `in_progress` only while real work is associated; move to `resolved` immediately after closure.
5. Run `go run ./cmd/archcheck --strict` and then `make verify-main`.

## Forbidden shortcuts

- Renaming `PRE-EXISTING-*` to escape the prefix filter.
- Changing `open` to `in_progress` without an associated implementation.
- Marking an entry resolved without a landed fix and evidence.
- Raising the policy cap without an owner, intermediate deadline, and reduction plan.
- Adding an environment variable that disables the gate.

## Ownership

- Policy cap: `architecture/policy.yaml::debt_budget`.
- Active catalog: `architecture/issues.yaml`.
- Executable weighted gate: `cmd/archcheck/debt_budget.go`.
- Strict entry point: `cmd/archcheck/main.go`.
- Legacy Check 64 remains a secondary compatibility check; it is no longer the authoritative scoring implementation.
