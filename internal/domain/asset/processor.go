// Package asset — MediaTransformer contracts + processing-stage type surface
// (PR-MEDIATRANSFORMER-RENAME, July 2026).
//
// Rename (July 2026): the legacy `Processor` interface is renamed to
// `MediaTransformer`, the input DTO `ProcessInput` is renamed to
// `TransformSpec`, and the output DTO `ProcessResult` is renamed to
// `RenditionSet`. The new contract receives a `StagedSource` (from
// the canonical assets.SourceStager port) + a `TransformSpec` and
// produces a local `RenditionSet`.
//
// godlike/06 SSOT: this file is the canonical owner of both the
// `MediaTransformer` and the legacy `Processor` contracts. No other
// file in the codebase may declare a type with these names.
//
// PR-MEDIATRANSFORMER-RENAME step 1 (July 2026): the god service
// is ONLY renamed in this commit. The forbidden fields
// (DriveFileID, DriveLink, DownloadLink, MD5, PublishAction,
// FolderID, ClipPageURL) STAY in `RenditionSet` for now — they
// are removed in subsequent steps. The new scan check
// `percheck_mediatransformer_no_infra_fields` (registered in
// cmd/archcheck/runner.go) is the forward-prevention gate that
// will trip on those existing fields and guide the field-removal
// steps.
//
// Backward compatibility (step 1, July 2026): the legacy `Processor`
// interface is retained (deprecated) with the same `Process` method
// signature so the ~50 existing callers in
// `internal/application/{youtube,clips,artlist,voiceover}` and
// `internal/api/assets/clips` continue to compile without churn.
// The legacy DTOs `ProcessInput` and `ProcessResult` are Go type
// aliases of `TransformSpec` and `RenditionSet` so a single struct
// serves both names. The alias is removed in step 2 when the
// forbidden fields are deleted and all callers migrate to the new
// `Transform` method.
//
// This file carries ONLY the canonical type surface:
// ProcessingStatus enum, ProcessingStage enum, ProcessingRecord struct,
// MediaTransformer interface, TransformSpec / RenditionSet DTOs,
// legacy Processor interface (deprecated), and the type aliases.
// NO SQL primitives, NO `database/sql` import.
package asset

import (
	"context"
	"time"
)

// ProcessingStatus is the 4-state lifecycle of a processing step.
type ProcessingStatus string

const (
	StatusPending   ProcessingStatus = "pending"
	StatusRunning   ProcessingStatus = "running"
	StatusCompleted ProcessingStatus = "completed"
	StatusFailed    ProcessingStatus = "failed"
)

// ProcessingStage is a canonical processing step name.
type ProcessingStage string

const (
	StageDownload      ProcessingStage = "download"
	StageNormalize     ProcessingStage = "normalize"
	StageTranscription ProcessingStage = "transcription"
	StageEmbedding     ProcessingStage = "embedding"
	StageIndexing      ProcessingStage = "indexing"
	StageUpload        ProcessingStage = "upload"
	StageVerify        ProcessingStage = "verify"
	StageCleanup       ProcessingStage = "cleanup"
)

// ProcessingRecord represents a single processing step for an asset.
type ProcessingRecord struct {
	AssetID      string           `json:"asset_id"`
	Step         string           `json:"step"`
	Status       ProcessingStatus `json:"status"`
	StartedAt    *time.Time       `json:"started_at,omitempty"`
	CompletedAt  *time.Time       `json:"completed_at,omitempty"`
	ErrorMessage string           `json:"error_message,omitempty"`
	AttemptCount int              `json:"attempt_count"`
	MetadataJSON string           `json:"metadata_json,omitempty"`
}

// MediaTransformer is the canonical interface for transforming a
// media asset that has already been staged to a deterministic local
// file (via the assets.SourceStager port). It runs the FFmpeg
// normalization/rendition pipeline, computes hashes, and returns a
// local `RenditionSet`.
//

// TransformSpec contains the input for transforming a staged media
// asset.
//
// PR-MEDIATRANSFORMER-RENAME (July 2026): the legacy `ProcessInput`
// is renamed to `TransformSpec`. The caller is expected to pass the
// `StagedSource` separately (as the first method parameter) so the
// staged LocalPath + IntermediateHash are NOT duplicated in this
// DTO. `StagedSource` is the canonical owner of the on-disk file
// path + hash; `TransformSpec` carries the transformation policy.
//
// PR-MEDIATRANSFORMER-RENAME step 1 (July 2026): the god service
// is ONLY renamed. The forbidden fields (DriveFileID, DriveLink,
// DownloadLink, MD5, PublishAction, FolderID, ClipPageURL) STAY
// for now — they are removed in subsequent steps. The new scan
// check `percheck_mediatransformer_no_infra_fields` is the
// forward-prevention gate that will trip on those existing fields.
type TransformSpec struct {
	ID   string
	Name string
	// LocalPath is the on-disk file path of the staged source. It
	// is normally derived from the StagedSource parameter; callers
	// should pass `staged.LocalPath` here. Kept as a separate field
	// so tests can construct a TransformSpec without a StagedSource.
	LocalPath        string
	SourceURL        string
	Term             string
	OutputDir        string
	Filename         string
	FolderID         string // FORBIDDEN (Drive) — removed in step 2
	Duration         int
	ForceKeyframes   bool
	StreamCopy       bool
	DownloadSections []string
	Normalize        *bool
	KeepAudio        bool
	DisableDuration  bool
	Width            int
	Height           int
	DriveFileID      string // FORBIDDEN (Drive) — removed in step 2
	ClipPageURL      string // FORBIDDEN (Drive) — removed in step 2
	Metadata         map[string]any
	// RenditionLayout (July 2026) signals that the transformer should
	// store generated files under rendition-kind subdirectories
	// (master, mezzanine, proxy, thumbnail, storyboard) inside
	// OutputDir and return them in RenditionSet.Renditions.
	// When false, the legacy flat layout is preserved.
	RenditionLayout bool
}

// RenditionSet contains the local result of transforming a media
// asset.
//
// PR-MEDIATRANSFORMER-RENAME (July 2026): the legacy `ProcessResult`
// is renamed to `RenditionSet`. The DTO is the canonical output of
// MediaTransformer.Transform and carries only local media metadata.
//
// PR-MEDIATRANSFORMER-RENAME step 1 (July 2026): the god service
// is ONLY renamed. The forbidden fields (DriveLink, DriveFileID,
// DownloadLink, MD5, PublishAction) STAY for now — they are removed
// in subsequent steps. The new scan check
// `percheck_mediatransformer_no_infra_fields` is the forward-
// prevention gate that will trip on those existing fields and guide
// the field-removal steps.
//
// F2.8 (June 2026): MD5 + PublishAction added. The pre-F2.8 transformer
// ran the upload path through drive.Uploader.UploadFile which
// returned only {FileID, WebViewLink, DownloadLink}. With the
// migration to delivery.Publisher.Publish the canonical PublishResult
// additionally surfaces {MD5Checksum, Action} — the
// Drive-calculated MD5 (the canonical "this is what Drive has stored"
// checksum, distinct from the locally-computed FileHash used for
// pre-upload dedup) AND the PublishAction enum (created/updated/
// skipped/renamed) so downstream consumers can tell whether a row
// already existed on Drive.
//
// Both fields are net-new (no omitempty since RenditionSet DTOs are
// not serialised). Pre-F2.8 callers that only relied on FileHash +
// DriveLink/DriveFileID/DownloadLink keep working unchanged. MD5 is
// "string" so delivery.PublishResult.MD5Checksum maps 1-a-1;
// PublishAction is "string" (NOT typed delivery.PublishAction) so
// the domain layer stays free of delivery-package imports (AGENTS.md
// Pattern 8: domain is the bottom of the import graph).
//
// Rendition storage (July 2026): Renditions carries the generated
// technical variants (master, mezzanine, proxy, thumbnail, storyboard)
// so callers can persist them into asset_locations + asset_renditions.
type RenditionSet struct {
	ID            string
	Filename      string
	LocalPath     string
	FileHash      string
	ContentHash   string
	DriveLink     string // FORBIDDEN (Drive) — removed in step 2
	DriveFileID   string // FORBIDDEN (Drive) — removed in step 2
	DownloadLink  string // FORBIDDEN (Drive) — removed in step 2
	MD5           string // FORBIDDEN (Drive) — removed in step 2; drive-returned md5Checksum
	PublishAction string // FORBIDDEN (Drive) — removed in step 2; created | updated | skipped | renamed | ""
	Status        string
	Error         string
	DuplicateOf   string
	// Renditions lists the generated technical variants for this asset.
	// Empty for transformers that have not been updated to the rendition
	// contract; callers must treat nil/empty as "only the canonical
	// LocalPath/Filename/FileHash are available".
	Renditions []RenditionOutput
}

// RenditionOutput describes a single generated technical variant of an asset.
type RenditionOutput struct {
	Kind       RenditionKind
	LocalPath  string
	Filename   string
	FileHash   string
	SizeBytes  int64
	MimeType   string
	Width      int
	Height     int
	FPS        float64
	Bitrate    int64
	Container  string
	Codec      string
	ColorSpace string
}

// ── Backward-compatibility aliases (PR-MEDIATRANSFORMER-RENAME step 1) ──
//
// These aliases are the minimal-blast-radius bridge that lets the
// ~50 existing callers in `internal/application/{youtube,clips,
// artlist,voiceover}` and `internal/api/assets/clips` continue to
// compile without churn while the new `MediaTransformer.Transform`
// contract rolls out. The aliases point to the canonical renamed
// types so a single struct serves both names:
//
//   - `ProcessInput` ↔ `TransformSpec`
//   - `ProcessResult` ↔ `RenditionSet`
//
// The aliases are REMOVED in step 2 (PR-MEDIATRANSFORMER-RENAME step 2)
// when:
//   (a) the forbidden fields are deleted from RenditionSet, and
//   (b) all legacy callers migrate to the new
//       `MediaTransformer.Transform` method signature.
//
// The legacy `Processor` interface (below) is also removed in step 2.

// ProcessInput is the backward-compat alias for TransformSpec.
// Deprecated: use TransformSpec directly. The alias is removed in
// PR-MEDIATRANSFORMER-RENAME step 2.
type ProcessInput = TransformSpec

// ProcessResult is the backward-compat alias for RenditionSet.
// Deprecated: use RenditionSet directly. The alias is removed in
// PR-MEDIATRANSFORMER-RENAME step 2.
type ProcessResult = RenditionSet

// Processor is the legacy god-service interface, retained (NOT removed)
// for backward compatibility with the ~50 existing callers in
// `internal/application/{youtube,clips,artlist,voiceover}` and
// `internal/api/assets/clips`.
//
// Deprecated: use MediaTransformer instead. The Process method
// signature stays the same so existing implementations (which adapt
// the new MediaTransformer.Transform internally via the
// `transformToProcessInput` helper) continue to satisfy both
// interfaces. The Processor interface is REMOVED in
// PR-MEDIATRANSFORMER-RENAME step 2.
type Processor interface {
	// Process downloads, processes, and uploads an asset.
	Process(ctx context.Context, input *ProcessInput) (*ProcessResult, error)
}
