package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
)

type FFmpegCommand struct {
	ffmpegPath string
	ffprobePath string
}

func NewFFmpegCommand(ffmpegPath, ffprobePath string) *FFmpegCommand {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	return &FFmpegCommand{
		ffmpegPath:  ffmpegPath,
		ffprobePath: ffprobePath,
	}
}

func (f *FFmpegCommand) ProcessClip(ctx context.Context, input ProcessClipInput) (*ProcessClipResult, error) {
	args := []string{
		"-i", input.InputPath,
		"-t", fmt.Sprintf("%d", input.Duration),
		"-vf", fmt.Sprintf("scale=%d:%d", input.Width, input.Height),
		"-r", fmt.Sprintf("%d", input.FPS),
		"-c:v", "libx264",
		"-c:a", "aac",
		"-y",
		input.OutputPath,
	}

	cmd := exec.CommandContext(ctx, f.ffmpegPath, args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg process failed: %w", err)
	}

	return &ProcessClipResult{
		OutputPath: input.OutputPath,
	}, nil
}

func (f *FFmpegCommand) Probe(ctx context.Context, path string) (*MediaInfo, error) {
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	}

	cmd := exec.CommandContext(ctx, f.ffprobePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	return parseProbeOutput(output)
}
