# PipelineGen — Canonical Sources

> Single meta-doc answering the question *"what is authoritative here?"*.
> Per-doc content (what a doc covers, what rule applies where) lives in
> the doc itself — this file only meta-answers the *authority* question.
> If two docs disagree, this file is the pointer to the resolution.

## 1. Canonical sources

| What is canonical | Doc (read first) | What you'll find there |
|---|---|---|
| Agent-facing rules, AI generation policy, Git workflow lessons | [`AGENTS.md`](AGENTS.md) | DB driver lock, FTS5 ban, admin token, Git-Lessons 1-3 + Rebase-Conflict Lesson, `pkg/` utilities table, modular edit patterns |
| System structure, data flow, module ownership | [`ARCHITECTURE.md`](ARCHITECTURE.md) | Diagram, three canonical journeys, §1–§14, day-1 commands |
| Active wave migration state (ratchet tracker) | [`architecture/current.yaml`](architecture/current.yaml) | Wave-by-wave status monotone-decreasing, exit gates, residuals, tracking tickets, `follow_up_tickets` |
| Per-capability package owner | [`architecture/ownership.generated.yaml`](architecture/ownership.generated.yaml) | Aggregated view rebuilt byte-deterministically by `cmd/architecture-aggregate` from `architecture/ownership/*.yaml` |
| Live API surface | [`docs/api/ACTIVE_API_GENERATED.md`](docs/api/ACTIVE_API_GENERATED.md) | Auto-generated via `./admin gen-api-docs`; CI-fails if not regenerated on commit |

## 2. Git workflow

- **Direct-to-main only** — no topic branches for routine changes
  (cf. AGENTS.md Git-Lesson-2).
- Before push: `git fetch origin && git rebase origin/main` — cheap-exit
  recovery when `origin/main` advanced locally (Rebase-Conflict Lesson).
- Push: `git push origin main` always; never `--force`. `--force-with-lease`
  is last-resort after the amend-loop anti-pattern (Rebase-Conflict Lesson).
- Agent commits: `git -c user.email='agent@pipelinegen.local' \
    -c user.name='PipelineGen Agent' commit -F-` + trailer
  `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>` after a
  blank line (Git-Lesson-3). Verify with
  `git log --format='%(trailers)' -1 <sha>`.

## 3. Verify command

Every push must pass:

```bash
make verify-main
```

The gate runs lint (`scripts/ci-architectural-checks.sh`) + vet + tests +
package-size caps + capability ownership checks. CI mirrors it on PR
(when a branch exists — rare).

## 4. Target structure

The target-tree governance (Phase 0, June 2026) lives in
[`architecture/policy.yaml`](architecture/policy.yaml): package-size caps
(`max_files_per_package=40`, `max_lines_per_file=500`), top-level dir
restrictions, per-capability rules. Enforced by
`go run ./cmd/archcheck` (Phase 0 report-only; `--strict` promotes to
hard gate in later phases).

## 5. Historical / non-normative docs (read-only)

These contain *historical* context and are **read-only references** —
do NOT cite them as sources of truth. If a contradiction surfaces,
the canonical sources in §1 win.

- [`REPOSITORY_CLEANUP.md`](REPOSITORY_CLEANUP.md) — historical cleanup mission
  log (legacy; some references to removed `docs/architecture/*` and
  `docs/cleanup/README.md` paths survive as drift). Preserved for audit
  only.
- `docs/archive/migration-history.md` — wave 1–13 archived closure
  trajectories (pre-`architecture/current.yaml` format).
- `evidence/*` — milestone evidence dumps (worker image cert, rollout
  plans, verdicts). Per-event audit, not project canon.
- `architecture/archive/*` — deprecated tracker snapshots (Wave 1-12
  per-section ownership pre-dc6add3e).

If you find that one of these says something a §1 doc doesn't, the §1
doc wins — historical docs are *"this is what happened"*, not
*"this is what is true"*.
