package ffmpeg

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// CanonicalClipFilter returns the canonical FFmpeg video filter chain
// used for every materialized clip. It scales and pads the input to the
// canonical resolution, normalizes the frame rate, and resets timestamps
// to ensure monotonic PTS.
func CanonicalClipFilter(profile config.VideoConfig) string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,setpts=PTS-STARTPTS",
		profile.Width, profile.Height, profile.Width, profile.Height, profile.FPS,
	)
}

// CanonicalClipFilterTrim returns the canonical video filter chain WITHOUT
// the final setpts reset. Use it when the input is already bounded by a
// trim, -ss/-to, or other cut operation and the output timestamps should
// reflect the trimmed segment rather than being reset to zero. This
// avoids the interaction where setpts=PTS-STARTPTS makes ffmpeg ignore
// the duration bound.
func CanonicalClipFilterTrim(profile config.VideoConfig) string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d",
		profile.Width, profile.Height, profile.Width, profile.Height, profile.FPS,
	)
}
