# DEBT BUDGET — Operator Procedure

> **Doctrine:** the open carry-forward surface must stay bounded. PRE-EXISTING-* catalog entries with `status: open` represent unresolved architectural drift; an unbounded backlog is a godlike/07 no-fake-availability violation (every open entry IS an unresolved operational risk).
>
> **Cap:** `max_pre_existing_open = 5` (architecture/policy.yaml::debt_budget). Enforced by `scripts/ci-architectural-checks.sh` Check 64 (fail-closed, exit 1 when count > 5). **No env flag** to flip the gate off (AGENTS.md YAGNI + godlike/07 fail-closed).

## What counts

`architecture/issues.yaml` entries with **both**:

1. `id` starts with `PRE-EXISTING-` (literal prefix match; the canonical "carry-forward" identity).
2. `status: open`. `in_progress` and `resolved` are deliberately NOT counted: transitioning an `open` entry to `in_progress` unblocks CI, so operators are incentivised to start work immediately rather than letting debt rot (`godlike/06 SSOT carry-forward` discipline).

PR-LIVE-VERIFY, P0-BUILD-FIX-1, P0-BOT-DRAIN-CONCERNS and other operational categories do NOT count toward the cap (they have their own cadence). Only the cumulative PRE-EXISTING backlog.

## When Check 64 fires (CI fails closed)

The output lists each offender:

```
FAIL: PRE-EXISTING open count = 9 > 5 (DEBT BUDGET cap=5)
  - PRE-EXISTING-1-WAL-PRAGMAS
  - PRE-EXISTING-2-INTERNAL-APP-COMPOSITION
  - PRE-EXISTING-3-ASSETS-MONITOR
  - PRE-EXISTING-4-ASSETS-PROVIDERS
  - PRE-EXISTING-5-ASSETS-ARTLIST
  - PRE-EXISTING-6-STOCKPIPELINE
  - PRE-EXISTING-7-USECASE-TRANSLATION
  - PRE-EXISTING-8-USECASE-CACHE-FINGERPRINT
  - PRE-EXISTING-9-ARCHCHECK-SNAPSHOT
```

## Triage procedure

When Check 64 fails, follow this order:

1. **Re-read the gate output** to enumerate the offenders (the script prints each `id`).
2. **Sort by severity + age**:
   - First: `p0` + oldest `opened_date`.
   - Then: `p1` + oldest.
   - Finally: `p2` / `p3` (low-stakes; can batch).
3. **For each, decide one of**:
   - **(a) Migrate to `resolved`** (preferred): the underlying root cause has a fix landed on `main`; the entry's `tracking_issue` + `evidence_filename` cite the artifact showing the fix (commit SHA, dry-run log, operator playbook entry). `resolved` entries DO NOT linger in `issues.yaml` per the file's lifecycle note (issues move to Git history).
   - **(b) Migrate to `in_progress`**: a fix is being actively developed; the entry has a concrete owner + a concrete closure ETA. The `in_progress` migration unblocks CI immediately. The follow-up PR/CI run MUST flip to `resolved` (or `wontfix`) once the work lands.
   - **(c) Defer explicitly via `tracking_issue`**: the entry has been redesignated onto a known future wave; the `tracking_issue` field is rewritten to point to the new plan doc. This is NOT a status flip, but DOES reduce the cognitive load on operators reading the catalog. A `_tracking_issue` rewrite DOES NOT unblock CI — only `(a)` or `(b)` does.
4. **Re-run Check 64** (locally: `bash scripts/ci-architectural-checks.sh | grep Check\ 64`). Repeat until `open` count ≤ 5.

## Anti-patterns (FORBIDDEN)

These behaviours look like progress but are godlike/07 debt-sweeping. Check 64 (and code review) watch for them.

1. **Renaming an entry's id** from `PRE-EXISTING-*` to something else (e.g. `REVAMPED-1-*`) to drop out of the cap. The id prefix is the canonical "carry-forward" identity; renaming to disguise debt is forbidden by godlike/07 zero-legacy-policy. CI cannot detect this — it's a code-review concern.
2. **Closing an entry to `resolved` without an actual fix.** The entry's `evidence_filename` must reference the artifact showing the fix landed (commit SHA on remote, dry-run log under `tests/operational/`, or operator playbook entry under `docs/operations/`). If no evidence exists, the flip is invalid.
3. **Removing the entry from `architecture/issues.yaml` silently.** Per the file's header comment, completed tickets live in Git history — NOT in the catalog. A `resolved` entry stays in the file until a maintenance PR removes it (the `git mv` history preserves the lead).
4. **Lifting `max_pre_existing_open` without a documented SSOT marker.** The cap is intentionally conservative. Raising it requires: a `lint_gates` entry explaining the increase (owner + deadline + transition plan in the rationale block, per the `lint_gates:` schema in `architecture/policy.yaml`), AND a corresponding Wave entry in `architecture/current.yaml::follow_up_tickets`. Both are committed in the same PR as the cap change.
5. **Env-gating the CI gate off.** There is no `DEBT_BUDGET_STRICT` flag. AGENTS.md YAGNI doctrine: no speculative hooks. The gate is unconditionally fail-closed.

## When to extend the catalog

A new `PRE-EXISTING-N-*` entry is opened ONLY when:

- A pre-existing failing-test / build-flag / probe is surfaced AFTER a commit merges onto `main` (not BEFORE — `in_progress` + `open` + a tracking PRPR construct is for known-at-merge-time debt).
- The discovery is reproduced on **pure main** (`git stash + reset`). The discovery MUST NOT be coupled to a wave commit's working tree residue. The `evidence_filename` cites the reproducer artifact.
- The `owner_capability` field names the package path that owns the surface.
- The `severity` follows the existing grid (`p0 = blocker; p1 = ship-gate-critical; p2 = verify-main-red; p3 = operational-polish`).

A new PRE-EXISTING entry INCREASES the open count by 1. If the cap is at limit (5), the new entry VIOLATES the gate on its own. Plan accordingly.

## Cross-references

- Policy rule: `architecture/policy.yaml::debt_budget`.
- CI gate: `scripts/ci-architectural-checks.sh` (Check 64 block — DEBT BUDGET max 5 PRE-EXISTING open).
- Catalog: `architecture/issues.yaml` (lives + dead entries).
- Wave tracker: `architecture/current.yaml` (cross-refs PRE-EXISTING-N entries under the relevant wave blocks).
- One-owner-per-fact: `docs/architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md`.
