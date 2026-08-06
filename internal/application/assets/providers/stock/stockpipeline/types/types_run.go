// Package stockpipeline — types_run.go (Stock P0 split, July 2026).
//
// This file owns the DTOs previously co-located in service.go:
// RunInput, ChunkMetadataInput, PipelineMetadata, ChunkMeta,
// SourceInfo, ClipInfo, PipelineInfo, PipelineResult, ChunkResult,
// VideoSource, StagedSource.
//
// godlike/06 SSOT: one canonical owner for stock run types.
// All callers (adapter.go, orchestrator.go, run_orchestrator.go,
// usecase.go, stager_adapter.go) are in the same package and
// reference these types directly.
package types

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// ClipSpec defines a single clip to extract from a source video.
// Used with Clips field on RunInput to bypass the deterministic planner.
//
// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): adds 4 typed fields
// that travel end-to-end (ClipSpec → ClipPlan → ChunkState →
// ChunkMetadataEntry → Qdrant semantic payload):
//
//   - Round    int       — boxing-style round number (1-12). Surfaced
//     in metadata.json + Qdrant payload for
//     semantic filtering ("round 7").
//   - Tags     []string  — free-form per-clip tags (people, theme,
//     technique). Surfaced in metadata.json +
//     indexed via BM25 sparse vector.
//   - Category string     — content category (boxing / running / etc.).
//     Carries the stock pipeline's "sport" axis.
//   - Slug     string     — explicit operator-supplied Drive folder
//     slug for this clip. Wins over the title-
//     derived slug in perClipLeafName (the
//     publisher's per-clip leaf derivation).
//     Use this when the title contains characters
//     that don't slugify cleanly (e.g. accented
//     Portuguese "Broner barcolla" stays verbatim).
//   - ParentSlug string   — optional operator-supplied slug for the
//     parent timestamp folder. When explicit clips are split into
//     5-second children, this value is preserved so all children land
//     under the same Drive folder.
type ClipSpec struct {
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url,omitempty"`
	StartSec    float64  `json:"start_sec"`
	EndSec      float64  `json:"end_sec"`
	Round       int      `json:"round,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Category    string   `json:"category,omitempty"`
	Slug        string   `json:"slug,omitempty"`
	ParentSlug  string   `json:"parent_slug,omitempty"`
}

// RunInput holds the parameters for a stock pipeline run.
//
// §12-7 (July 2026): adds FinalizationLease (the canonical
// finalization.Lease the JobFinalizer validates inside the
// spine-write TX, extracted from broker job by HandleJob via
// extractLease) and PolicyVersion (the run-fingerprint salt
// godlike/07 SSOT, propagated to the per-run metadata.json).
//
// Explicit clips (July 2026): Clips field holds pre-defined
// timestamp ranges that bypass the deterministic planner.
// DriveURLs field holds Google Drive source URLs.
type RunInput struct {
	SearchQueries []string
	DirectURLs    []string
	DriveURLs     []string
	Clips         []ClipSpec
	TotalMinutes  int
	// Explicit stock contract. These fields are the source of truth for
	// bounded stock runs; TotalMinutes remains only for legacy callers.
	TargetTotalDurationSeconds     int
	TargetDurationPerSourceSeconds int
	ClipsPerSource                 int
	ClipDurationSeconds            int
	DownloadMode                   string
	MaxVideos                      int
	ChunkDuration                  int
	ClipDuration                   int
	SecondsPerSegment              int
	NoAudio                        bool
	NoEffects                      bool
	NoTransitions                  bool
	Subfolder                      string
	FolderName                     string
	DriveFolderID                  string
	FolderID                       string
	DriveFolderResolved            bool
	Metadata                       *ChunkMetadataInput
	Progress                       func(percent int, message string)

	// §12-7 (July 2026).
	FinalizationLease finalization.Lease // broker lease for StockFinalizeStep spine write
	PolicyVersion     string             // run-fingerprint salt used by orchestrator + metadata.json

	// Persist (July 2026) enables media_assets writing in sync mode.
	// When true, the Service routes through the resilient orchestrator
	// path (RunResilient) with a synthetic broker lease, so the
	// StockFinalizeStep writes to media_assets via the single-TX spine.
	// Defaults to false — existing sync-mode callers are unaffected.
	Persist bool
}

// ChunkMetadataInput holds user-provided metadata for chunks.
type ChunkMetadataInput struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// PipelineMetadata is the single metadata JSON uploaded at the end with all chunks.
type PipelineMetadata struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Source      SourceInfo        `json:"source"`
	Pipeline    PipelineInfo      `json:"pipeline"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
	Chunks      []ChunkMeta       `json:"chunks"`
}

// ChunkMeta describes a single chunk within the pipeline metadata.
type ChunkMeta struct {
	Index         int        `json:"index"`
	TimelineStart float64    `json:"timeline_start"`
	TimelineEnd   float64    `json:"timeline_end"`
	DriveLink     string     `json:"drive_link,omitempty"`
	DownloadLink  string     `json:"download_link,omitempty"`
	Clips         []ClipInfo `json:"clips"`
}

// SourceInfo describes the source video.
type SourceInfo struct {
	URL      string  `json:"url"`
	Title    string  `json:"title,omitempty"`
	Duration float64 `json:"duration_sec,omitempty"`
}

// ClipInfo describes a single clip within a chunk.
type ClipInfo struct {
	Index int    `json:"index"`
	Start string `json:"start"`
	End   string `json:"end"`
	Title string `json:"title,omitempty"`
}

// PipelineInfo describes pipeline settings used.
type PipelineInfo struct {
	ClipDuration  int  `json:"clip_duration"`
	ChunkDuration int  `json:"chunk_duration"`
	NoAudio       bool `json:"no_audio"`
	NoEffects     bool `json:"no_effects"`
	NoTransitions bool `json:"no_transitions"`
}

// PipelineResult holds the results of a stock pipeline run.
type PipelineResult struct {
	SearchTerms    []string      `json:"search_terms"`
	TotalClips     int           `json:"total_clips"`
	TotalChunks    int           `json:"total_chunks"`
	Chunks         []ChunkResult `json:"chunks"`
	MetadataLink   string        `json:"metadata_link,omitempty"`
	MetadataFileID string        `json:"metadata_file_id,omitempty"`
}

// ChunkResult represents a single rendered and uploaded video chunk.
//
// Blocco 1b (July 2026): added Rendered / Uploaded outcome fields so
// callers can distinguish which stages completed. Pre-fix callers had
// no way to know whether the chunk's DriveLink was real or
// empty-because-upload-failed.
//
// Stock Cutover Commit 4-expanded (July 2026): the previously-typed
// `internal/.../types_status.go` (deleted in Commit 4) and the
// `run_upload_indexing_test.go` for the canonical 3-test failure-mode
// contract that replaces the field-level signal. Per-job post-emission
// indexing state is now surfaced at the orchestrator level via
// `job.StatusIndexPending` (see domain/job/job.go), not at the per-
// chunk level.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type ChunkResult struct {
	Index         int      `json:"index"`
	TimelineStart float64  `json:"timeline_start"`
	TimelineEnd   float64  `json:"timeline_end"`
	LocalPath     string   `json:"local_path"`
	DriveLink     string   `json:"drive_link"`
	DownloadLink  string   `json:"download_link"`
	DriveFileID   string   `json:"drive_file_id"`
	SHA256        string   `json:"sha256"`
	Title         string   `json:"title"`
	SourceIDs     []string `json:"source_ids,omitempty"`
	// Rendered is true when the FFmpeg render step completed and the
	// chunk file exists on disk.
	Rendered bool `json:"rendered"`
	// Uploaded is true when Publisher.Publish wrote the chunk to Drive.
	Uploaded bool `json:"uploaded"`
}

// VideoSource represents a single video to be downloaded and processed.
type VideoSource struct {
	URL         string
	Title       string
	Source      string
	DurationSec float64
}

// StagedSource is the result of a lightweight StageSource call.
// It contains only the downloaded file — no render, upload, or indexing.
// The caller owns the file at LocalPath and is responsible for cleanup.
//
// Blocco 2a (July 2026): created to separate the "fetch" contract from
// the full pipeline (render → upload → index). Adapter.Fetch uses this
// instead of Run so the staged file survives the return.
type StagedSource struct {
	LocalPath string
	Bytes     int64
}
