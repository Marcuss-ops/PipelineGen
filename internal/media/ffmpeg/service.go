package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/pkg/video_spec"
)

// Service provides video rendering via ffmpeg.
type Service struct {
	cfg *config.Config
	log *zap.Logger
}

// NewService creates a new ffmpeg rendering service.
func NewService(cfg *config.Config, log *zap.Logger) *Service {
	return &Service{
		cfg: cfg,
		log: log,
	}
}

// RenderScene parses a video plan JSON and re-encodes the source video
// using ffmpeg with the target resolution/codec settings.
func (s *Service) RenderScene(ctx context.Context, planPath string, outputPath string) error {
	s.log.Info("using ffmpeg for video rendering",
		zap.String("plan", planPath),
		zap.String("output", outputPath))

	// Parse the plan JSON
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("failed to read video plan: %w", err)
	}

	var plan video_spec.MediaTimelinePlan
	if err := json.Unmarshal(planData, &plan); err != nil {
		return fmt.Errorf("failed to parse video plan: %w", err)
	}

	// Extract source video path from primary track's first segment
	if len(plan.Tracks) == 0 || len(plan.Tracks[0].Segments) == 0 {
		return fmt.Errorf("video plan has no video segments")
	}

	segment := plan.Tracks[0].Segments[0]
	inputPath := segment.Path

	// Build ffmpeg filter chain
	// We need: seek + trim + scale + fps
	startTime := segment.Start
	duration := segment.Duration
	v := s.cfg.Video.WithDefaults()
	width := plan.Output.Width
	height := plan.Output.Height
	fps := plan.Output.FPS
	videoCodec := plan.Output.VideoCodec
	audioCodec := plan.Output.AudioCodec
	crf := plan.Output.CRF

	if width == 0 {
		width = v.Width
	}
	if height == 0 {
		height = v.Height
	}
	if fps == 0 {
		fps = v.FPS
	}
	if crf == 0 {
		crf = v.CRF
	}
	if videoCodec == "" {
		videoCodec = v.Codec
	}
	if audioCodec == "" {
		audioCodec = v.AudioCodec
	}
	preset := normalizePresetForCodec(videoCodec, v.Preset)

	// Build ffmpeg args
	args := []string{
		"-ss", fmt.Sprintf("%.3f", startTime),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.3f", duration),
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d", width, height, width, height, fps),
		"-c:v", videoCodec,
		"-preset", preset,
		"-crf", fmt.Sprintf("%d", crf),
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-c:a", audioCodec,
		"-y",
		outputPath,
	}

	s.log.Debug("ffmpeg command",
		zap.Strings("args", args),
		zap.String("input", inputPath))

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		s.log.Debug("ffmpeg output", zap.String("stdout_stderr", string(output)))
	}
	if err != nil {
		return fmt.Errorf("ffmpeg render failed: %w", err)
	}

	s.log.Info("ffmpeg render successful", zap.String("output", outputPath))
	return nil
}

func normalizePresetForCodec(videoCodec, preset string) string {
	preset = strings.TrimSpace(preset)
	if preset == "" {
		return "veryfast"
	}

	normalizedCodec := strings.ToLower(strings.TrimSpace(videoCodec))
	if normalizedCodec == "" {
		return preset
	}

	if strings.Contains(normalizedCodec, "nvenc") {
		return preset
	}

	if len(preset) == 2 && preset[0] == 'p' && preset[1] >= '1' && preset[1] <= '7' {
		return "veryfast"
	}

	return preset
}
