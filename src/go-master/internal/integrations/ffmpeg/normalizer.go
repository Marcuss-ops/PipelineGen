package ffmpeg

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"velox/go-master/internal/core/media"
)

type Normalizer struct {
	ffmpegPath string
}

func NewNormalizer(ffmpegPath string) *Normalizer {
	return &Normalizer{
		ffmpegPath: ffmpegPath,
	}
}

type NormalizeOptions struct {
	Width      int
	Height     int
	Framerate  int
	VideoCodec string
	AudioCodec string
	Bitrate    string
}

type NormalizedFile struct {
	Path        string
	Duration    time.Duration
	Width       int
	Height      int
	FileSize    int64
}

func (n *Normalizer) Normalize(ctx context.Context, input string, opts NormalizeOptions) (*NormalizedFile, error) {
	ext := filepath.Ext(input)
	output := input[:len(input)-len(ext)] + "_normalized.mp4"

	args := []string{"-i", input}

	if opts.Width > 0 && opts.Height > 0 {
		args = append(args, "-s", fmt.Sprintf("%dx%d", opts.Width, opts.Height))
	}
	if opts.Framerate > 0 {
		args = append(args, "-r", fmt.Sprintf("%d", opts.Framerate))
	}
	if opts.VideoCodec != "" {
		args = append(args, "-c:v", opts.VideoCodec)
	}
	if opts.AudioCodec != "" {
		args = append(args, "-c:a", opts.AudioCodec)
	}
	if opts.Bitrate != "" {
		args = append(args, "-b:v", opts.Bitrate)
	}

	args = append(args, output)

	cmd := exec.CommandContext(ctx, n.ffmpegPath, args...)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w", err)
	}

	return &NormalizedFile{
		Path: output,
	}, nil
}

func (n *Normalizer) ExtractSegment(ctx context.Context, input, start, end, output string) error {
	args := []string{
		"-i", input,
		"-ss", start,
		"-to", end,
		"-c", "copy",
		output,
	}

	cmd := exec.CommandContext(ctx, n.ffmpegPath, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	return nil
}

func (n *Normalizer) GetMetadata(ctx context.Context, input string) (*media.File, error) {
	args := []string{
		"-i", input,
		"-f", "null",
		"-",
	}

	cmd := exec.CommandContext(ctx, n.ffmpegPath, args...)
	output, _ := cmd.CombinedOutput()

	return parseFFmpegOutput(string(output))
}

func parseFFmpegOutput(output string) (*media.File, error) {
	return &media.File{}, nil
}
