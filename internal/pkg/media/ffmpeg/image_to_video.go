package ffmpeg

import (
	"context"
	"fmt"
	"time"

	"velox/go-master/internal/pkg/executil"
)

// ImageToVideoOptions configures how a still image is converted to a video.
type ImageToVideoOptions struct {
	Duration    int // Video duration in seconds (default 7)
	Width       int
	Height      int
	FPS         int
	Codec       string
	Preset      string
	CRF         int
	Zoom        bool // If true, apply a subtle zoom-in (no pan, just light zoom)
}

// ImageToVideo converts a still image to an MP4 video with the specified duration.
// The image is scaled to fit Width×Height with padding as needed.
func (p *Processor) ImageToVideo(ctx context.Context, inputImage, outputVideo string, opts ImageToVideoOptions) error {
	if opts.Duration <= 0 {
		opts.Duration = 7
	}
	if opts.Width <= 0 {
		opts.Width = 1920
	}
	if opts.Height <= 0 {
		opts.Height = 1080
	}
	if opts.FPS <= 0 {
		opts.FPS = 30
	}
	if opts.Codec == "" {
		opts.Codec = "libx264"
	}
	if opts.Preset == "" {
		opts.Preset = "medium"
	}
	if opts.CRF <= 0 {
		opts.CRF = 18
	}

	// Build filter: scale to fill, crop to exact dimensions
	var filter string
	if opts.Zoom {
		// Light zoom-in: subtle 5% zoom over the duration, no pan (no Ken Burns).
		// This gives a gentle sense of motion without the dynamic centering.
		filter = fmt.Sprintf(
			"scale=%d:%d:force_original_aspect_ratio=increase,"+
				"crop=%d:%d,setsar=1,"+
				"zoompan=z='min(zoom+0.0005,1.05)':d=%d:s=%dx%d",
			opts.Width*2, opts.Height*2, // oversize input for zoom room
			opts.Width, opts.Height,
			opts.FPS*opts.Duration,
			opts.Width, opts.Height,
		)
	} else {
		filter = fmt.Sprintf(
			"scale=%d:%d:force_original_aspect_ratio=decrease,"+
				"pad=%d:%d:(ow-iw)/2:(oh-ih)/2,"+
				"fps=%d",
			opts.Width, opts.Height,
			opts.Width, opts.Height,
			opts.FPS,
		)
	}

	args := []string{
		"-y", "-hide_banner", "-loglevel", "warning",
		"-loop", "1",
		"-i", inputImage,
		"-vf", filter,
		"-t", fmt.Sprintf("%d", opts.Duration),
		"-c:v", opts.Codec,
		"-preset", opts.Preset,
		"-crf", fmt.Sprintf("%d", opts.CRF),
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-an",
		outputVideo,
	}

	_, err := executil.Run(ctx, p.path, args, executil.Options{
		Timeout: time.Duration(opts.Duration+30) * time.Second,
	})
	return err
}
