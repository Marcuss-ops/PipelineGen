package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/pkg/media/ffmpeg"
)

// renderChunk concatenates copied clips into a single output video and then
// normalizes the final chunk in one encode pass.
func (s *Service) renderChunk(ctx context.Context, clips []string, titles []string, outputPath string) error {
	if len(clips) == 0 {
		return fmt.Errorf("no clips to render")
	}
	videoCfg := s.cfg.Video.WithDefaults()
	concatPath := outputPath + ".concat.mp4"
	_ = os.Remove(concatPath)
	defer os.Remove(concatPath)

	if len(clips) == 1 {
		s.log.Info("single clip chunk normalize starting",
			zap.String("source_clip", clips[0]),
			zap.String("output_path", outputPath),
		)
		return s.ffmpegProc.Normalize(ctx, clips[0], outputPath, ffmpeg.NormalizeOptions{
			Width:            videoCfg.Width,
			Height:           videoCfg.Height,
			FPS:              videoCfg.FPS,
			Codec:            videoCfg.Codec,
			Preset:           videoCfg.Preset,
			CRF:              videoCfg.CRF,
			KeyframeInterval: videoCfg.KeyframeInterval,
			KeepAudio:        true,
		})
	}

	s.log.Info("ffmpeg chunk concat starting",
		zap.Int("clip_count", len(clips)),
		zap.String("concat_path", concatPath),
		zap.String("output_path", outputPath),
		zap.Strings("titles", titles),
	)

	if err := s.ffmpegProc.MergeInputs(ctx, clips, concatPath); err != nil {
		return fmt.Errorf("concat chunk: %w", err)
	}
	s.log.Info("ffmpeg chunk concat finished", zap.String("concat_path", concatPath))

	s.log.Info("ffmpeg chunk normalize starting",
		zap.String("input_path", concatPath),
		zap.String("output_path", outputPath),
	)
	if err := s.ffmpegProc.Normalize(ctx, concatPath, outputPath, ffmpeg.NormalizeOptions{
		Width:            videoCfg.Width,
		Height:           videoCfg.Height,
		FPS:              videoCfg.FPS,
		Codec:            videoCfg.Codec,
		Preset:           videoCfg.Preset,
		CRF:              videoCfg.CRF,
		KeyframeInterval: videoCfg.KeyframeInterval,
		KeepAudio:        true,
	}); err != nil {
		return fmt.Errorf("normalize chunk: %w", err)
	}
	s.log.Info("ffmpeg chunk normalize finished", zap.String("output_path", outputPath))
	return nil
}

// loadEffects scans the given directory for .mp4 overlay effect files.
func loadEffects(dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("effects dir is empty")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read effects dir %q: %w", dir, err)
	}
	var effects []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".mp4") {
			effects = append(effects, filepath.Join(dir, e.Name()))
		}
	}
	if len(effects) == 0 {
		return nil, fmt.Errorf("no .mp4 effect files found in %s", dir)
	}
	return effects, nil
}
