# Canonical Surfaces Unification — Action Plan

**Date:** 2026-07-08
**Author:** Marcuss-ops + PipelineGen Agent
**Status:** in_progress
**Deadline:** 2026-09-01

---

## §0 — Problem Statement

Today each pipeline (YouTube, Stock, Artlist, Voiceover) independently
derives Drive paths, clip identities, metadata shapes, and outbox
envelopes. This creates **4 classes of duplication** that will cause
divergent behaviour as new pipelines land:

| # | Surface | YouTube today | Stock today | Risk |
|---|---------|---------------|-------------|------|
| 1 | Drive path/subdir | `DriveFolderID` + `deriveNormalizedGroup("")` + `BuildClipFilename` | `stockRootFolderName` + `perClipLeafName` + `RootFolderName`/`PathLeafName` | Two distinct folder-tree shapes for the same logical concept |
| 2 | Clip identity | `yt_{videoID}_{start}_{end}_{policy}` | `planner:{hash}:{index}` | `asset_id` and `source_version` derived via different functions |
| 3 | Metadata | `CanonicalClipMetadata` (23 fields) + `ClipAsset` | `ChunkState` + `ChunkMetadataEntry` + `StockRunMetadata` | Qdrant payload differs per pipeline; search_text shape diverges |
| 4 | SQLite+outbox+Qdrant writer | `CommitClipAndIndexEvent` (5-step atomic TX) | `FinalizeAsset` (finalizer TX) | Two independent envelopes for `asset.index.requested` |

---

## §1 — Goal

Create **4 canonical surface points** in `internal/domain/` or
`internal/application/` that ALL pipelines route through:

1. **`DriveDestinationResolver`** — single entry point for Drive path resolution
2. **`ClipIdentityBuilder`** — single entry point for `asset_id` / `source_version` / `idempotency_key`
3. **`ClipSemanticMetadata`** — single common metadata type for SQLite, Qdrant, Drive
4. **`AssetPersistenceWriter`** — single writer for `media_assets` + `asset_locations` + `outbox_events`

---

## §2 — Punti Canonici Design

### 2.1 DriveDestinationResolver

**Location:** `internal/domain/delivery/drive_destination.go` (SSOT)

```go
// DriveDestinationInput is the semantic input for Drive path resolution.
// Every pipeline (YouTube, Stock, Artlist, Voiceover) passes this struct
// to the canonical resolver instead of building paths ad-hoc.
type DriveDestinationInput struct {
    RootFolderID   string // canonical Drive root (from config)
    RootFolderName string // human-readable root name (stock: "Pacquiao Vs Broner")
    LeafName       string // immediate leaf folder (stock: perClipLeafName; youtube: slugified description)
    Group          string // semantic group (youtube: derived from Destination.Group → Category → "youtube_uncategorized"; stock: stockRootFolderName)
    Subject        string // semantic subject (youtube: clip title; stock: clip description)
    Provider       string // source provider ("youtube", "pexels", "artlist")
    Category       string // semantic category ("Boxe", "Boxing")
    Project        string // project identifier (voiceover: from API request)
    Language       string // language code ("it", "en")
    Domain         string // pipeline domain ("youtube", "stock", "voiceover", "artlist")
}

// DriveDestination is the resolved canonical Drive location.
type DriveDestination struct {
    RootFolderID   string
    ParentFolderID string // resolved parent folder (may be same as RootFolderID)
    LeafFolderName string // sanitized leaf folder name
    FullPath       string // human-readable path for metadata
    Group          string // resolved group for search_text
}

// ResolveDriveDestination resolves a semantic input into a canonical
// Drive location. This is the SOLE owner of the path-building logic
// for all pipelines.
func ResolveDriveDestination(ctx context.Context, input DriveDestinationInput) (DriveDestination, error)
```

**Migration:** YouTube `deriveNormalizedGroup` + `buildClipAsset` + `DriveFolderID/DriveFolderPath` → route through resolver. Stock `stockRootFolderName` + `perClipLeafName` → route through resolver. Voiceover `VoiceoverPath` → route through resolver.

**Key invariant:** `VerifiedArtifact.PathLeafName` and `VerifiedArtifact.RootFolderName` remain the infrastructure-layer fields, but they are now populated FROM `DriveDestination.LeafFolderName` and `DriveDestination.RootFolderName`, not computed independently per pipeline.

### 2.2 ClipIdentityBuilder

**Location:** `internal/domain/asset/clip_identity.go` (SSOT)

```go
// ClipIdentityInput is the canonical input for generating a clip's
// triple identity (asset_id, source_version, idempotency_key).
type ClipIdentityInput struct {
    SourceProvider string // "youtube", "pexels", "stock", "artlist"
    SourceVideoID  string // YouTube video ID, stock source hash, etc.
    SourceURL      string // original URL
    StartSec       float64
    EndSec         float64
    PolicyVersion  string // e.g. "stock_timestamp_v1", "youtube_v1"
    ContentHash    string // SHA256 of the file content
    Domain         string // "youtube", "stock", "voiceover", "artlist"
}

// ClipIdentity is the resolved canonical identity triple.
type ClipIdentity struct {
    AssetID        string // canonical asset ID (e.g. "yt_{videoID}_{start}_{end}_{policy}")
    SourceVersion  string // content-hash-derived version for supersede gate
    IdempotencyKey string // deterministic key for outbox dedup
}

// BuildClipIdentity derives the canonical identity triple from a
// ClipIdentityInput. This is the SOLE owner of identity derivation
// for all pipelines.
func BuildClipIdentity(input ClipIdentityInput) ClipIdentity
```

**Migration:** YouTube `yt_{videoID}_{start}_{end}_{policy}` format → canonical output of `BuildClipIdentity` when `Domain="youtube"`. Stock `planner:{hash}:{index}` → canonical output when `Domain="stock"`. Both share the same `source_version` derivation (content hash → SHA256 hex).

### 2.3 ClipSemanticMetadata

**Location:** `internal/domain/asset/clip_semantic_metadata.go` (SSOT)

```go
// ClipSemanticMetadata is the canonical metadata envelope shared by
// all pipelines. Each pipeline maps its domain-specific DTOs into
// this type before writing to SQLite, Qdrant, or Drive metadata.
type ClipSemanticMetadata struct {
    // ── Source identity ──
    SourceProvider string   `json:"source_provider,omitempty"`
    SourceURL      string   `json:"source_url,omitempty"`
    SourceVideoID  string   `json:"source_video_id,omitempty"`
    Origin         string   `json:"origin,omitempty"`        // "youtube", "pexels", "artlist"
    Destination    string   `json:"destination,omitempty"`    // "youtube_clip", "stock_chunk"

    // ── Content ──
    Title       string   `json:"title,omitempty"`
    Description string   `json:"description,omitempty"`
    Summary     string   `json:"summary,omitempty"`
    Hook        string   `json:"hook,omitempty"`

    // ── Timing ──
    StartSec    float64  `json:"start_sec,omitempty"`
    EndSec      float64  `json:"end_sec,omitempty"`
    DurationSec float64  `json:"duration_sec,omitempty"`

    // ── Semantic ──
    Group           string   `json:"group,omitempty"`
    Subject         string   `json:"subject,omitempty"`
    Topics          []string `json:"topics,omitempty"`
    Speakers        []string `json:"speakers,omitempty"`
    MentionedPeople []string `json:"mentioned_people,omitempty"`
    Tags            []string `json:"tags,omitempty"`
    Category        string   `json:"category,omitempty"`
    Entities        []string `json:"entities,omitempty"`
    Event           string   `json:"event,omitempty"`
    Round           int      `json:"round,omitempty"`
    Scene           string   `json:"scene,omitempty"`

    // ── Drive location ──
    DriveFileID     string `json:"drive_file_id,omitempty"`
    DrivePath       string `json:"drive_path,omitempty"`
    FolderID        string `json:"folder_id,omitempty"`
    FolderPath      string `json:"folder_path,omitempty"`

    // ── Provenance ──
    PolicyVersion   string `json:"policy_version,omitempty"`
    ContentHash     string `json:"content_hash,omitempty"`
    IndexingStatus  string `json:"indexing_status,omitempty"`

    // ── LLM enrichment (plumbing-on-nil) ──
    SemanticTitle   string `json:"semantic_title,omitempty"`
    EmbeddingText   string `json:"embedding_text,omitempty"`
    Language        string `json:"language,omitempty"`

    // ── Workflow ──
    JobID           string `json:"job_id,omitempty"`
    WorkflowID      string `json:"workflow_id,omitempty"`
    RunFingerprint  string `json:"run_fingerprint,omitempty"`
    ChunkIndex      int    `json:"chunk_index,omitempty"`
    TotalChunks     int    `json:"total_chunks,omitempty"`
}

// FromCanonicalClipMetadata converts YouTube's CanonicalClipMetadata
// into ClipSemanticMetadata. This is the adapter seam.
func FromCanonicalClipMetadata(m youtubetypes.CanonicalClipMetadata, drive DriveDestination) ClipSemanticMetadata

// FromStockRunMetadata converts Stock's ChunkMetadataEntry +
// StockRunMetadata into ClipSemanticMetadata.
func FromStockRunMetadata(entry ChunkMetadataEntry, run StockRunMetadata) ClipSemanticMetadata
```

**Migration:** `CanonicalClipMetadata` (YouTube) → adapter converts to `ClipSemanticMetadata`. `ChunkState` + `ChunkMetadataEntry` + `StockRunMetadata` (Stock) → adapter converts to `ClipSemanticMetadata`. Qdrant `AssetData` reads from `ClipSemanticMetadata` instead of per-pipeline DTOs.

### 2.4 AssetPersistenceWriter

**Location:** `internal/application/assets/persistence/writer.go` (SSOT)

```go
// AssetPersistenceRequest is the canonical input for writing a video
// clip asset to SQLite + outbox + Qdrant in a single atomic operation.
type AssetPersistenceRequest struct {
    Identity   ClipIdentity
    Location   DriveDestination
    Metadata   ClipSemanticMetadata
    LocalPath  string // local file path on disk
    FileHash   string // SHA256 of the file content
    MediaType  string // "video", "audio"
    Source     string // "youtube", "stock", "artlist", "voiceover"
}

// AssetPersistenceWriter is the canonical writer for video/audio clip
// assets. It replaces YouTube's CommitClipAndIndexEvent and Stock's
// FinalizeAsset with a single unified surface.
type AssetPersistenceWriter interface {
    // PersistAndIndex writes media_assets + asset_locations +
    // outbox_events (asset.index.requested) in a single transaction.
    PersistAndIndex(ctx context.Context, req AssetPersistenceRequest) error
}
```

**Migration:** YouTube `CommitClipAndIndexEvent` → wraps `PersistAndIndex`. Stock `FinalizeAsset` (finalizer TX) → wraps `PersistAndIndex`. Both produce the same `media_assets` shape + `asset_locations` shape + `outbox_events` envelope.

---

## §3 — PR Migration Sequence

| PR | Surface | Deadline | Files changed |
|----|---------|----------|---------------|
| `PR-CANONICAL-DRIVE-DESTINATION` | §2.1 DriveDestinationResolver | 2026-08-08 | `internal/domain/delivery/drive_destination.go` (NEW) + adapter in `internal/application/assets/delivery/mapper.go` |
| `PR-CANONICAL-CLIP-IDENTITY` | §2.2 ClipIdentityBuilder | 2026-08-08 | `internal/domain/asset/clip_identity.go` (NEW) + adapter in youtube usecase + stock planner |
| `PR-CANONICAL-CLIP-METADATA` | §2.3 ClipSemanticMetadata | 2026-08-15 | `internal/domain/asset/clip_semantic_metadata.go` (NEW) + adapters in youtube dto + stock orchestrator_metadata |
| `PR-CANONICAL-ASSET-WRITER` | §2.4 AssetPersistenceWriter | 2026-08-15 | `internal/application/assets/persistence/writer.go` (NEW) + adapter wrapping CommitClipAndIndexEvent + FinalizeAsset |
| `PR-CANONICAL-SEARCHTEXT-PORT` | Unified search_text composition | 2026-08-08 | `internal/domain/asset/search_text.go` (NEW) + youtube `composeYouTubeClipSearchText` → delegate to port + stock `composeStockChunkSearchText` → delegate to port |
| `PR-CANONICAL-E2E-MULTICLIP` | End-to-end multi-clip test | 2026-08-15 | `tests/e2e/canonical_surfaces_e2e_test.go` (NEW) — 1 YouTube clip + 1 Stock chunk through the same canonical surface |

---

## §4 — Execution Order

Per AGENTS.md Git-Lesson-2: each PR lands **directly on main** (no branches, no `--force`).

```
PR-CANONICAL-DRIVE-DESTINATION  ──┐
PR-CANONICAL-CLIP-IDENTITY      ──┼── parallel (no dependencies between them)
PR-CANONICAL-SEARCHTEXT-PORT    ──┘
          │
          ▼
PR-CANONICAL-CLIP-METADATA      (depends on ClipIdentity for asset_id)
          │
          ▼
PR-CANONICAL-ASSET-WRITER       (depends on all above)
          │
          ▼
PR-CANONICAL-E2E-MULTICLIP      (final verification gate)
```

---

## §5 — Verification Gates

Each PR must pass:

1. `gofmt -l` clean
2. `go vet ./internal/...` exit 0
3. `go build ./...` exit 0
4. `go test -short ./internal/domain/asset/...` PASS
5. `go test -short ./internal/application/assets/...` PASS
6. No new `RootFolderOverride` in `internal/application/**` or `internal/api/**` (archcheck B1 gate)

The final PR (`PR-CANONICAL-E2E-MULTICLIP`) additionally asserts:
- YouTube clip → Drive subdir correct → `media_assets` + `outbox` + Qdrant all populated
- Stock chunk → Drive subdir correct → `media_assets` + `outbox` + Qdrant all populated
- Both produce identical `asset.index.requested` envelope shape

---

## §6 — Honest Scope-Lock (godlike/07)

- **NOT a big-bang refactor.** Each PR adds the canonical surface + one adapter. Existing per-pipeline code continues working via the adapter. CUTOVER (removing the per-pipeline direct calls) is a separate wave.
- **NOT breaking existing callers.** `VerifiedArtifact.PathLeafName` / `RootFolderName` / `RootFolderOverride` stay as-is in the infrastructure layer. The canonical surface sits ABOVE them.
- **NOT touching voiceover/Artlist yet.** The first wave focuses on YouTube + Stock (the two pipelines with the most divergence). Voiceover and Artlist are Wave 2.

---

## §7 — Cross-References

- `architecture/waves/wave_p1_high.yaml#CANONICAL-SURFACES-UNIFICATION-2026-07-08` (wave-tracker)
- `architecture/action-plans/2026-07-04-clips-metadata-consolidation.md` (predecessor: 7-type collapse already partially shipped)
- `internal/application/assets/delivery/mapper.go` (canonical BuildPublishRequest — the resolver will delegate to this)
- `internal/application/youtube/usecase/process_segment_helpers.go` (composeYouTubeClipSearchText — to be ported to port)
- `internal/application/assets/providers/stock/stockpipeline/orchestrator_metadata.go` (buildStockRunMetadata — to be ported to adapter)
- `internal/infrastructure/database/sqlite/assets/clip_atomic_writer.go` (CommitClipAndIndexEvent — to be wrapped by AssetPersistenceWriter)
- `internal/application/assets/finalizer/asset_finalizer_tx.go` (FinalizeAsset — to be wrapped by AssetPersistenceWriter)

---

## §8 — Co-authored-by

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
Co-authored-by: Marcuss-ops
