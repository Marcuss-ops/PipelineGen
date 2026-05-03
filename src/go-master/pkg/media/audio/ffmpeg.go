// Package audio provides audio processing utilities using FFmpeg.
//
// STATUS: ACTIVE - This package is actively used by voiceover service.
package audio

import (
	"context"
	"fmt"
	"time"

	"velox/go-master/pkg/executil"
)

func RemoveSilence(ctx context.Context, ffmpegPath, input, output string) error {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	args := []string{
		"-y",
		"-i", input,
		"-af", "silenceremove=start_periods=1:start_threshold=-45dB:start_silence=0.25:stop_periods=-1:stop_threshold=-45dB:stop_silence=0.35",
		"-c:a", "libmp3lame",
		"-q:a", "2",
		output,
	}

	_, err := executil.Run(ctx, ffmpegPath, args, executil.Options{
		Timeout:        10 * time.Minute,
		CombinedOutput: true,
	})
	return err
}

func ProbeDuration(ctx context.Context, ffmpegPath, input string) (float64, error) {
	if ffmpegPath == "" {
		ffmpegPath = "ffprobe"
	}

	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	}

	result, err := executil.Run(ctx, ffmpegPath, args, executil.Options{
		Timeout:        30 * time.Second,
		CombinedOutput: true,
	})
	if err != nil {
		return 0, err
	}

	var duration float64
	fmt.Sscanf(result.Output, "%f", &duration)
	return duration, nil
}
