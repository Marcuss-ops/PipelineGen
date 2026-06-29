# CUTOVER-prep audit — qdrant sub-package migration

**Date:** 2026-06-29
**Migration sequence:** EXPAND → BACKFILL → CUTOVER → CONTRACT (per `godlike/07 §migration sequence`)
**Owner:** @pipeline-team
**Hard deadline:** 2026-07-15 (gate day-bomb per `5ac3d5dde` transitional allowlist on `docs/migrations/archcheck-2026-06-28-hard-gate-promotion.md`)

## Pre-state — Tier-1 EXPAND already landed

The Phase 2 EXPAND Tier-1 commit has shipped on `origin/main` and is the precondition for this audit:

- **Commit:** `bc1d6ab0` — `refactor(qdrant): EXPAND Tier-1 scaffolding — 5 capability sub-package doc.go`
- **5 sub-package directories** under `internal/infrastructure/qdrant/` with `package <name>` declarations + 1-line docs:
  - `connection/` — client transport, config, connection-pool plumbing
  - `collection/` — schema definitions, retention, blue-green switch, `collection_info`
  - `points/` — upsert, search, query, payload indexes, index writer/document
  - `dr/` — snapshots, restore, dr adapter (PR-QDRANT-WIRE-MIRROR BACKFILL target per deprecations.yaml record #9)
  - `reaper/` — point reaper (locator cleaner, payload merge, lifecycle culling)

Tier-1 ships **no content move**, **no consumer-import rewrite**, **no flat-file removal**. The package `internal/infrastructure/qdrant` is unchanged at 46 productive `.go` files. The check-16 package-size allowlist remains in WARN mode pre-expiry.

## Audit baseline — flat-path import surface

**Audit commands (verbatim, for reproducibility):**

```bash
# External consumer files (excludes the qdrant dir itself + tests):
rg -l 'internal/infrastructure/qdrant' --type go -g '!*_test.go' . \
  | grep -v '^./internal/infrastructure/qdrant' \
  | sort -u

# Top-imported symbols (ranked by external-consumer file count):
for sym in $(rg -o 'qdrant\.[A-Z][A-Za-z0-9_]*' --type go -g '!*_test.go' . | sort -u); do
  rg -l "${sym}\b" --type go -g '!*_test.go' . \
    | grep -v '^./internal/infrastructure/qdrant' \
    | sort -u \
    | wc -l \
    | xargs -I{} echo "{} ${sym}"
done | sort -rn | head -15
```

**Audit summary (post Tier-1, HEAD `bc1d6ab0`):**

| Metric | Value |
|---|---|
| External consumer files of `internal/infrastructure/qdrant` | **18** |
| Total `qdrant.X` short-name references across the codebase | **247** |
| Distinct exported `qdrant.*` symbols in the codebase | (audit-able via the rg command above) |

## Top-imported symbols → canonical capability sub-package mapping

The following top symbols are ranked by external-consumer file count and assigned to a sub-package per the Tier-1 EXPAND doc.go rationale.

| Symbol | External consumer files | Capability sub-package | Rationale |
|---|---:|---|---|
| `qdrant.Client` | 12 | `connection/` | The Client struct IS the connection transport (per `connection/doc.go`); groups naturally with its constructor. |
| `qdrant.NewClient` | 9 | `connection/` | Constructor for `Client`; lives next to its return type. |
| `qdrant.IndexSchema` | 1 | `collection/` | IndexSchema is the canonical schema-definition surface (per `collection/doc.go`). |
| `qdrant.SnapshotDescription` | 1 | `dr/` | DR-family canonical home, per `PR-QDRANT-WIRE-MIRROR` (`architecture/deprecations.yaml` record #9, status: in_progress). |
| `qdrant.EmbeddingSpec` | 1 | `collection/` | Embedding spec is schema-side. Migrates with `IndexSchema`. |
| `qdrant.DefaultSparseModel` | 1 | `collection/` | Schema-side sparse-model default constant. |

## Symbol migration sequencing (per-sub-package)

Each row below represents the next migration commit. The import path rewrite replaces `internal/infrastructure/qdrant` with `internal/infrastructure/qdrant/<capability>` in only the consumer files that reference that symbol.

| Migration commit | Capability sub-package | Symbols moved | # consumer files |
|---|---|---|---:|
| CUTOVER-connection | `connection/` | `qdrant.Client`, `qdrant.NewClient` (+ `qdrant.RuntimeConfig` if it materializes post-audit) | 12–21 |
| CUTOVER-collection | `collection/` | `qdrant.IndexSchema`, `qdrant.EmbeddingSpec`, `qdrant.DefaultSparseModel`, `qdrant.CollectionInfo`, `qdrant.SchemaDiff`, etc. | ≥3 |
| CUTOVER-dr | `dr/` | `qdrant.SnapshotDescription`, `qdrant.PointPayload` (per PR-QDRANT-WIRE-MIRROR) | ≥3 |
| CUTOVER-points | `points/` | point-side types (SearchRequest, HybridSearchRequest, IndexHealthReport, etc.) | ≥5 |
| CUTOVER-reaper | `reaper/` | reaper-side types (point reaper, locator cleaner, payload merge) | ≥3 |

The exact count per row is reproducible via the per-symbol rg command above.

## Out-of-scope: cross-package references discovered during audit

The following in-package references inside `internal/infrastructure/qdrant/*` block naive content move. They must be addressed by the **BACKFILL** phase before CUTOVER (per godlike/07, BACKFILL precedes CUTOVER):

1. `errors.go` references `SchemaDiff` (defined in `collection_types.go`). SchemaDiff is on the CUTOVER-collection row above, so by the time `errors.go` moves to `qdrant/errors/`, SchemaDiff will already live in `qdrant/collection/`.
2. `types_dr.go` defines `SnapshotDescription` (alias to `internal/domain/qdrantdr`) + `PointPayload`. These are the CUTOVER-dr row symbols; BACKFILL must move them to `qdrant/dr/` first.
3. `reaper.go` references `PointPayload` via short-name `qdrant.PointPayload`. Reaper must import `dr` once BACKFILL promotes `PointPayload` to `dr.PointPayload`.
4. `client_dr.go`, `verifier.go`, `client_payload_indexes.go` use the same short-name `qdrant.X` pattern; they are part of the CUTOVER-dr, CUTOVER-reaper, CUTOVER-points rows respectively.

The BACKFILL phase must complete **before** any CUTOVER commit to avoid build-break. The recommended ordering is per-capability BACKFILL → per-capability CUTOVER → per-capability CONTRACT, in that order.

## Post-CUTOVER exit criteria (Check 16 + Check 15 green)

- **Check 16 (`internal/<dir>/<pkg>` ≥40 productive files):**
  - Each new sub-package must hold ≤39 productive `.go` files at minimum; the Tier-1 commit already clears this for empty subdirs.
  - The flat `internal/infrastructure/qdrant/` must hold zero productive `.go` files after CONTRACT removes them.
  - The transitional baseline (`docs/migrations/archcheck-strict-baseline.json` item #N for qdrant) must shrink to zero.
- **Check 15 (file-size ≤500 LoC per file):**
  - The prior batches (Step 6 batches 1–6) already split the 46 oversized files out of the flat. The new sub-package files inherit the per-file split discipline.

## Deadline risk

| Risk | Mitigation |
|---|---|
| `2026-07-15` allowlist day-bomb promotes Check 15+16 to HARD FAIL (pre-expiry = WARN). | Phase 3 W3-W4 human-driven split tickets cover the contingency. |
| Per-symbol rg import-rewrite is mechanical but cumulative — a wrong symbol mapping silently compiles if multiple symbols share the same short-name. | CUTOVER commits run `go build ./...` + `go vet ./...` + targeted `go test ./internal/<consumer>` per commit; failure → `git reset` to last good commit. |
| Re-imports via the new sub-package paths create transient cycle risk during the BACKFILL → CUTOVER window. | Verify each BACKFILL commit is build-green INDEPENDENT of CUTOVER (flat files stay as backup until CUTOVER completes). |

## Tracking

- `architecture/current.yaml` Wave 24 entry (pending; this audit is the prerequisite).
- `architecture/deprecations.yaml` #9 PR-QDRANT-WIRE-MIRROR (SnapshotDescription canonical home).
- `architecture/ownership/infrastructure.yaml` (qdrant subzone per dc6add3e split) gains per-sub-package rows on CUTOVER completion (the Tier-1 commit already establishes these as canonical homes); the aggregated canonical view at [`architecture/ownership.generated.yaml`](../../architecture/ownership.generated.yaml) reflects these per-sub-package rows.

## Cross-references

- `docs/migrations/archcheck-2026-06-28-hard-gate-promotion.md` (Phase 2 EXPAND + future CONTRACT gate activation)
- `architecture/deprecations.yaml` #9 PR-QDRANT-WIRE-MIRROR
- `architecture/policy.yaml`::`max_files_per_package: 40` (Check 16 bound)
- godlike/07 §migration sequence (EXPAND → BACKFILL → CUTOVER → CONTRACT)
- godlike/11 §agent execution playbook (per-commit verification + remote-commit presence check)
