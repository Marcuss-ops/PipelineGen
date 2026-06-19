// Package job defines the canonical domain types for the job system.
//
// Deprecated: Types are now canonically defined in internal/domain/job/.
// This file re-exports them as type aliases for backward compatibility.
package jobs

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Type aliases (canonical types in domain/job) ────────────────────

type Status = job.Status
type Filter = job.Filter
type Job = job.Job
type Event = job.Event

// ── Re-exported constants ───────────────────────────────────────────

const (
	StatusQueued    = job.StatusQueued
	StatusLeased    = job.StatusLeased
	StatusRunning   = job.StatusRunning
	StatusRetryWait = job.StatusRetryWait
	StatusSucceeded = job.StatusSucceeded
	StatusFailed    = job.StatusFailed
	StatusCancelled = job.StatusCancelled

	JobTypeMediaExtract           = job.TypeMediaExtract
	JobTypeMediaStock             = job.TypeMediaStock
	JobTypeVoiceoverBatch         = job.TypeVoiceoverBatch
	JobTypeSubtitleGenerate       = job.TypeSubtitleGenerate
	JobTypeRenderVideo            = job.TypeRenderVideo
	JobTypeYouTubeUpload          = job.TypeYouTubeUpload
	JobTypeYouTubeClipExtract     = job.TypeYouTubeClipExtract
	JobTypeCatalogSync            = job.TypeCatalogSync
	JobTypeArtlistRun             = job.TypeArtlistRun
	JobTypeSystemCleanup          = job.TypeSystemCleanup
	JobTypeMediaGenerate          = job.TypeMediaGenerate
	JobTypeVideoGenerate          = job.TypeVideoGenerate
	JobTypeBooksProcess           = job.TypeBooksProcess
	JobTypeLessonsProcess         = job.TypeLessonsProcess
	JobTypeMediaReindex           = job.TypeMediaReindex
	JobTypeYouTubeRebuildST       = job.TypeYouTubeRebuildST
	JobTypeBatchScriptGenerate    = job.TypeBatchScriptGenerate
	JobTypeClipScriptGenerate     = job.TypeClipScriptGenerate
	JobTypeCatalogScriptGenerate  = job.TypeCatalogScriptGenerate
	JobTypeBulkUploadYouTubeClips = job.TypeBulkUploadYouTubeClips
	JobTypeDriveFolderSync        = job.TypeDriveFolderSync
)
