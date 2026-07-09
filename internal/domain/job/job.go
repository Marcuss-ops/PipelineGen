// Package job provides type aliases for the canonical kernel/job types
// (Phase A.2, June 2026).
//
// Production definitions live in internal/kernel/job/. This package is
// preserved for back-compat — 107 import sites in 93 files resolve
// unchanged because Go type aliases are transparent at the package
// boundary. Future code should import internal/kernel/job directly.
//
// What stayed in domain/job (intentionally NOT migrated to kernel):
//   - Type* string constants (job.Type discriminator constants).
//     These are CAPABILITY-SPECIFIC (TypeMediaExtract lives with the
//     media capability, TypeScriptGenerate with the scripts capability,
//     etc.) and fail the ≥2-capability kernel eligibility rule. They
//     remain the SSOT for job.Type values used across the codebase.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases to canonical kernel/job types (Phase A.2) ──────────

type (
	// Status is the canonical 8-state job lifecycle (see kernel/job.Status).
	Status = kerneljob.Status
	// Filter narrows job queries (see kernel/job.Filter).
	Filter = kerneljob.Filter
	// Job is the canonical domain entity for a job in the system.
	Job = kerneljob.Job
	// Event represents a discrete event in a job's timeline.
	Event = kerneljob.Event
)

// ── Status constant aliases to canonical kernel/job constants ──────
//
// Go permits typed-constant aliases: const X = kerneljob.Y produces a
// new const of identical type and value. Equality holds both ways:
//   job.StatusQueued == kerneljob.StatusQueued (true)
//   var x job.Status = job.StatusQueued         (compiles)

const (
	StatusQueued             = kerneljob.StatusQueued
	StatusLeased             = kerneljob.StatusLeased
	StatusRunning            = kerneljob.StatusRunning
	StatusFinalizing         = kerneljob.StatusFinalizing
	StatusRetryWait          = kerneljob.StatusRetryWait
	StatusSucceeded          = kerneljob.StatusSucceeded
	StatusPartiallySucceeded = kerneljob.StatusPartiallySucceeded
	StatusIndexPending       = kerneljob.StatusIndexPending
	StatusFailed             = kerneljob.StatusFailed
	StatusCancelled          = kerneljob.StatusCancelled
)

// ── Job type string constants ───────────────────────────────────────
//
// Per the godlike/02 ≥2-capability rule, capability-specific job.Type
// discriminator constants stay in domain/job (canonical). Each
// constant is owned by exactly one capability:
//
//   - media: TypeMediaExtract, TypeMediaStock, TypeArtlistRun,
//     TypeMediaGenerate, TypeMediaReindex, TypeMediaEnrich,
//     TypeBulkUploadYouTubeClips, TypeMediaCurate
//   - voiceover: TypeVoiceoverBatch, TypeVoiceoverPromo
//   - render/video: TypeRenderVideo, TypeVideoGenerate
//   - subtitles: TypeSubtitleGenerate
//   - youtube: TypeYouTubeUpload, TypeYouTubeClipExtract,
//     TypeYouTubeClipExtractImportant, TypeYouTubeRebuildST
//   - catalog: TypeCatalogSync
//   - system: TypeSystemCleanup
//   - books: TypeBooksProcess
//   - lessons: TypeLessonsProcess
//   - scripts: TypeScriptGenerate
//   - drive: TypeDriveFolderSync
const (
	TypeMediaExtract      = "media.extract"
	TypeMediaStock        = "media.stock"
	TypeVoiceoverBatch    = "voiceover.batch"
	TypeVoiceoverGenerate = "voiceover.generate"
	// TypeVoiceoverGenerateItem is the per-language child job scheduled by the
	// parent voiceover.generate handler via FanoutVoiceoversUseCase
	// (PR-VOICEOVER-PARENT-CHILD-FANOUT, June 2026). Concurrency is regulated
	// by the registry's per-job-type Concurrency field, NOT by goroutines
	// inside the API. Independent retry.
	TypeVoiceoverGenerateItem  = "voiceover.generate_item"
	TypeSubtitleGenerate       = "subtitle.generate"
	TypeRenderVideo            = "render.video"
	TypeYouTubeUpload          = "youtube.upload"
	TypeYouTubeClipExtract     = "youtube_clip.extract"
	TypeCatalogSync            = "catalog.sync"
	TypeArtlistRun             = "media.artlist"
	TypeSystemCleanup          = "system.cleanup"
	TypeMediaGenerate          = "media.generate_missing_asset"
	TypeVideoGenerate          = "video.generate"
	TypeBooksProcess           = "books.process"
	TypeLessonsProcess         = "lessons.process"
	TypeMediaReindex           = "media.reindex"
	TypeMediaEnrich            = "media.enrich"
	TypeYouTubeRebuildST       = "youtube.rebuild_search_text"
	TypeScriptGenerate         = "script.generate"
	TypeBulkUploadYouTubeClips = "media.bulk_upload_youtube_clips"
	TypeDriveFolderSync        = "drive.folder.sync"
	TypeMediaCurate            = "media.curate"
	TypeVoiceoverPromo         = "voiceover.promo"

	// ── Spina Dorsale Fase 2 (July 2026): downstream artifact jobs ──

	// TypeImagesGenerate is the canonical job type for AI image
	// generation. Scheduled by the workflow coordinator after
	// script.generate completes.
	TypeImagesGenerate = "images.generate"

	// TypeAssetsResolve is the canonical job type for semantic
	// asset resolution. Scheduled by the workflow coordinator
	// after script.generate completes. Reads AssetRequirements
	// and resolves clips/stock via Qdrant.
	TypeAssetsResolve = "assets.resolve"

	// TypeDocumentGenerate is the canonical job type for Google
	// Doc creation. Scheduled by the workflow coordinator after
	// script.generate completes.
	TypeDocumentGenerate = "document.generate"

	// ── Step 11B (July 2026): script.generate sibling job types ──

	// TypeScriptVoiceoverSibling is the canonical sibling job type
	// for voiceover assets spawned by HandleClipScriptGenerateJob
	// immediately after the script job is enqueued. Each sibling
	// carries ParentJobID = the parent script.generate JobID and
	// AssetRequirements.Required drives the parent's fail-closed
	// policy (Step 11B (d)): if any REQUIRED voiceover sibling
	// FAILED (or was never enqueued), the parent script.generate
	// transitions to FAILED with PartialReason="missing_required_downstream".
	//
	// Concurrency is bounded at 4 per-worker (configured in
	// internal/application/jobs/registry.go). Independent retry
	// per sibling (no shared retry envelope).
	TypeScriptVoiceoverSibling = "script.spawn_voiceover"

	// TypeScriptImageSibling is the canonical sibling job type for
	// AI image assets spawned by HandleClipScriptGenerateJob. The
	// image sibling mirrors TypeScriptVoiceoverSibling's surface:
	// ParentJobID + AssetRequirements + Concurrency: 4 + independent
	// retry. Fail-closed propagation is uniform across both sibling
	// classes via the aggregator (Step 12B's ChildTerminatedEvent
	// pipeline).
	TypeScriptImageSibling = "script.spawn_images"

	// ── P0 #4 audit (audit 2026-07-03) child-job architecture ──

	// TypeScriptGenerateItem is the canonical child job type for
	// script.generate batches. Each item in a multi-item batch becomes
	// a separate script.generate_item job, with its own broker-side
	// retry envelope. The canonical parent aggregator
	// (internal/application/scripts/jobs/parent_aggregator.go)
	// ticks the children and calls FinalizeAggregateParent to set the parent's
	// final broker status based on the aggregate.
	//
	// The child type mirrors TypeVoiceoverGenerateItem on the
	// voiceover side (P0 #1 closure, 7f319edb); the same 4-step
	// pattern is replicated here — child jobs are an OWNED fact of
	// the script-batch capability (godlike/06 one-canonical-owner-per-fact).
	TypeScriptGenerateItem = "script.generate_item"

	// PR-BATCH-REGISTER-ASYNC (July 2026): async clip registration via
	// the /api/media/register-batch endpoint. Each clip becomes an
	// independent media.clip job; yt-dlp + cut + Drive upload + DB write
	// happen off the request thread. ProducesArtifacts=false because the
	// registration pipeline persists its own media_assets row + outbox
	// events inside a per-clip tx (mirror of youtube_clip.extract); the
	// broker's legacy Complete is the canonical mark-SUCCEEDED seam.
	TypeClipRegister = "media.clip"

	// PR-GEMMA-EXTRACT-IMPORTANT (July 2026): per-LLM-segment YouTube clip
	// extraction for the POST /api/clips/extract-important flow. Each
	// important LLM-identified segment is downloaded (yt-dlp SectionDownloader),
	// uploaded to a per-video Drive subfolder, then committed via the
	// canonical ClipAtomicWriter in a per-clip atomic tx (single-tx
	// media_assets INSERT + outbox_events asset.index.requested INSERT).
	// Owned by the youtube capability per the capability listing SSOT
	// (the `youtube:` line in the const-block goddoc above); the LLM-driven
	// per-segment fan-out is PRODUCED BY the ExtractImportantClips pipeline
	// at internal/application/youtube/usecase/extract_important_clips.go.
	//
	// ProducesArtifacts=true per-extracted-clips batch — each clip is its own
	// per-clip atomic tx (ClipAtomicWriter writes media_assets + outbox in
	// one tx), so the canonical pattern is: handler returns SUCCEEDED on the
	// batch envelope; the broker's legacy Complete marks the job SUCCEEDED
	// without trying to persist artifacts (the per-clip tx already did).
	TypeYouTubeClipExtractImportant = "youtube.clip_extract_important"

	// PR-011A (July 2026): post-publish RLM/LLM enrichment pass.
	TypeMediaStockRLMEnrich = "media.stock_rlm_enrich"

	// TypeImageGenerateGoogle is the job type for Google Slides image generation.
	TypeImageGenerateGoogle = "image.generate.google"
)
