// Package job provides type aliases for the canonical kernel/job types
// (Phase A.2, June 2026).
//
// Production definitions live in internal/kernel/job/. This package is
// preserved for back-compat — 107 import sites in 93 files resolve
// unchanged because Go type aliases are transparent at the package
// boundary. Future code should import internal/kernel/job directly.
//
// What stayed in domain/job (intentionally NOT migrated to kernel):
//   - 22 Type* string constants (job.Type discriminator constants).
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
	StatusQueued       = kerneljob.StatusQueued
	StatusLeased       = kerneljob.StatusLeased
	StatusRunning      = kerneljob.StatusRunning
	StatusFinalizing   = kerneljob.StatusFinalizing
	StatusRetryWait    = kerneljob.StatusRetryWait
	StatusSucceeded    = kerneljob.StatusSucceeded
	StatusIndexPending = kerneljob.StatusIndexPending
	StatusFailed       = kerneljob.StatusFailed
	StatusCancelled    = kerneljob.StatusCancelled
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
//     TypeYouTubeRebuildST
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
)
