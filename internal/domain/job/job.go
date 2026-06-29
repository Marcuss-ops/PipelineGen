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
	// Status is the canonical 7-state job lifecycle (see kernel/job.Status).
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
	StatusQueued    = kerneljob.StatusQueued
	StatusLeased    = kerneljob.StatusLeased
	StatusRunning   = kerneljob.StatusRunning
	StatusRetryWait = kerneljob.StatusRetryWait
	StatusSucceeded = kerneljob.StatusSucceeded
	StatusFailed    = kerneljob.StatusFailed
	StatusCancelled = kerneljob.StatusCancelled
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
//     TypeYouTubeRebuildST, TypeYouTubeChannelSync
//   - catalog: TypeCatalogSync
//   - system: TypeSystemCleanup
//   - books: TypeBooksProcess
//   - lessons: TypeLessonsProcess
//   - scripts: TypeScriptGenerate
//   - drive: TypeDriveFolderSync
const (
	TypeMediaExtract           = "media.extract"
	TypeMediaStock             = "media.stock"
	TypeVoiceoverBatch         = "voiceover.batch"
	TypeVoiceoverGenerate      = "voiceover.generate"
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
	TypeYouTubeChannelSync     = "youtube.channel.sync"
)
