# Legacy Directory Migration Maps

This directory contains the per-directory migration maps referenced from
`AGENTS.md` § "Legacy Directories Policy". Each map has the same shape:

1. **Current state** — package names + public symbols currently exported.
2. **Migration target** — the canonical path that should absorb the symbol.
3. **Audit results** — every grep performed to prove the migration is safe,
   including importers and string-literal usages.
4. **Cut-over steps** — exact PR-ordered steps to migrate without breaking
   the build at any intermediate state.
5. **Status** — `pending | in-progress | done`.

| Legacy directory | Target | Status | Owner | Map |
|---|---|---|---|---|
| `internal/core/`                 | `internal/domain/asset/` or `internal/infrastructure/<X>/` | pending | Wave-13 follow-up | [internal-core.md](internal-core.md) |
| `internal/media/`                | split across domain / application / infrastructure          | pending | Wave-14          | [internal-media.md](internal-media.md) |
| `internal/assets/`               | `internal/domain/asset/`                                    | pending | Wave-14          | [internal-assets.md](internal-assets.md) |
| `internal/artifacts/`            | `internal/domain/job/` (eliminate, interface-wrap)          | pending | Wave-15          | [internal-artifacts.md](internal-artifacts.md) |
| `internal/sources/{youtube,artlist}/` | `internal/application/assets/providers/<X>/`             | in-progress | Wave 12          | [internal-sources.md](internal-sources.md) |
| `internal/upload/drive/`         | `internal/infrastructure/drive/`                            | pending | Wave-15          | [internal-upload.md](internal-upload.md) |
| `internal/application/scriptflow/` | `internal/application/scripts/<X>/`                      | **done** | Wave 6 | [internal-application-scriptflow.md](internal-application-scriptflow.md) |
| `internal/domain/media/`         | `internal/domain/asset/`                                    | pending | Wave-14          | [internal-domain-media.md](internal-domain-media.md) |
| `internal/domain/worker/`        | `internal/domain/job/` (DELETE — duplicates `domain/job`)   | **done** | this PR         | [internal-domain-worker.md](internal-domain-worker.md) |
| `internal/domain/outbox/`        | `internal/domain/lifecycle/` (DELETE — duplicates `outboxevents`) | **done** | this PR         | [internal-domain-outbox.md](internal-domain-outbox.md) |

The PR that introduced the legacy-directory CI guard (`scripts/ci-architectural-checks.sh`
Check 13) is the one that executes the rows marked **done**. Future PRs work left
to right, top to bottom.

## How each map is structured

Each `*.md` map below:
- lists the **public symbols** currently exported (typed exports, constants,
  vars) via `grep -E '^const|^var|^type|^func' <file>`.
- lists every **importer** via `rg -l 'pipelinegen/internal/<legacy>/' --type go`.
- lists every **string-literal** reference to the constant values (e.g. for an
  `EventAssetIndexRequested = "asset.index.requested"` constant, we grep the
  literal string, not just the import).
- prescribes a **cut-over recipe** that the next PR can copy.

A directory can be **deleted entirely** when both rows of importers + string
literals are empty (or only contain references to the canonical duplicate).
That conclusion is what flipped the two rows above to **done** in this PR.
