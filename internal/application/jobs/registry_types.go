// Package jobs — registry_types.go (PR-SPLIT-JOBS-REGISTRY-DEFINITIONS, July 2026).
//
// godlike/06 SSOT (one-canonical-owner-per-fact): this file is the
// canonical RE-EXPORT surface for the registry's per-job-type data shapes.
// The canonical string literals live in their owning capability domain
// packages (per godlike/02 §Capability-specific constants stay in their
// owning domain package). Every Type* constant here is an alias (= pkg.TypeXxx)
// — ZERO new string literals.
//
//   - RegistryEntry + JobPolicy (type alias) — the per-job-type
//     policy record surface (Wave 19 / P1-9, June 2026).
//   - The full Type... identifier block — RE-EXPORTS every canonical
//     string jobType from its owning capability domain package.
//
// Lookup paths preserved: jobs.RegistryEntry{...}, jobs.JobPolicy{...},
// and every `jobs.Type*` constant resolve identically pre/post split
// (same package).
//
// 3-file split layout (per d44e0239 pkg/retry canonical pattern):
//
//	registry_definitions.go  (slim: package doc + canonical policy constants)
//	registry_timeout.go     (HC-1 typed port surface)
//	registry_types.go       (this file: RegistryEntry + JobPolicy + Type... const block)
package jobs

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/books"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/document"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/image"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/subtitle"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/system"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/video"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/youtube"
)

// ── RegistryEntry (Wave 19 / P1-9 canonical policy record) ─────────────

// RegistryEntry defines a registered job type with its operational parameters.
//
// Wave 19 / P1-9 (June 2026): the canonical policy record. The
// Queue + Concurrency fields extend the pre-Wave-19 surface so that
// every registered job type carries operator-facing controls in ONE
// shape (Timeout + DefaultMaxRetries + Queue + Concurrency + caps).
// The pre-Wave-19 name RegistryEntry is preserved because callers
// across internal/app, internal/application/scheduler, and the
// dispatcher wiring reference it directly; the JobPolicy alias
// (declared below) is the user-facing semantic name.
//
// Job type policy contract (Wave 19 / P1-9):
//
//	JobPolicy{
//	    Type:                canonicalJobType,                  // e.g. "script.generate"
//	    Description:         human-readable string,             // operator-facing
//	    Timeout:             per-job execution cap,             // 0 = canonical 10m default
//	    DefaultMaxRetries:   retry count when caller omits,     // canonical = 3
//	    Queue:               queue label (string),              // empty -> DefaultQueue
//	    Concurrency:         per-worker parallel-leases,        // <=0 -> DefaultConcurrency
//	    RequiredCapabilities: capability tags worker must have,  // empty = any worker
//	}
//
// Field-default canon (Wave 19): every registered job type surfaces a
// non-zero Queue (DefaultQueue) and Concurrency (>=DefaultConcurrency)
// through the typed accessors after applyDefaults() applies them. The
// raw entry.Queue / entry.Concurrency MAY read "" / 0 before the
// pass; consumers MUST read through the typed `Registry.Queue(t)` and
// `Registry.Concurrency(t)` accessors rather than the raw field so
// the canonical values are observed regardless of whether the literal
// was set.
//
// Migration to Wave 19 fields is field-additive (no struct rename).
// Because JobPolicy is a Go type alias (not a new named type), all
// pre-Wave-19 `RegistryEntry{...}` literals and `RegistryEntry`-by-
// name references in production code compile unchanged. New code MUST
// prefer the JobPolicy name when declaring entries.
type RegistryEntry struct {
	// Type is the canonical job type string (e.g. "script.generate").
	Type string
	// Description is a human-readable description for operators.
	Description string
	// Timeout is the per-job execution timeout. Zero means use the default (10 min).
	Timeout time.Duration
	// DefaultMaxRetries is the default retry count when the caller doesn't specify.
	DefaultMaxRetries int
	// Queue is the routing label for the in-process broker.
	//
	// Today the broker is in-process (Worker polls SQLite), so Queue is
	// a logical-grouping label rather than a hard routing key. Future
	// broker upgrades (e.g. a dedicated message bus with multiple queue
	// partitions) read this field to bucket workers per job family.
	// Empty value is normalised to DefaultQueue by Compose() so the
	// typed accessor `Registry.Queue(t)` returns a non-empty label for
	// ANY registered job type.
	Queue string
	// Concurrency is the per-worker parallel-lease budget for this job type.
	//
	// Today workers poll one job at a time, so this is a CLAMP rather
	// than an immediate concurrency control. Future broker upgrades
	// (e.g. an in-process semaphore that throttles expensive job types)
	// read this field to cap per-worker fan-out. Zero or negative
	// values are normalised to DefaultConcurrency by Compose() so the
	// typed accessor `Registry.Concurrency(t)` returns a value
	// >= DefaultConcurrency for ANY registered job type.
	Concurrency int
	// RequiredCapabilities are worker capabilities needed (empty = any worker).
	RequiredCapabilities []string

	// ProducesArtifacts is true when this job type produces files (videos,
	// images, documents, audio, etc.) that must be finalised through the
	// JobFinalizer spine. Jobs with ProducesArtifacts=true MUST call
	// CompleteWithArtifacts instead of the legacy Complete path.
	//
	// When true, SQLiteStore.Complete rejects completions with the canonical
	// typed sentinel domainremote.ErrCompleteJobPathViolation (godlike/06
	// SSOT: the typed sentinel at internal/domain/remote/complete_job.go is
	// the SINGLE canonical owner of the failure mode "legacy Complete path
	// attempted on artifact-producing job"; the pre-FASE-0.1 package-local
	// alias ErrArtifactJobRequiresCompleteWithArtifacts was REMOVED).
	ProducesArtifacts bool
}

// JobPolicy is the canonical alias for RegistryEntry. New code MUST
// prefer the JobPolicy name when declaring or augmenting entries;
// the RegistryEntry alias is kept for back-compat with pre-Wave-19
// callers that reference the field by name directly. Both names refer
// to the SAME type (Go type-alias semantics — not a named distinct
// type), so adapting a pre-Wave-19 RegistryEntry literal to the
// Wave-19 surface is a field-additive change, not a struct rename.
type JobPolicy = RegistryEntry

// ── Standard job.Job Types — RE-EXPORT block ──────────────────────────
//
// Each constant is an alias to the canonical SSOT in its owning
// capability domain package. Per godlike/06 one-canonical-owner-per-
// fact: ZERO new string literals here; every value resolves to a
// capability domain constant.
const (
	TypeMediaExtract     = media.TypeExtract
	TypeMediaStock       = media.TypeStock
	TypeVoiceoverBatch   = voiceover.TypeBatch
	TypeSubtitleGenerate = subtitle.TypeGenerate
	TypeRenderVideo      = video.TypeRender
	TypeYouTubeUpload    = youtube.TypeUpload
	TypeCatalogSync      = catalog.TypeSync
	TypeArtlistRun       = media.TypeArtlistRun
	TypeSystemCleanup    = system.TypeCleanup
	TypeMediaGenerate    = media.TypeGenerate
	TypeVideoGenerate    = video.TypeGenerate
	TypeBooksProcess     = books.TypeProcess
	TypeLessonsProcess   = lessons.TypeProcess
	TypeMediaReindex     = media.TypeReindex
	TypeMediaEnrich      = media.TypeEnrich
	TypeYouTubeRebuildST = youtube.TypeRebuildSearchText
	TypeDriveFolderSync  = drive.TypeFolderSync
	TypeMediaCurate      = media.TypeCurate
	TypeVoiceoverPromo   = voiceover.TypePromo
	// TypeVoiceoverGenerateItem is the per-language child job scheduled by the
	// parent voiceover.generate handler via FanoutVoiceoversUseCase
	// (PR-VOICEOVER-PARENT-CHILD-FANOUT, June 2026). Concurrency is
	// regulated by the registry's per-job-type Concurrency field
	// (configured at compose time), NOT by goroutines inside the API.
	TypeVoiceoverGenerateItem = voiceover.TypeGenerateItem
	TypeImageGenerateGoogle   = image.TypeGenerateGoogle

	// P0 Commit 2 (July 2026) canonical aliases — declared in this block
	// (NOT in codec.go) so the package-level re-export surface stays in
	// ONE place. registry_codec_completeness_test.go uses these as bare
	// identifiers when wiring canonical codecs; the canonical strings
	// themselves live in their owning capability domain packages.
	TypeAssetsResolve = asset.TypeResolve

	// Step 11B (July 2026) sibling-job type aliases. Canonical strings
	// ("script.spawn_voiceover", "script.spawn_images") live in
	// internal/domain/script per godlike/02 §Capability-specific
	// constants stay in their owning domain package.
	TypeScriptVoiceoverSibling = script.TypeVoiceoverSibling
	TypeScriptImageSibling     = script.TypeImageSibling

	// ── P0 #4 audit (audit 2026-07-03) child-job type ──
	// Audit 2026-07-03 P0 #4: per-item retry in script batches via
	// canonical child-job architecture (mirror of voiceover P0 #1
	// closure, commit 7f319edb). Each item in a multi-item batch
	// becomes a script.generate_item job with its own broker-side
	// retry envelope. Concurrency=4 per-worker per Step 11B/12B
	// sibling-fan-out budget; independent per-item retry. The
	// parent aggregator (internal/application/scripts/jobs/
	// parent_aggregator.go) ticks the children and emits FinalizeAggregateParent
	// with target_status=FAILED when aggregate=failed_terminal per
	// godlike/07 (no fake availability), otherwise SUCCEEDED.
	TypeScriptGenerateItem = script.TypeGenerateItem

	// PR-BATCH-REGISTER-ASYNC (July 2026): async clip registration via
	// the /api/media/register-batch endpoint. Each clip becomes an
	// independent media.clip job; yt-dlp + cut + Drive upload + DB write
	// happen off the request thread. ProducesArtifacts=false because the
	// registration pipeline persists its own media_assets row + outbox
	// events inside a per-clip tx (mirror of youtube_clip.extract); the
	// broker's legacy Complete is the canonical mark-SUCCEEDED seam.
	TypeClipRegister = media.TypeClipRegister

	// PR-011A (July 2026): post-publish RLM/LLM enrichment pass.
	//
	// After the stock pipeline publishes a chunk (PR-001..PR-009
	// chain), the worker enqueues a media.stock_rlm_enrich job per
	// chunk to populate the 6 LLM-only fields (Category / Event /
	// Round / Scene / Subject / Entities) that PR-007 plumbing
	// already threads but PR-008 left empty pending a real LLM call.
	// The handler is registered at composition time ONLY when
	// cfg.External.StockEnrichmentEnabled=true (godlike/07 fail-closed
	// at composition: no-enrichment-configured = no-handler-registered
	// = no-job-enqueued; the canonical retry path is via worker
	// exponential backoff when the LLM call is wired).
	//
	// ProducesArtifacts=false because the enrichment pass updates
	// media_assets.metadata_json inside a per-chunk tx and re-emits
	// the existing asset.published outbox event (Wave 5
	// SEMANTIC-LOCATION-API). The broker's legacy Complete is the
	// canonical mark-SUCCEEDED seam — no per-item finalizer needed.
	TypeMediaStockRLMEnrich = media.TypeStockRLMEnrich

	// PR-GEMMA-EXTRACT-IMPORTANT (July 2026): per-LLM-segment fan-out
	// clip extractor for POST /api/clips/extract-important. Canonical
	// string lives in internal/domain/youtube (godlike/02
	// capability-specific constants stay in their owning domain package);
	// this re-export keeps the registry_extraction.go Register call
	// site consistent with the rest of its sibling entries
	// (TypeMediaExtract, TypeYouTubeClipExtract, TypeClipRegister —
	// all unprefixed). JobType mirrors TypeYouTubeClipExtract but
	// batch-fans out per LLM-identified segment instead of per video
	// OR clip ID.
	TypeYouTubeClipExtractImportant = youtube.TypeClipExtractImportant

	// Canonical job types that were missing from the re-export block.
	// They live in their owning capability domain packages and are
	// re-exported here so sibling registry_*.go files can reference
	// them as bare identifiers.
	TypeVoiceoverGenerate      = voiceover.TypeGenerate
	TypeYouTubeClipExtract     = youtube.TypeClipExtract
	TypeScriptGenerate         = script.TypeGenerate
	TypeImagesGenerate         = image.TypeImagesGenerate
	TypeDocumentGenerate       = document.TypeGenerate
	TypeBulkUploadYouTubeClips = media.TypeBulkUploadYouTubeClips
	TypeAssetTextMaterialize   = asset.TypeTextMaterialize

	// PR-YOUTUBE-EXTRACT-REGISTRY (July 2026): youtube.extract is registered
	// in registry_extraction.go via the domain/youtube package constant.
	TypeYouTubeExtract = youtube.TypeExtract
)
