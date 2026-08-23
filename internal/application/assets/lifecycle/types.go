package lifecycle

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type AssetKind string

const (
	AssetKindVideo    AssetKind = "video"
	AssetKindAudio    AssetKind = "audio"
	AssetKindImage    AssetKind = "image"
	AssetKindDocument AssetKind = "document"
)

type FinalizeInput struct {
	ID       string
	Name     string
	Filename string
	Kind     AssetKind
	Source   string
	SourceID string

	// Destination is the canonical Drive destination key. Required
	// post-F2.7 so the lifecycle service can route the upload
	// through the delivery.Publisher + DestinationRegistry belt (so
	// it gets RequireSubpath + ConflictPolicy enforcement). Each
	// caller sets the matching key (lifecycle.DestinationVoiceover
	// for voiceover callers, lifecycle.DestinationYouTubeClip for
	// youtube callers, etc.).
	Destination delivery.DestinationKey
	Group       string
	Subfolder   string
	// Subject is used by path builders that key off an asset ID
	// (YouTubeClipPath / ArtlistPath / StockPath / ImagePath).
	Subject string
	// ProjectID is used by VoiceoverPath / BookPath / ScriptPath
	// (project-level grouping).
	ProjectID string
	// Language is the BCP-47 tag consumed by VoiceoverPath /
	// ScriptPath (per-project-language folders).
	Language string
	// Style is consumed by ImagePath (image style folder).
	Style string

	LocalPath  string
	FolderID   string
	FolderPath string

	DriveLink     string
	DriveFileID   string
	DownloadLink  string
	LegacyFileMD5 string
	Metadata      string

	Duration     int
	RequireLocal bool
	RequireHash  bool
	RequireDrive bool
	VerifyDB     bool
}

type FinalizeResult struct {
	OK             bool
	Status         string
	DeliveryStatus asset.AssetPublishStatus
	LegacyFileMD5  string
	ContentHash    string
	DriveLink      string
	DriveFileID    string
	DownloadLink   string
	LocalPath      string
	Error          string
}

// UploadOnlyResult is the post-Drive-upload surface. Used by the
// canonical VOICEOVER 2-PHASE SPLIT (P0.7 Wave 21, June 2026) where
// destinationStage uploads via lifecycle.Service.UploadOnly without
// touching the DB, and finalizeStage separately writes the
// voiceovers + media_assets projection + outbox in a single tx.
//
// Layering note: this struct is intentionally a drive-surface subset
// of FinalizeResult (drops OK/Status/Error/LegacyFileMD5). The caller is
// responsible for marking the BatchItem as StatusUploaded on success
// (legacy constant) and routing any Drive upload failure through
// FailureUpload at the Stage-2 fail() contract.
type UploadOnlyResult struct {
	DriveLink    string
	DriveFileID  string
	DownloadLink string
}

// VoiceoverProjectionInput is the canonical media_assets projection
// row for the new 2-phase pipeline (P0.7 Wave 21, June 2026). The
// caller (voiceover.finalizeStage) builds this from a populated
// BatchItem (Stage 1 + Stage 2 have completed); lifecycle.Service
// then writes it through UpsertVoiceoverProjectionTx INSIDE the
// caller-owned tx, so the media_assets row commits atomically with
// the voiceovers row + the asset.index.requested outbox event.
//
// Layering note: this struct is **only** the media_assets projection
// surface — the canonical voiceovers row lives in
// internal/application/voiceover/persistence.VoiceoverRecord. The
// two structs are deliberately separate so a schema evolution to one
// table does NOT drift the other.
type VoiceoverProjectionInput struct {
	ID            string // primary key (shared with voiceovers.id)
	Source        string // always "voiceover" — caller sets it
	Name          string // = text_preview (≤100 chars)
	Filename      string // canonical file name (e.g. vo_en_*.mp3)
	FolderID      string
	FolderPath    string
	MediaType     string // "audio"
	LocalPath     string
	DriveFileID   string
	DriveLink     string
	DownloadLink  string
	LegacyFileMD5 string
	Language      string
	Status        string // "completed" on happy path
	Metadata      string // JSON envelope (mirrors voiceovers.metadata)
}

// Use assetop types for compatibility
type ExistingAssetQuery = assetop.ExistingAssetQuery
type AssetRecord = assetop.AssetRecord

// Finalizer is the canonical commit boundary used by the lifecycle.
type Finalizer interface {
	Finalize(context.Context, *artifacts.MediaRecord, artifacts.FinalizeOptions) (*artifacts.FinalizeResult, error)
}

// AssetRecordStore defines the interface for asset record persistence
type AssetRecordStore interface {
	Upsert(ctx context.Context, rec *artifacts.MediaRecord) error
	Get(ctx context.Context, id string) (*artifacts.MediaRecord, error)
	FindExisting(ctx context.Context, query ExistingAssetQuery) (*AssetRecord, error)
	ListWithDriveFileID(ctx context.Context, source string) ([]*AssetRecord, error)
	MarkDriveMissing(ctx context.Context, id string) error
	DeleteAssetRecord(ctx context.Context, id string) error
}
