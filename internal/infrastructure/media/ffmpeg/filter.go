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
