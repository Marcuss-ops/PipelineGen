// Package stockpipeline — step_extract_clips_upload.go
// (PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// Extracted from step_extract_clips.go per godlike/06 SSOT
// one-canonical-owner-per-fact. Owns the concurrent upload worker
// pool and the per-task local types (clipUploadTask, clipUploadResult).
package stockpipeline

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// clipUploadTask is a prepared clip ready for concurrent Drive upload.
//
// clip is the canonical asset awaiting its media commit. The upload worker
// attaches the published Drive identity to it, so the commit that follows the
// publication carries the Drive location as its primary location instead of a
// local-only one (see publishCuts for the ordering contract).
type clipUploadTask struct {
	clipIdx         int
	plan            ClipPlan
	cVA             finalization.VerifiedArtifact
	segmentFilename string
	leafName        string
	clip            *asset.Asset
}

// clipUploadResult pairs a published ChunkState with its leafName
// or carries an error when the upload step failed.
type clipUploadResult struct {
	chunk    ChunkState
	leafName string
	err      error
}
