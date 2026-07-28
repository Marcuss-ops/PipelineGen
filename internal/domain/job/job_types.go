// Package job provides type aliases for the canonical kernel/job types
// (Phase A.2, June 2026).
//
// Production definitions live in internal/kernel/job/. This package is
// preserved for back-compat — import sites resolve unchanged because Go
// type aliases are transparent at the package boundary. Future code
// should import internal/kernel/job directly.
//
// Capability-specific Type* string constants are owned by their respective
// domain packages (godlike/02). They are re-exported here for back-compat
// during the Wave 5 contraction window.
//
// PR-RENAME-DOMAIN-JOB (July 2026): renamed from job.go to job_types.go
// to match the codebase's godlike/06 SSOT convention (the file that
// owns a per-fact topic MUST carry the topic name in its filename).
// The 14 per-domain job_types.go files remain the canonical SSOT
// per godlike/02.
package job

import (
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
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases to canonical kernel/job types (Phase A.2) ──────────

type (
	// Status is the canonical 8-state job lifecycle (see kernel/job.Status).
	Status = job.Status
	// Filter narrows job queries (see kernel/job.Filter).
	Filter = job.Filter
	// Job is the canonical domain entity for a job in the system.
	Job = job.Job
	// Event represents a discrete event in a job's timeline.
	Event = job.Event
)

// ── Status constant aliases to canonical kernel/job constants ──────
//
// Go permits typed-constant aliases: const X = job.Y produces a
// new const of identical type and value. Equality holds both ways:
//   job.StatusQueued == job.StatusQueued (true)
//   var x job.Status = job.StatusQueued         (compiles)

const (
	StatusQueued             = job.StatusQueued
	StatusLeased             = job.StatusLeased
	StatusRunning            = job.StatusRunning
	StatusWaitingChildren    = job.StatusWaitingChildren
	StatusFinalizing         = job.StatusFinalizing
	StatusRetryWait          = job.StatusRetryWait
	StatusSucceeded          = job.StatusSucceeded
	StatusPartiallySucceeded = job.StatusPartiallySucceeded
	StatusIndexPending       = job.StatusIndexPending
	StatusFailed             = job.StatusFailed
	StatusCancelled          = job.StatusCancelled
)

// ── Job type string constants ───────────────────────────────────────
//
// Per the godlike/02 ≥2-capability rule, capability-specific job.Type
// discriminator constants now live in their owning domain packages.
// domain/job re-exports them for back-compat during the Wave 5
// contraction window.
const (
	// Media job types are owned by internal/domain/media.
	TypeMediaExtract           = media.TypeExtract
	TypeMediaStock             = media.TypeStock
	TypeArtlistRun             = media.TypeArtlistRun
	TypeMediaGenerate          = media.TypeGenerate
	TypeMediaReindex           = media.TypeReindex
	TypeMediaEnrich            = media.TypeEnrich
	TypeBulkUploadYouTubeClips = media.TypeBulkUploadYouTubeClips
	TypeMediaCurate            = media.TypeCurate
	TypeClipRegister           = media.TypeClipRegister
	TypeMediaStockRLMEnrich    = media.TypeStockRLMEnrich

	// Voiceover job types are owned by internal/domain/voiceover.
	TypeVoiceoverBatch        = voiceover.TypeBatch
	TypeVoiceoverGenerate     = voiceover.TypeGenerate
	TypeVoiceoverGenerateItem = voiceover.TypeGenerateItem
	TypeVoiceoverPromo        = voiceover.TypePromo

	// Subtitle job types are owned by internal/domain/subtitle.
	TypeSubtitleGenerate = subtitle.TypeGenerate

	// Video/render job types are owned by internal/domain/video.
	TypeRenderVideo   = video.TypeRender
	TypeVideoGenerate = video.TypeGenerate

	// YouTube job types are owned by internal/domain/youtube.
	TypeYouTubeUpload               = youtube.TypeUpload
	TypeYouTubeClipExtract          = youtube.TypeClipExtract
	TypeYouTubeClipExtractImportant = youtube.TypeClipExtractImportant
	TypeYouTubeRebuildST            = youtube.TypeRebuildSearchText

	// Catalog job types are owned by internal/domain/catalog.
	TypeCatalogSync = catalog.TypeSync

	// System job types are owned by internal/domain/system.
	TypeSystemCleanup = system.TypeCleanup

	// Books job types are owned by internal/domain/books.
	TypeBooksProcess = books.TypeProcess

	// Lessons job types are owned by internal/domain/lessons.
	TypeLessonsProcess = lessons.TypeProcess

	// Script job types are owned by internal/domain/script.
	TypeScriptGenerate         = script.TypeGenerate
	TypeScriptVoiceoverSibling = script.TypeVoiceoverSibling
	TypeScriptImageSibling     = script.TypeImageSibling
	TypeScriptGenerateItem     = script.TypeGenerateItem

	// Drive job types are owned by internal/domain/drive.
	TypeDriveFolderSync = drive.TypeFolderSync

	// Image job types are owned by internal/domain/image.
	TypeImagesGenerate      = image.TypeImagesGenerate
	TypeImageGenerateGoogle = image.TypeGenerateGoogle

	// Document job types are owned by internal/domain/document.
	TypeDocumentGenerate = document.TypeGenerate

	// Asset job types are owned by internal/domain/asset.
	TypeAssetsResolve        = asset.TypeResolve
	TypeAssetTextMaterialize = asset.TypeTextMaterialize
)
