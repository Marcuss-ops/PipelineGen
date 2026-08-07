package ffmpeg

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// CanonicalClipFilter preserves the historical VideoConfig argument while
// delegating to the profile-only implementation.
func CanonicalClipFilter(profile config.VideoConfig) string {
	return CanonicalVideoProfileFilter(profile.CanonicalVideoProfile())
}

// CanonicalVideoProfileFilter returns the canonical FFmpeg video filter chain
// from the artifact profile only. It scales and pads the input to the
// canonical resolution, normalizes the frame rate, and resets timestamps.
func CanonicalVideoProfileFilter(profile config.CanonicalVideoProfile) string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,setpts=PTS-STARTPTS",
		profile.Width, profile.Height, profile.Width, profile.Height, profile.FPS,
	)
}

// CanonicalClipFilterTrim preserves the historical VideoConfig argument while
// delegating to the profile-only trim implementation.
func CanonicalClipFilterTrim(profile config.VideoConfig) string {
	return CanonicalVideoProfileFilterTrim(profile.CanonicalVideoProfile())
}

// CanonicalVideoProfileFilterTrim returns the canonical video filter chain
// without the final setpts reset.
func CanonicalVideoProfileFilterTrim(profile config.CanonicalVideoProfile) string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d",
		profile.Width, profile.Height, profile.Width, profile.Height, profile.FPS,
	)
}
