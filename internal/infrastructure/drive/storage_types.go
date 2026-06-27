// Package drive provides the destination/upload facade that re-exports
// canonical domain types (SourceType, MediaType from internal/domain/asset)
// and destination/upload types so legacy call sites continue to compile.
//
// Historical context: during PR-Phase 7, types were migrated to their
// canonical homes (internal/domain/asset for enums + resolver, internal/
// infrastructure/drive for upload logic). This package was re-created as
// a STOPGAP to keep existing call sites compiling while follow-up
// migrations landed.
//
// Canonical locations (current):
//
//   - SourceType / MediaType  → internal/domain/asset (re-exported here)
//   - AssetDestinationRequest → internal/domain/asset.ResolveRequest
//   - Drive upload logic      → internal/infrastructure/drive.Uploader
//
// The type aliases below (`storage.SourceImage`, `storage.MediaTypeImage`,
// `storage.AssetDestinationRequest`, etc.) resolve to this package so
// existing call sites compile without import rewrites.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// â”€â”€ Enum types â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
// Re-exported as type aliases so `storage.SourceType` and
// `storage.MediaType` literally are `asset.SourceType` / `asset.MediaType`;
// downstream code that switches on these (e.g. media/indexing, fullimages)
// keeps working without import rewrites.
type (
	SourceType = asset.SourceType
	MediaType  = asset.MediaType
)

// â”€â”€ Source constants (re-exported from canonical models) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
const (
	SourceStock       = asset.SourceStock
	SourceArtlist     = asset.SourceArtlist
	SourceYoutubeClip = asset.SourceYoutubeClip
	SourceClipDrive   = asset.SourceClipDrive
	SourceImage       = asset.SourceImage
	SourceGenerated   = asset.SourceGenerated

	// SourceSoundEffect lives here (NOT in models) because it is an
	// audio-extraction artifact of the image/video pipeline rather than a
	// real production source. Once it stabilises, promote it to
	// asset.SourceType.
	SourceSoundEffect SourceType = "sound_effect"
)

// â”€â”€ MediaType constants (canonical re-exports + image-pipeline locals)
const (
	MediaTypeStock    = asset.MediaTypeStock
	MediaTypeClip     = asset.MediaTypeClip
	MediaTypeImage    = asset.MediaTypeImage
	MediaTypeAudio    = asset.MediaTypeAudio
	MediaTypeDocument = asset.MediaTypeDocument

	// MediaTypeImageVideo is for image-pipeline-produced video files
	// (FullImages Ken Burns, Google Vids generations, NVIDIA animate).
	// Distinguishes from MediaTypeClip (the upstream video ingest path).
	MediaTypeImageVideo MediaType = "image_video"

	// MediaTypeSoundEffect â€” proxies the Source* counterpart above for
	// audio clips extracted from generated videos.
	MediaTypeSoundEffect MediaType = "sound_effect"
)

// â”€â”€ AssetDestinationRequest â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
//
// Mirrors the request shape consumed by the image/audio upload paths.
// Fields map closely to the legacy destinations.AssetDestinationRequest
// pre-Phase-7 struct, kept so google_drive_upload.go and friends compile.
//
// Canonical migration target: internal/domain/asset.DestinationRequest
// (deferred â€” the 13 callers' field-name contract is too widespread to
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
// Used by DestinationResolver.Resolve. Distinct from asset.ResolveResult
// so consumers can keep the JSON shape stable while the canonical
// destination package continues to evolve.
type Destination struct {
	FolderID    string `json:"folder_id,omitempty"`
	LocalPath   string `json:"local_path,omitempty"`
	RemotePath  string `json:"remote_path,omitempty"`
	WebViewLink string `json:"web_view_link,omitempty"`
}
