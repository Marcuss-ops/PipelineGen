# Tooling caller matrix (2026-11-15)

Scope: `cmd/`, `make/*.mk`, and tracked scripts. Generated from the current tree
after the P1/P2 cleanup. This matrix is the precondition for any merge: a tool
is mergeable only when ownership is identical **and** no Make/CI caller contract
splits across the two entry points.

## `cmd/` tools

| Directory | Go files | LOC | `package main` files | Make/CI callers | Decision |
|---|---:|---:|---:|---|---|
| `cmd/admin` | 127 | 19,726 | 3 | `make/operations.tidy.mk`, `make/build.mk`, `make/verify.mk`, `.github/workflows/{manual,architecture,ci}.yml` | Keep: operator surface, no peer with identical ownership |
| `cmd/archcheck` | 94 | 20,131 | 8 | `make/verify.mk`, `.github/workflows/{architecture,ci}.yml` | Keep: governance gate |
| `cmd/architecture-aggregate` | 1 | 119 | 1 | `make/verify.mk` | Keep: single-purpose generator |
| `cmd/diag_chain` | 0 | 0 | 0 | none | Remove: empty directory |
| `cmd/goldenattribution` | 2 | 481 | 2 | none | Keep: standalone golden diagnostic |
| `cmd/goldensummary` | 3 | 784 | 3 | none | Keep: standalone golden diagnostic |
| `cmd/mediacert` | 1 | 102 | 1 | `make/vidrush.mk` | Keep: media certification contract |
| `cmd/model-registry-gen` | 1 | 114 | 1 | none | Keep: generator with no peer |
| `cmd/overlaystatus` | 1 | 57 | 1 | none | Keep: overlay operator probe |
| `cmd/semantic-extract` | 4 | 452 | 4 | none | Keep: semantic extraction CLI |
| `cmd/server` | 1 | 130 | 1 | `make/build.mk`, `.github/workflows/ci.yml` | Keep: server entrypoint |
| `cmd/tmp_chaincheck` | 0 | 0 | 0 | none | Remove: empty directory |
| `cmd/twin-ddl-gen` | 1 | 109 | 1 | none | Keep: DDL generator |
| `cmd/velox` | 8 | 2,175 | 8 | `make/build.mk` | Keep: video CLI family |
| `cmd/velox-knobs` | 1 | 47 | 1 | none | Keep separate: config inspector, no shared Make contract with `velox` |
| `cmd/worker` | 1 | 55 | 1 | `.github/workflows/ci.yml`, `make/build.mk` | Keep: worker entrypoint |

**Merge result:** zero merges. No two non-empty tools combine identical
ownership with no Make/CI split. `cmd/velox-knobs` is the only near-peer, but it
is a distinct config-inspection entry point with no build contract with
`cmd/velox`; merging it would be an unverified CLI change, not consolidation.

**Removed:** `cmd/diag_chain/` and `cmd/tmp_chaincheck/` (empty, zero tracked
files, zero callers).

## `make/*.mk` files

Total: 14 files, 1,739 lines. Every file is included from the root `Makefile`.

| File | Lines | Additional callers | Decision |
|---|---:|---|---|
| `make/audio.mk` | 24 | none | Keep |
| `make/build.mk` | 154 | none | Keep |
| `make/deploy.mk` | 46 | none | Keep |
| `make/docker.mk` | 119 | none | Keep |
| `make/live.mk` | 157 | references `verify.mk` | Keep |
| `make/operations.auth.mk` | 30 | none | Keep |
| `make/operations.smoke-voiceover.mk` | 24 | none | Keep |
| `make/operations.tidy.mk` | 330 | references `verify.mk` | Keep |
| `make/reconciliation.mk` | 31 | none | Keep |
| `make/test.mk` | 94 | none | Keep |
| `make/verify.components.mk` | 159 | none | Keep |
| `make/verify.mk` | 245 | `Makefile`, `.github/workflows/ci.yml`, `operations.tidy.mk`, `live.mk`, `youtube_stock.mk` | Keep: shared CI contract |
| `make/vidrush.mk` | 213 | `Makefile`, `verify.mk` | Keep: vidrush contract |
| `make/youtube_stock.mk` | 113 | `Makefile`, `verify.mk`, `.github/workflows/manual.yml` | Keep: CI contract |

**Merge result:** zero merges. `verify.components.mk` is the closest candidate
to `verify.mk`, but `verify.mk` is a shared include and CI target; folding it in
would change every caller in the table and is therefore a contract split, not a
cleanup.

## Tracked scripts

- 147 tracked `.sh`/`.bash`/`.py`/`.mjs`/`.js` files outside `internal/`, `pkg/`,
  `cmd/`, and `frontend/` (the earlier “131” count missed the `ops/jobs` and
  `scripts/ci` groups).
- Largest groups: `scripts/ci` (19), `scripts/` (17), `ops/jobs/remote` (14),
  `semantic_acceptance` (8), `ops/jobs/hiphop_36` (7),
  `scripts/services/embedding_server` (7), `scripts/bench/report` (6),
  `scripts/bridges` (6), `tests/operational` (6).
- No script is moved or merged by this matrix. Script consolidation needs a
  per-script caller table (Make target, CI workflow, operator runbook); the
  directory-level counts are not evidence of identical ownership.

## Acceptance evidence

- No Makefile target, CI workflow, or command entry point changed.
- `go build ./...` and `go run ./cmd/archcheck --strict` remain the gate.
- The matrix is the source of truth for the follow-up merge review.
