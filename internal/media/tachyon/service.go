package tachyon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"go.uber.org/zap"

	"velox/go-master/internal/pkg/tachyon_spec"
)

// Service provides an interface to the TACHYON rendering engine,
// with a fallback to ffmpeg when Tachyon binary is not available.
type Service struct {
	binaryPath string
	log        *zap.Logger
}

// NewService creates a new TACHYON service.
func NewService(binaryPath string, log *zap.Logger) *Service {
	return &Service{
		binaryPath: binaryPath,
		log:        log,
	}
}

// RenderScene renders a TACHYON scene file.
// If the Tachyon binary is not available, it falls back to ffmpeg.
func (s *Service) RenderScene(ctx context.Context, planPath string, outputPath string) error {
	// Try Tachyon first
	if s.binaryPath != "" {
		if _, err := os.Stat(s.binaryPath); err == nil {
			s.log.Info("calling tachyon", zap.String("plan", planPath), zap.String("output", outputPath))

			cmd := exec.CommandContext(ctx, s.binaryPath, "--plan", planPath, "-o", outputPath)
			output, err := cmd.CombinedOutput()

			if len(output) > 0 {
				s.log.Debug("tachyon output", zap.String("stdout_stderr", string(output)))
			}

			if err != nil {
				s.log.Error("tachyon render failed, falling back to ffmpeg",
					zap.Error(err), zap.String("output", string(output)))
			} else {
				s.log.Info("tachyon render successful", zap.String("output", outputPath))
				return nil
			}
		} else {
			s.log.Info("tachyon binary not found, using ffmpeg fallback",
				zap.String("binary", s.binaryPath))
		}
	}

	// Fallback: parse plan JSON and use ffmpeg
	return s.renderWithFFmpegFallback(ctx, planPath, outputPath)
}

// renderWithFFmpegFallback parses the tachyon plan JSON and re-encodes the source video
// using ffmpeg with the target resolution/codec settings.
func (s *Service) renderWithFFmpegFallback(ctx context.Context, planPath, outputPath string) error {
	s.log.Info("using ffmpeg fallback for tachyon plan",
		zap.String("plan", planPath),
		zap.String("output", outputPath))

	// Parse the plan JSON
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("failed to read tachyon plan: %w", err)
	}

	var plan tachyon_spec.MediaTimelinePlan
	if err := json.Unmarshal(planData, &plan); err != nil {
		return fmt.Errorf("failed to parse tachyon plan: %w", err)
	}

	// Extract source video path from primary track's first segment
	if len(plan.Tracks) == 0 || len(plan.Tracks[0].Segments) == 0 {
		return fmt.Errorf("tachyon plan has no video segments")
	}

	segment := plan.Tracks[0].Segments[0]
	inputPath := segment.Path

	// Build ffmpeg filter chain
	// We need: seek + trim + scale + fps
	startTime := segment.Start
	duration := segment.Duration
	width := plan.Output.Width
	height := plan.Output.Height
	fps := plan.Output.FPS
	videoCodec := plan.Output.VideoCodec
	audioCodec := plan.Output.AudioCodec
	crf := plan.Output.CRF

	if width == 0 {
		width = 1920
	}
	if height == 0 {
		height = 1080
	}
	if fps == 0 {
		fps = 30
	}
	if crf == 0 {
		crf = 23
	}
	if videoCodec == "" {
		videoCodec = "libx264"
	}
	if audioCodec == "" {
		audioCodec = "aac"
	}

	// Build ffmpeg args
	args := []string{
		"-ss", fmt.Sprintf("%.3f", startTime),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.3f", duration),
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d", width, height, width, height, fps),
		"-c:v", videoCodec,
		"-crf", fmt.Sprintf("%d", crf),
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
		return fmt.Errorf("ffmpeg fallback render failed: %w", err)
	}

	s.log.Info("ffmpeg fallback render successful", zap.String("output", outputPath))
	return nil
}

// BuildCmd returns a command to build TACHYON if it's not already built.
func (s *Service) BuildCmd() *exec.Cmd {
	return exec.Command("scripts/build-linux.sh", "--debug")
}