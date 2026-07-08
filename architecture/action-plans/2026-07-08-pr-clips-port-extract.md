# PR-CLIPS-PORT-EXTRACT — Action Plan

**Date**: 2026-07-08
**Status**: PLANNING — docs lockstep only; implementation deferred
**Owner capability**: `internal/api/assets/clips` → `internal/application/clips/ports/`
**Wave-tracker anchor**: `architecture/current.yaml#PR-CLIPS-PORT-EXTRACT`
  (physical band file: `architecture/waves/wave_p3_low_and_audit.yaml` per codebase convention; slot DEFERRED per `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-6` forward-pointer)

---

## §0 — Honest status snapshot

**3 direct infrastructure imports in `clips/handler.go`** (verified at file load time):

| Import | Lines | Role | Direct or via alias |
|--------|-------|------|---------------------|
| `internal/infrastructure/drive` (`drive.Admin`) | 54, 104 | Drive-folder + upload surface | Direct |
| `internal/infrastructure/ai/semantic` (`semantic.MetadataWriter`) | 57, 205 | Clip metadata enrichment | Direct |
| `internal/infrastructure/indexing/clipindexer` (`clipindexer.Service`) | 58, 98, 204 | Clip-indexer upsert/delete | Direct |

These 3 imports are the canonical AGENTS.md Pattern 0 violation: a transport-layer package (`internal/api/`) directly imports infrastructure-layer types. The Pattern 0 fix is to declare typed ports in `internal/application/clips/ports/` and inject concrete adapters via the composition root.

**This action plan is a forward-pointer** — the implementation lands in a separate PR cycle (deadline 2026-08-15). The current commit is **docs-only** per user spec literal.

---

## §1 — Goal

Migrate `clips/handler.go` from **3 direct infra imports** to **3 typed-port dependencies** injected via the composition root. The 3 ports live at `internal/application/clips/ports/clip_ingest.go` (canonical SOLE owner per godlike/06 SSOT one-canonical-owner-per-fact):

1. **`ClipIngestDrivePort`** — narrow surface for Drive folder + upload operations
2. **`ClipIngestMetadataPort`** — narrow surface for clip metadata enrichment
3. **`ClipIngestIndexerPort`** — narrow surface for clip-indexer upsert/delete (scoped to ingest needs, NOT the full `clipindexer.Service`)

The composition root wires concrete adapters in `internal/app/build_bundles_clips.go`:
- `*drivenrollment.ClipIngestDriveAdapter` (forward-pointer naming — see §3 below)
- `*metadataenrollment.ClipIngestMetadataAdapter`
- `*indexerenrollment.ClipIngestIndexerAdapter`

---

## §2 — Per-PR migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

### §2.1 — `PR-CLIPS-PORT-DECLARE` (P0, deadline 2026-07-22)

Declare the 3 typed ports at `internal/application/clips/ports/clip_ingest.go` (NEW file, ~120 LoC):

```go
// godlike/06 SSOT: this file is the SOLE canonical owner of the 3
// ClipIngest* ports. Composition root wires concrete adapters; clips
// handler consumes via Deps.ClipIngestDrive / Deps.ClipIngestMetadata /
// Deps.ClipIngestIndexer.

package ports

import "context"

// ClipIngestDrivePort — narrow Drive-folder + upload surface
type ClipIngestDrivePort interface {
    EnsureFolder(ctx context.Context, parent, name string) (folderID string, err error)
    UploadFile(ctx context.Context, parent, name string, r io.Reader) (fileID, webViewLink string, err error)
}

// ClipIngestMetadataPort — narrow clip metadata enrichment surface
type ClipIngestMetadataPort interface {
    EnrichClipMetadata(ctx context.Context, assetID string) (enriched *ClipMetadata, err error)
}

// ClipIngestIndexerPort — narrow clip-indexer upsert/delete surface (scoped)
type ClipIngestIndexerPort interface {
    Upsert(ctx context.Context, assetID string, vectors map[string][]float32) (err error)
    Delete(ctx context.Context, assetID string) (err error)
}

type ClipMetadata struct {
    Title, Description, Summary string
    Tags []string
    Hook string
    Speakers []string
    MentionedPeople []string
    Topics []string
    SourceURL, SourceProvider, SourceVideoID string
}
```

Add compile-time `var _ PortsShape = ...` pin discipline (per godlike/06 SSOT one-canonical-owner-per-fact).

**Verification**: `gofmt + go vet + go build ./internal/application/clips/ports/...` exit 0. No consumer yet (ports exist for handler-side refactor).

### §2.2 — `PR-CLIPS-PORT-COMPOSITION-ADAPTERS` (P0, deadline 2026-07-29)

Wire 3 concrete adapters in `internal/app/build_bundles_clips.go` (NEW file, ~180 LoC):

```go
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// composition-root wiring for the 3 ClipIngest* ports. Adapter
// implementations live in their own packages per godlike/06 SSOT
// layering (no infra-from-application import edges).

package app

import (
    "github.com/Marcuss-ops/PipelineGen/internal/application/clips/ports"
    "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
    "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
    "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// Forward-pointer package name: `drivenrollment` (per user spec literal).
// Actual package path TBD at PR-CLIPS-PORT-COMPOSITION-ADAPTERS execution.
// Alternatives: `drivenrollment`, `driveenrollment`, `driveadapter`.

type ClipIngestDriveAdapter struct {
    admin drive.Admin
    log   *zap.Logger
}

func (a *ClipIngestDriveAdapter) EnsureFolder(ctx context.Context, parent, name string) (string, error) {
    return a.admin.EnsureFolder(ctx, parent, name)
}

func (a *ClipIngestDriveAdapter) UploadFile(ctx context.Context, parent, name string, r io.Reader) (string, string, error) {
    return a.admin.UploadFile(ctx, parent, name, r)
}

var _ ports.ClipIngestDrivePort = (*ClipIngestDriveAdapter)(nil)

// (analogous for MetadataAdapter + IndexerAdapter)
```

**Verification**: `gofmt + go vet + go build ./internal/app/...` exit 0. Adapter compile-time pins surface to godlike/06 SSOT.

### §2.3 — `PR-CLIPS-PORT-HANDLER-WIRE` (P1, deadline 2026-08-05)

Replace 3 direct infra fields in `clips/handler.go::Deps` with 3 typed-port fields:

```go
// PRE: DriveAdmin drive.Admin
// POST:
ClipIngestDrive     ports.ClipIngestDrivePort
ClipIngestMetadata  ports.ClipIngestMetadataPort
ClipIngestIndexer   ports.ClipIngestIndexerPort
```

Update `NewHandler` to wire the 3 ports; REMOVE the direct `drive.Admin` / `semantic.MetadataWriter` / `clipindexer.Service` fields.

Update all callers (`internal/app/wire_assets_clips.go`) to construct the 3 adapters and pass them via the new fields.

**Verification**: `gofmt + go vet + go build ./...` exit 0. `internal/infrastructure/drive`, `internal/infrastructure/ai/semantic`, `internal/infrastructure/indexing/clipindexer` no longer imported by `internal/api/assets/clips/` (verified via `rg internal/infrastructure/... internal/api/assets/clips/` returning 0 matches).

### §2.4 — `PR-CLIPS-PORT-TEST-COVERAGE` (P1, deadline 2026-08-12)

Add 6 hermetic TDD tests in `internal/application/clips/ports/clip_ingest_test.go`:

1. `TestClipIngestDrivePort_InterfaceContract` — 3 sub-cases (nil-receiver, happy-path, error-propagation)
2. `TestClipIngestMetadataPort_InterfaceContract` — 3 sub-cases (enrichment success, missing-asset, partial-fields)
3. `TestClipIngestIndexerPort_ScopedContract` — 3 sub-cases (upsert happy-path, delete happy-path, partial-failure-rollback)
4. `TestPortsCompiletimePin` — asserts all 3 ports have at least one concrete impl satisfying the compile-time `var _` pin
5. `TestClipIngestDriveAdapter_NilLogger` — adapter nil-tolerance
6. `TestClipIngestDriveAdapter_DelegationContract` — adapter must DELEGATE (not re-implement) the underlying drive.Admin method

**Verification**: `go test -short -count=1 ./internal/application/clips/ports/` PASS 6/6.

### §2.5 — `PR-CLIPS-PORT-ARCHCHECK-GATE` (P2, deadline 2026-08-15)

Add forward-prevention gate in `cmd/archcheck/scan/percheck_clips_infra_import.go` (NEW, ~80 LoC):

- HARD-FAIL: `internal/infrastructure/drive`, `internal/infrastructure/ai/semantic`, `internal/infrastructure/indexing/clipindexer` imports inside `internal/api/assets/clips/` (production code only)
- ALLOW: `*_test.go` files (test fixtures may import infra directly for integration tests)
- ALLOW: the canonical composition-root wiring file `internal/app/build_bundles_clips.go` (canonical owner)
- WARN: comment-only references (godlike/07 residue accounting)

Register the check in `cmd/archcheck/runner.go::DefaultChecks` as the 5th per-check scanner (after Check 5 typeredecl + Check 53 txcontext + Check 54 monitor-infra-import + Check N player-client).

**Verification**: `gofmt + go vet + go build ./cmd/archcheck/...` exit 0. `go run ./cmd/archcheck --strict` on the real repo reports ZERO violations (the 3 imports have been replaced by typed ports in PR-CLIPS-PORT-HANDLER-WIRE).

---

## §3 — Forward-pointer naming clarification

The user spec references `*drivenrollment.ClipIngestDriveAdapter` — this is the **forward-pointer package name** for the composition-root adapter (Section §2.2). The actual package path is TBD at PR-CLIPS-PORT-COMPOSITION-ADAPTERS execution time. Alternative names being considered:

- `drivenrollment` (literal user spec)
- `drivenrollment` (split: `drive` + `nrollment` — common Go convention for adapter packages)
- `driveadapter` (descriptive, mirrors the `drivenrollment` + `metadataenrollment` + `indexerenrollment` triplet)

The decision is DEFERRED to PR-CLIPS-PORT-COMPOSITION-ADAPTERS implementation time per godlike/07 minimum-blast-radius. The action plan preserves the literal name `drivenrollment` for traceability.

---

## §4 — Honest scope-lock (godlike/07)

**This action plan is docs-only** — the current commit lands the wave-tracker slot DEFERRED + this action plan + the 3-surface lockstep. The implementation lands incrementally per §2.1..§2.5 between 2026-07-22 and 2026-08-15.

**Pre-existing build issues** (per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`): 6-item voiceover + app carry-forward UNCHANGED — NOT regressions of this plan.

**Pre-existing YAML parse carry-forward** (per `PRE-EXISTING-YAML-PARSE-2026-07-04` + `PR-CURRENT-YAML-PARSE-FIX-PART-6`): both `wave_p0_critical.yaml` + `wave_p3_low_and_audit.yaml` fail `yaml.safe_load` on this host. The wave-tracker slot is DEFERRED per the established codebase convention — the action plan + CHANGELOG + AGENTS surfaces are the canonical SOLE record of the forward-pointer. When PART-6 lands, the slot can be appended verbatim from the template in §2.6.

---

## §5 — Cross-references (godlike/06 SSOT umbrella)

- **AGENTS.md Pattern 0** (the canonical rule): typed-port abstraction layer for new externalizable dependencies.
- **AGENTS.md Pattern 5** (mechanical split precedent): PR-CHROME-PROVIDER-SPLIT, PR-WIRE-ASSETS-CAPABILITY-SPLIT, PR-CLIPS-HANDLER-SPLIT-1.
- **`architecture/waves/wave_p3_low_and_audit.yaml#PR-CLIPS-PORT-EXTRACT`** (DEFERRED): wave-tracker slot.
- **`architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`**: 6-item carry-forward (NOT regressions).
- **`architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`** + **`#PR-CURRENT-YAML-PARSE-FIX-PART-6`**: parse-error carry-forward (forward-pointer unblocks wave-tracker slot).
- **`internal/api/assets/clips/handler.go`**: 3 direct imports at lines 54, 57, 58 (verified at plan-write time).
- **`internal/application/clips/ports/`**: target directory (NEW) for the 3 typed ports.

---

## §6 — Lifecycle audit-trail

- **2026-07-08**: Action plan landed (this commit). Wave-tracker slot DEFERRED.
- **2026-07-15**: target review checkpoint — confirm port declarations align with PR-CLIPS-PORT-DECLARE.
- **2026-07-22**: `PR-CLIPS-PORT-DECLARE` ships.
- **2026-07-29**: `PR-CLIPS-PORT-COMPOSITION-ADAPTERS` ships.
- **2026-08-05**: `PR-CLIPS-PORT-HANDLER-WIRE` ships (clips/handler.go direct imports REPLACED).
- **2026-08-12**: `PR-CLIPS-PORT-TEST-COVERAGE` ships (6 hermetic TDD tests).
- **2026-08-15**: `PR-CLIPS-PORT-ARCHCHECK-GATE` ships (forward-prevention gate live).
- **2026-08-15**: parent wave-flip to `status: shipped + exit_signal: true` triggers ONLY when all 5 sub-PRs reach `status: shipped` AND the archcheck gate reports ZERO violations.

---

**Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local>
**3-surface lockstep**: this action plan ≈ `CHANGELOG.md ## Unreleased > ### Documentation` mirror entry ≈ `AGENTS.md ## Recent cross-cutting closures` mirror entry. Wave-tracker slot DEFERRED per pre-existing YAML parse carry-forward.
