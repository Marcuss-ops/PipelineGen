// Package asset — canonical MediaType taxonomy.
package asset

// MediaType classifies the content type of an asset (stock footage, video
// clip, image, audio/voiceover, document, generated video, sound effect).
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
	// MediaTypeScript identifies script-to-asset catalog entries emitted
	// by script_assets-family providers.
	MediaTypeScript MediaType = "script"
)

// IsValid reports whether the MediaType matches a known constant.
func (m MediaType) IsValid() bool {
	switch m {
	case MediaTypeStock, MediaTypeClip, MediaTypeImage, MediaTypeAudio, MediaTypeDocument, MediaTypeImageVideo, MediaTypeSoundEffect, MediaTypeScript:
		return true
	}
	return false
}
