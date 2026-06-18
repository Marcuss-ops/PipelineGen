// Package storage is the legacy destination/upload facade that the team's
// PR-Phase 7 refactor removed without updating the 13 dangling call sites.
//
// Context: in PR-Phase 7 the team merged versions of this package into
// independent micro-modules (internal/upload/drive for the uploader,
// internal/media/models for the enums, internal/core/destination for the
// resolver). They shipped the moves without rewriting the consumer side,
// so `internal/media/storage` is referenced from 13 files but contains
// no Go files. The build fails with:
//
//	no required module provides package
//	github.com/Marcuss-ops/PipelineGen/internal/media/storage
//
// This package was re-created as a STOPGAP so the existing call sites
// compile while migrate-to-canonical follow-ups land. Long-term, every
// consumer should import:
//
//   - SourceType / MediaType  → internal/media/models (canonical enums)
//   - AssetDestinationRequest → internal/core/domain/asset (canonical request)
//   - Drive upload logic      → internal/upload/drive.Uploader
//
// For now, all existing `storage.SourceImage`, `storage.MediaTypeImage*`,
// `storage.AssetDestinationRequest`, `storage.Resolver`,
// `storage.NewResolver`, `storage.Store`, `storage.NewStore`, etc.
// resolve to this package so the 13 dangling sites compile.
package storage

import (
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// ── Enum types ────────────────────────────────────────────────────────
// Re-exported as type aliases so `storage.SourceType` and
// `storage.MediaType` literally are `models.SourceType` / `models.MediaType`;
// downstream code that switches on these (e.g. media/indexing, fullimages)
// keeps working without import rewrites.
type (
	SourceType = models.SourceType
	MediaType  = models.MediaType
)

// ── Source constants (re-exported from canonical models) ─────────────
const (
	SourceStock       = models.SourceStock
	SourceArtlist     = models.SourceArtlist
	SourceYoutubeClip = models.SourceYoutubeClip
	SourceClipDrive   = models.SourceClipDrive
	SourceImage       = models.SourceImage
	SourceGenerated   = models.SourceGenerated

	// SourceSoundEffect lives here (NOT in models) because it is an
	// audio-extraction artifact of the image/video pipeline rather than a
	// real production source. Once it stabilises, promote it to
	// models.SourceType.
	SourceSoundEffect SourceType = "sound_effect"
)

// ── MediaType constants (canonical re-exports + image-pipeline locals)
const (
	MediaTypeStock    = models.MediaTypeStock
	MediaTypeClip     = models.MediaTypeClip
	MediaTypeImage    = models.MediaTypeImage
	MediaTypeAudio    = models.MediaTypeAudio
	MediaTypeDocument = models.MediaTypeDocument

	// MediaTypeImageVideo is for image-pipeline-produced video files
	// (FullImages Ken Burns, Google Vids generations, NVIDIA animate).
	// Distinguishes from MediaTypeClip (the upstream video ingest path).
	MediaTypeImageVideo MediaType = "image_video"

	// MediaTypeSoundEffect — proxies the Source* counterpart above for
	// audio clips extracted from generated videos.
	MediaTypeSoundEffect MediaType = "sound_effect"
)

// ── AssetDestinationRequest ───────────────────────────────────────────
//
// Mirrors the request shape consumed by the image/audio upload paths.
// Fields map closely to the legacy destinations.AssetDestinationRequest
// pre-Phase-7 struct, kept so google_drive_upload.go and friends compile.
//
// Canonical migration target: internal/core/domain/asset.DestinationRequest
// (deferred — the 13 callers' field-name contract is too widespread to
// reroute in a single follow-up without breaking other PRs).
type AssetDestinationRequest struct {
	Source            SourceType `json:"source,omitempty"`
	MediaType         MediaType  `json:"media_type,omitempty"`
	Style             string     `json:"style,omitempty"`
	Subject           string     `json:"subject,omitempty"`
	Hash              string     `json:"hash,omitempty"`
	Ext               string     `json:"ext,omitempty"`
	DriveRootOverride string     `json:"drive_root_override,omitempty"`

	// Group is used by the sound_effect_handlers path (named `name` there).
	Group string `json:"group,omitempty"`

	// GenerationID is used by the ingest_direct path.
	GenerationID string `json:"generation_id,omitempty"`
}

// ResolvedDest is what Resolver.Resolve returns: relative + absolute
// local paths. Mirrors the legacy `destinations.Destination` shape
// sans Drive fields (those are computed lazily by Store.EnsureDriveFolder).
type ResolvedDest struct {
	RelativePath string `json:"relative_path,omitempty"`
	LocalPath    string `json:"local_path,omitempty"`
}

// Destination is the lightweight, locally-resolved destination shape.
// Used by DestinationResolver.Resolve. Distinct from destination.ResolveResult
// so consumers can keep the JSON shape stable while the canonical
// destination package continues to evolve.
type Destination struct {
	FolderID    string `json:"folder_id,omitempty"`
	LocalPath   string `json:"local_path,omitempty"`
	RemotePath  string `json:"remote_path,omitempty"`
	WebViewLink string `json:"web_view_link,omitempty"`
}
