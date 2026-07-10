# interface{} → any Conversion — Action Plan (2026-07-09)

**Source:** `architecture/action-plans/2026-08-08-refactor-checklist-action-plan.md` §3.C (PR-LSDC-INTERFACE-TYPED-CONVERSION) + LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09 Phase A P0 spec (PR-CLEANUP-TYPED-INTERFACES).

**Status:** executing on `main` directly per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`, no `--force`).

**Owner capability:** cross-cutting (`internal/infrastructure/qdrant/`, `internal/application/scripts/`, `internal/api/`, `internal/domain/asset/`, `internal/app/`).

---

## §0 Honest Status Snapshot (godlike/07 NO-FAKE-AVAILABILITY)

**PR-0 (MECHANICAL) already shipped** at commit `f876c13aa` (2026-07-09):
- 140 files changed, 1598 insertions / 444 deletions
- `map[string]interface{}` → `map[string]any` (89 sites)
- `[]interface{}` → `[]any` (6 sites)
- func params/returns `interface{}` → `any` (~87 sites across qdrant/, domain/, app/, scripts/, infrastructure/)
- `gofmt -w` cleanup on 20 production + 11 test files
- EXEMPT sites preserved (duck-typing in `routes.go`/`server.go`, type assertion in `wire_assets.go`)
- Verification: `gofmt` clean, `go vet` exit 0, `go build` exit 0, **0 `interface{}` remaining in production code**

**Remaining TYPED sites (~14):** The mechanical conversion swapped `interface{}` → `any` (identical types in Go 1.18+), but the underlying pattern of duck-typing via `any` struct fields/params persists. These ~14 sites should become proper Pattern 0 typed interfaces for compile-time safety.

**Honest limitation (godlike/07):** this action plan's static priority is by blast-radius × type-safety gain, NOT by git-log frequency. Post-wave cross-validation via `PR-INTERFACE-ANY-HOTSPOT-CROSSREF` (deadline 2026-09-15) will validate against actual frequency.

---

## §1 Scope Inventory

### 1.1 Already Shipped (PR-0, commit `f876c13aa`)

| Pattern | Count | Conversion |
|---------|-------|------------|
| `map[string]interface{}` → `map[string]any` | 89 | Mechanical Go 1.18+ syntax |
| `[]interface{}` → `[]any` | 6 | Mechanical Go 1.18+ syntax |
| func params/returns `interface{}` → `any` | ~87 | Mechanical Go 1.18+ syntax |
| `gofmt -w` cleanup | 31 files | Whitespace/line-length normalization |
| EXEMPT (preserved) | ~5 | Duck-typing in routes.go/server.go, wire_assets.go |

### 1.2 Remaining TYPED Sites (PR-1..PR-5)

| PR | File(s) | Sites | Blast Radius | Deadline |
|----|---------|-------|--------------|----------|
| PR-1 | `routes.go`, `server.go` | 4 (`healthSvc`, `qdrantHealth`, `Health`, `QdrantHealth`) | Low — local structural interfaces | 2026-08-15 |
| PR-2 | `curation_types.go` | 6 (`clipsRepo`, `clipBuilder`, `generateOneUC`, `clipSearch`, `NewMediaCurator`, `SetClipSearchPort`) | Low — deprecated surface | 2026-08-22 |
| PR-3 | `services.go` | 5 (`BuildCandidates`, `ImageSearchService`, `JobEnqueueService`, `HarvestService`, `AssociationService`) | Medium — cross-package interface | 2026-08-22 |
| PR-4 | `clip_source_builder.go` | 2 (`ollamaClient`, `reranker`) | Medium — avoids import cycle | 2026-09-01 |
| PR-5 | `ports.go` | 1 (`ClipFolderMemoryPort`) | Low — type alias removal | 2026-09-01 |

**Total TYPED sites:** ~18 across 5 files.

---

## §2 Per-PR Execution Checklist

### PR-1: HealthService + QdrantHealth typed ports (P0, deadline 2026-08-15)

**Goal:** Replace `any` struct fields with local structural interfaces in `internal/api/routes.go` and `internal/api/server.go`.

**Files:** `internal/api/routes.go` (4 sites: L66 `healthSvc`, L68 `qdrantHealth`, L167 `SetHealthService`, L181 `SetQdrantHealthHandler`) + `internal/api/server.go` (2 sites: L91 `Health`, L95 `QdrantHealth`).

**Typed interfaces (local to `internal/api/`):**
```go
// HealthServicePort is the structural interface for the health service.
// Only the methods consumed by the Router are listed (godlike/07 minimum-blast-radius).
type HealthServicePort interface {
    RegisterRoutes(r *gin.RouterGroup)
}

// QdrantHealthPort is the structural interface for the Qdrant health handler.
type QdrantHealthPort interface {
    Live(c *gin.Context)
    Ready(c *gin.Context)
}
```

**Execution:**
1. Declare `HealthServicePort` + `QdrantHealthPort` in a new `internal/api/ports.go` (~20 LoC).
2. Replace `healthSvc any` → `healthSvc HealthServicePort` in `routes.go` Router struct.
3. Replace `qdrantHealth any` → `qdrantHealth QdrantHealthPort` in `routes.go` Router struct.
4. Replace `Health any` → `Health HealthServicePort` in `server.go` Server struct.
5. Replace `QdrantHealth any` → `QdrantHealth QdrantHealthPort` in `server.go` Server struct.
6. Update `SetHealthService(svc any)` → `SetHealthService(svc HealthServicePort)`.
7. Update `SetQdrantHealthHandler(h any)` → `SetQdrantHealthHandler(h QdrantHealthPort)`.
8. Remove the type assertions in `routes.go` usage sites (now unnecessary — the field is already typed).
9. Add `var _ HealthServicePort = (*systemhealth.Service)(nil)` compile-time pin in `internal/app/` composition root.
10. Add `var _ QdrantHealthPort = (*transport.QdrantHealthHandler)(nil)` compile-time pin.

**Verification gates:**
```bash
gofmt -l internal/api/ports.go internal/api/routes.go internal/api/server.go
go vet ./internal/api/...
go build ./internal/api/... ./internal/app/...
go test -short -count=1 ./internal/api/...
```

---

### PR-2: MediaCurator typed ports (P1, deadline 2026-08-22)

**Goal:** Replace `any` fields in `MediaCurator` with typed interfaces. NOTE: `MediaCurator` is deprecated — low blast radius.

**File:** `internal/application/scripts/dto/curation_types.go` (6 sites: L58 `clipsRepo`, L59 `clipBuilder`, L60 `generateOneUC`, L61 `clipSearch`, L78 `NewMediaCurator`, L93 `SetClipSearchPort`).

**Typed interfaces (local to `dto/`):**
```go
type CurationClipsRepo interface {
    // methods consumed by MediaCurator
}

type CurationClipBuilder interface {
    // methods consumed by MediaCurator
}

type CurationGenerateOneUC interface {
    // methods consumed by MediaCurator
}

type CurationClipSearch interface {
    // methods consumed by MediaCurator
}
```

**Execution:**
1. Identify the actual methods called on each `any` field via `grep` in the file.
2. Declare the 4 local structural interfaces in `curation_types.go` (co-located with sole consumer per godlike/06 SSOT).
3. Replace `any` struct fields with the typed interfaces.
4. Update `NewMediaCurator` and `SetClipSearchPort` signatures.
5. Add `var _ CurationClipsRepo = (*SomeConcrete)(nil)` compile-time pins where the concrete is known.

**Verification gates:**
```bash
gofmt -l internal/application/scripts/dto/curation_types.go
go vet ./internal/application/scripts/dto/...
go build ./internal/application/scripts/...
go test -short -count=1 ./internal/application/scripts/dto/...
```

---

### PR-3: services.go typed request/response (P1, deadline 2026-08-22)

**Goal:** Replace `any` in `BuildCandidates`, `ImageSearchService`, `JobEnqueueService`, `HarvestService`, `AssociationService` with typed request/response structs.

**File:** `internal/application/scripts/usecase/services.go` (5 sites: L70, L80, L97, L102, plus `AssociationService`).

**Execution:**
1. For `BuildCandidates(ctx, req any) (any, error)` — identify the actual request/response types from callers and replace with typed structs.
2. For `ImageSearchService.Search(ctx, q any) ([]any, error)` — identify the query type and result type.
3. For `JobEnqueueService.Enqueue(ctx, req any) (any, error)` — likely `EnqueueRequest`/`EnqueueResponse` from the jobs domain.
4. For `HarvestService.EnqueueHarvest(ctx, req any) (any, error)` — similar.
5. For `AssociationService` — check if `AssocSearchService` already exists as a typed replacement.

**Verification gates:**
```bash
gofmt -l internal/application/scripts/usecase/services.go
go vet ./internal/application/scripts/usecase/...
go build ./internal/application/scripts/...
go test -short -count=1 ./internal/application/scripts/usecase/...
```

---

### PR-4: OllamaClient + Reranker typed ports (P2, deadline 2026-09-01)

**Goal:** Replace `any` struct fields `ollamaClient` and `reranker` in `ClipSourceBuilder` with local typed ports.

**File:** `internal/application/scripts/usecase/clip_source_builder.go` (2 sites: L44 `ollamaClient`, L45 `reranker`).

**Typed interfaces (local to `usecase/`):**
```go
type ClipOllamaClientPort interface {
    // methods consumed by ClipSourceBuilder
}

type ClipRerankerPort interface {
    // methods consumed by ClipSourceBuilder
}
```

**Execution:**
1. Identify methods called on `ollamaClient` and `reranker` via grep.
2. Declare local structural interfaces co-located with sole consumer.
3. Replace `any` fields + setter params with typed interfaces.
4. Add compile-time pins at the composition root wiring site.

**Verification gates:**
```bash
gofmt -l internal/application/scripts/usecase/clip_source_builder.go
go vet ./internal/application/scripts/usecase/...
go build ./internal/application/scripts/...
go test -short -count=1 ./internal/application/scripts/usecase/...
```

---

### PR-5: ClipFolderMemoryPort typed interface (P2, deadline 2026-09-01)

**Goal:** Replace `type ClipFolderMemoryPort = any` with a proper typed interface.

**File:** `internal/application/clips/ports.go` (1 site: L225).

**Execution:**
1. Identify methods called via the `ClipFolderMemoryPort` type assertion.
2. Declare the typed interface with those methods.
3. Verify `var _ ClipFolderMemoryPort = (*clipsFolderMemoryAdapter)(nil)` compile-time pin still compiles.
4. Remove the `= any` type alias.

**Verification gates:**
```bash
gofmt -l internal/application/clips/ports.go
go vet ./internal/application/clips/...
go build ./internal/application/clips/...
go test -short -count=1 ./internal/application/clips/...
```

---

## §3 Priority Matrix

| Band | PR | Deadline | Blast Radius | Justification |
|------|----|----------|--------------|---------------|
| P0 | PR-1 HealthService + QdrantHealth | 2026-08-15 | Low | Hot-path API routing; compile-time safety for health endpoints |
| P1 | PR-2 MediaCurator | 2026-08-22 | Low | Deprecated surface; low risk, easy win |
| P1 | PR-3 services.go typed request/response | 2026-08-22 | Medium | Cross-package interface; higher complexity |
| P2 | PR-4 OllamaClient + Reranker | 2026-09-01 | Medium | Avoids import cycle; requires careful port design |
| P2 | PR-5 ClipFolderMemoryPort | 2026-09-01 | Low | Type alias removal; minimal blast |

---

## §4 Wave-Tracker Entry (DEFERRED per YAML parse carry-forward)

**DEFERRED** per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer (deadline 2026-08-15). The canonical closure record is this action plan + CHANGELOG.md + AGENTS.md per the established precedent (mirrors PR-POSTPROCESSOR-UNIFICATION-PHASE-4, SCRIPT-DOWNSTREAM-CUTOVER, LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09).

**Canonical wave-tracker slot template (to append when YAML parse is resolved):**

```yaml
- id: INTERFACE-ANY-CONVERSION-2026-07-09
  status: in_progress
  owner_capability: cross-cutting
  deadline: 2026-09-15
  exit_gate: |
    All 5 linked_issues shipped + go vet exit 0 + go build exit 0 +
    0 'any' struct fields remaining that could be typed interfaces +
    PR-INTERFACE-ANY-HOTSPOT-CROSSREF validates against git-log frequency
  linked_issues:
    - id: PR-INTERFACE-ANY-MECHANICAL
      status: shipped
      ship_sha: f876c13aa
      ship_date: 2026-07-09
      owner_capability: cross-cutting
    - id: PR-INTERFACE-ANY-HEALTH-PORTS
      status: pending
      deadline: 2026-08-15
      owner_capability: internal/api
    - id: PR-INTERFACE-ANY-CURATION-PORTS
      status: pending
      deadline: 2026-08-22
      owner_capability: internal/application/scripts/dto
    - id: PR-INTERFACE-ANY-SERVICES-TYPED
      status: pending
      deadline: 2026-08-22
      owner_capability: internal/application/scripts/usecase
    - id: PR-INTERFACE-ANY-CLIP-SOURCE-PORTS
      status: pending
      deadline: 2026-09-01
      owner_capability: internal/application/scripts/usecase
    - id: PR-INTERFACE-ANY-FOLDER-MEMORY-PORT
      status: pending
      deadline: 2026-09-01
      owner_capability: internal/application/clips
    - id: PR-INTERFACE-ANY-HOTSPOT-CROSSREF
      status: pending
      deadline: 2026-09-15
      owner_capability: cross-cutting
```

---

## §5 Honest Scope-Lock (godlike/07)

1. **PR-0 shipped 140 files** — the mechanical `interface{}` → `any` conversion is complete. No backfill needed.
2. **PR-1..PR-5 are forward-pointers** — each lands incrementally on `main` per AGENTS.md Git-Lesson-2.
3. **No import cycles** — each PR declares local structural interfaces co-located with the sole consumer (godlike/06 SSOT).
4. **No composition-root changes** beyond compile-time pins — the typed interfaces replace `any` fields/params, not the wiring itself.
5. **Pre-existing 5-item voiceover + app build-issue carry-forward** per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` UNCHANGED — NOT regressions of any PR in this wave.

---

## §6 Cross-References

- `architecture/action-plans/2026-08-08-refactor-checklist-action-plan.md` §3.C (parent spec)
- `architecture/action-plans/2026-07-09-logic-simplification-dead-code-action-plan.md` §3 Phase A P0 (parent spec)
- `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` (wave-tracker DEFERRED)
- `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (carry-forward)
- AGENTS.md §Pattern 0 (typed port discipline)
- AGENTS.md §godlike/06 SSOT (one-canonical-owner-per-fact)
- AGENTS.md §godlike/07 minimum-blast-radius
- AGENTS.md Git-Lesson-2 (direct-to-main workflow)
- AGENTS.md Git-Lesson-3 (Co-authored-by trailer)
