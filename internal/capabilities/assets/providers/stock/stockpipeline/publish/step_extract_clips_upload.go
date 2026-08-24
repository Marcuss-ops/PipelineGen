// Package stockpipeline — step_extract_clips_upload.go
// (PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// Extracted from step_extract_clips.go per godlike/06 SSOT
// one-canonical-owner-per-fact. Owns the concurrent upload worker
// pool and the per-task local types (clipUploadTask, clipUploadResult).
package assets

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// clipUploadTask is a prepared clip ready for concurrent Drive upload.
type clipUploadTask struct {
	clipIdx         int
	plan            ClipPlan
	cVA             finalization.VerifiedArtifact
	segmentFilename string
	leafName        string
}

// clipUploadResult pairs a published ChunkState with its leafName
// or carries an error when the upload step failed.
type clipUploadResult struct {
	chunk    ChunkState
	leafName string
	err      error
}
