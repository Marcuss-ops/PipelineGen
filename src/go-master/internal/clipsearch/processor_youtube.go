package clipsearch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// NormalizeClipSegmentWithAudio builds a YouTube clip keeping source audio.
// This is intentionally different from Artlist normalization (fixed 7s no-audio).
func (p *ClipProcessor) NormalizeClipSegmentWithAudio(ctx context.Context, rawPath string, startSec, durationSec float64) (string, error) {
	if durationSec <= 0 {
		durationSec = 25
	}
	if durationSec < 6 {
		durationSec = 6
	}
	if durationSec > 90 {
		durationSec = 90
	}
	if startSec < 0 {
		startSec = 0
	}

	out := strings.TrimSuffix(rawPath, filepath.Ext(rawPath)) + "_yt_moment_1080p.mp4"
	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.2f", startSec),
		"-i", rawPath,
		"-t", fmt.Sprintf("%.2f", durationSec),
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,format=yuv420p",
		"-c:v", "libx264",
		"-profile:v", "high",
		"-level", "4.1",
		"-pix_fmt", "yuv420p",
		"-preset", "fast",
		"-crf", "22",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "48000",
		"-ac", "2",
		"-movflags", "+faststart",
		out,
	}
	if err := p.runFFmpeg(ctx, args); err != nil {
		// Fallback only if the source has no audio stream.
		noAudioArgs := []string{
			"-y",
			"-ss", fmt.Sprintf("%.2f", startSec),
			"-i", rawPath,
			"-t", fmt.Sprintf("%.2f", durationSec),
			"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,format=yuv420p",
			"-c:v", "libx264",
			"-profile:v", "high",
			"-level", "4.1",
			"-pix_fmt", "yuv420p",
			"-preset", "fast",
			"-crf", "22",
			"-an",
			"-movflags", "+faststart",
			out,
		}
		if retryErr := p.runFFmpeg(ctx, noAudioArgs); retryErr != nil {
			return "", fmt.Errorf("ffmpeg normalize youtube segment failed: %w", err)
		}
	}
	if err := p.VerifyCompatibleMP4(ctx, out); err != nil {
		return "", fmt.Errorf("normalized youtube clip incompatible: %w", err)
	}
	return out, nil
}
