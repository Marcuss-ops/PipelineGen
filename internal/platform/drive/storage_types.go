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
//   - SourceType / MediaType → internal/domain/asset (re-exported here)
//   - Drive upload logic     → internal/infrastructure/drive.Uploader
//
// The type aliases below (`storage.SourceImage`, `storage.MediaTypeImage`,
// etc.) resolve to this package so existing call sites compile without
// import rewrites.
package drive

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Re-exported as type aliases so `storage.SourceType` and
// `storage.MediaType` literally are `asset.SourceType` / `asset.MediaType`;
// downstream code that switches on these (e.g. media/indexing, fullimages)
// keeps working without import rewrites.
type (
	SourceType = asset.SourceType
	MediaType  = asset.MediaType
)

// Source constants (re-exported from canonical models)
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

// MediaType constants (canonical re-exports + image-pipeline locals)
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

	// MediaTypeSoundEffect — proxies the Source* counterpart above for
	// audio clips extracted from generated videos.
	MediaTypeSoundEffect MediaType = "sound_effect"
)

// ResolvedDest is what the legacy Resolver.Resolve returned: relative +
// absolute local paths. It is retained for any remaining callers that
// still reference drive.ResolvedDest; new code should use the typed
// delivery.PublishRequest / PublishResult surfaces instead.
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
