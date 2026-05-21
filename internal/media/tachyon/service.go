package tachyon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/pkg/tachyon_spec"
)

// Service provides an interface to the TACHYON rendering engine.
// When the Tachyon binary is unavailable, it falls back to ffmpeg using
// the unified video config (codec, preset, resolution, FPS) to ensure
// consistent output across all rendering paths.
type Service struct {
	binaryPath string
	cfg        *config.Config
	log        *zap.Logger
}

// NewService creates a new TACHYON service. If binaryPath is empty,
// a default development path is used.
func NewService(binaryPath string, cfg *config.Config, log *zap.Logger) *Service {
	if binaryPath == "" {
		binaryPath = "src/tachyon/build/dev-linux/src/tachyon"
	}
	return &Service{
		binaryPath: binaryPath,
		cfg:        cfg,
		log:        log,
	}
}

// RenderScene renders a TACHYON scene file. It first attempts to use the
// Tachyon native renderer; if that fails or the binary is missing, it falls
// back to ffmpeg using the video settings from config for consistent encoding.
func (s *Service) RenderScene(ctx context.Context, planPath string, outputPath string) error {
	// Try Tachyon first
	if s.binaryPath != "" {
		if _, err := os.Stat(s.binaryPath); err == nil {
			s.log.Info("calling tachyon", zap.String("plan", planPath), zap.String("output", outputPath))

			// Prepare C++ scene file if planPath is JSON
			finalPath := planPath
			if strings.HasSuffix(planPath, ".json") {
				cppPath, err := s.convertJsonToCpp(planPath)
				if err != nil {
					s.log.Error("failed to convert JSON plan to C++, using ffmpeg fallback", zap.Error(err))
					return s.renderWithFFmpegFallback(ctx, planPath, outputPath)
				}
				finalPath = cppPath
				defer os.Remove(cppPath)
			}

			// New CLI command: tachyon render --cpp <path> --out <path>
			cmd := exec.CommandContext(ctx, s.binaryPath, "render", "--cpp", finalPath, "--out", outputPath)
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

func (s *Service) convertJsonToCpp(jsonPath string) (string, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return "", err
	}

	var plan tachyon_spec.MediaTimelinePlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return "", err
	}

	cppPath := jsonPath + ".cpp"
	var sb strings.Builder
	sb.WriteString("#include \"tachyon/scene/builder.h\"\n\n")
	sb.WriteString("extern \"C\" void build_scene(tachyon::SceneSpec& out) {\n")
	sb.WriteString("    using namespace tachyon::scene;\n")
	sb.WriteString("    out = SceneBuilder()\n")
	sb.WriteString(fmt.Sprintf("        .composition(\"main\", [](CompositionBuilder& c) {\n"))
	
	v := s.cfg.Video.WithDefaults()
	width := plan.Output.Width
	if width == 0 { width = v.Width }
	height := plan.Output.Height
	if height == 0 { height = v.Height }
	fps := plan.Output.FPS
	if fps == 0 { fps = v.FPS }
	
	sb.WriteString(fmt.Sprintf("            c.size(%d, %d).fps(%d).duration(%f);\n", 
		width, height, fps, s.getPlanDuration(plan)))
	
	for i, track := range plan.Tracks {
		for j, seg := range track.Segments {
			layerID := fmt.Sprintf("l_%d_%d", i, j)
			sb.WriteString(fmt.Sprintf("            c.layer(\"%s\", [](LayerBuilder& l) {\n", layerID))
			if seg.Transition != nil {
				sb.WriteString(fmt.Sprintf("                l.transition(\"%s\", %f);\n", 
					seg.Transition.Type, seg.Transition.Duration))
			}
			sb.WriteString(fmt.Sprintf("                l.video(\"%s\").start(%f).duration(%f).in(%f);\n", 
				seg.Path, seg.Start, seg.Duration, seg.TimelineStart))
			sb.WriteString("            });\n")
		}
	}
	
	// Add effects
	for _, effect := range plan.Effects {
		sb.WriteString(fmt.Sprintf("            c.effect(\"%s\", %f, %f", 
			effect.Type, effect.Start, effect.Duration))
		// Add parameters if present
		if len(effect.Parameters) > 0 {
			sb.WriteString(", [](EffectParams& p) {\n")
			for k, v := range effect.Parameters {
				sb.WriteString(fmt.Sprintf("                p.set(\"%s\", \"%s\");\n", k, v))
			}
			sb.WriteString("            }")
		}
		sb.WriteString(");\n")
	}
	
	sb.WriteString("        })\n")
	sb.WriteString("        .build();\n")
	sb.WriteString("}\n")

	if err := os.WriteFile(cppPath, []byte(sb.String()), 0644); err != nil {
		return "", err
	}
	return cppPath, nil
}

func (s *Service) getPlanDuration(plan tachyon_spec.MediaTimelinePlan) float64 {
	max := 0.0
	for _, track := range plan.Tracks {
		for _, seg := range track.Segments {
			if seg.TimelineStart+seg.Duration > max {
				max = seg.TimelineStart + seg.Duration
			}
		}
	}
	if max == 0 { return 1.0 }
	return max
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

	// Build ffmpeg args
	args := []string{
		"-ss", fmt.Sprintf("%.3f", startTime),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.3f", duration),
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d", width, height, width, height, fps),
		"-c:v", videoCodec,
		"-preset", v.Preset,
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
		return fmt.Errorf("ffmpeg fallback render failed: %w", err)
	}

	s.log.Info("ffmpeg fallback render successful", zap.String("output", outputPath))
	return nil
}

// BuildCmd returns a shell command to build the TACHYON binary.
func (s *Service) BuildCmd() *exec.Cmd {
	return exec.Command("scripts/build-linux.sh", "--debug")
}
