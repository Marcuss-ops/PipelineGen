package asset

// MediaType classifies the content type of an asset (stock footage, video
// clip, image, audio/voiceover, document, generated video, sound effect).
//
// History (Wave-14 cut-over, Jun 2026):
//
//   - Phase 1: declared locally in internal/domain/asset/asset_types.go as a
//     named string type.
//   - Phase 3 (Wave-15 follow-up): replaced by `type MediaType = media.MediaType`
//     alias to converge with internal/domain/media/media.go (where the
//     canonical const set lived). Callers did not notice because the alias
//     was transparent — `asset.MediaType`, `asset.MediaTypeClip`, etc. all
//     worked either way.
//   - Wave-14: internal/domain/media/ is deleted. This file now declares
//     MediaType natively and re-hosts the const set that previously lived
//     in media.media.go. The Phase 3 alias was the correct bridge while
//     media co-existed; now that media is gone, the alias itself is gone.
//
// Naming rationale: the type remains `MediaType` (so existing call sites
// `asset.MediaType(...)`, `asset.MediaType("clip")`, `*Asset{MediaType:
// asset.MediaTypeClip}` continue to compile unchanged).
type MediaType string

const (
	// MediaTypeStock refers to stock footage.
	MediaTypeStock MediaType = "stock"
	// MediaTypeClip refers to a video clip.
	MediaTypeClip MediaType = "clip"
	// MediaTypeImage refers to an image.
	MediaTypeImage MediaType = "image"
	// MediaTypeAudio refers to an audio file (voiceover).
	MediaTypeAudio MediaType = "audio"
	// MediaTypeDocument refers to a document (Google Doc).
	MediaTypeDocument MediaType = "document"
	// MediaTypeImageVideo is for generated video files.
	MediaTypeImageVideo MediaType = "image_video"
	// MediaTypeSoundEffect is for extracted sound effect audio clips.
	MediaTypeSoundEffect MediaType = "sound_effect"
)

// IsValid reports whether the MediaType matches a known constant.
func (m MediaType) IsValid() bool {
	switch m {
	case MediaTypeStock, MediaTypeClip, MediaTypeImage, MediaTypeAudio, MediaTypeDocument, MediaTypeImageVideo, MediaTypeSoundEffect:
		return true
	}
	return false
}
