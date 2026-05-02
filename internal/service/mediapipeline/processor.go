package mediapipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"velox/go-master/pkg/media/ffmpeg"
	"velox/go-master/pkg/pathutil"
)

func (s *Service) process(ctx context.Context, item *WorkItem, spec ProcessingSpec) error {
	if s.ffmpegProcessor == nil {
		return fmt.Errorf("processor not configured")
	}

	if spec.Mode == ProcessingNone {
		item.ProcessedPath = item.LocalPath
		return nil
	}

	outputDir := s.processOutputDir
	os.MkdirAll(outputDir, 0755)

	outputPath := filepath.Join(outputDir, pathutil.Slug(item.Name)+"_processed.mp4")

	if spec.Mode == ProcessingNormalize {
		return s.processWithNormalize(ctx, item, spec, outputPath)
	}

	if spec.Mode == ProcessingCustom {
		return s.processWithCustom(ctx, item, spec, outputPath)
	}

	return fmt.Errorf("unknown processing mode: %s", spec.Mode)
}

func (s *Service) processWithNormalize(ctx context.Context, item *WorkItem, spec ProcessingSpec, outputPath string) error {
	opts := ffmpeg.NormalizeOptions{
		Duration:        spec.Duration,
		DisableDuration: spec.DisableDuration,
		KeepAudio:       spec.KeepAudio,
		Width:           spec.Width,
		Height:          spec.Height,
		FPS:             spec.FPS,
		Codec:           spec.Codec,
		Preset:          spec.Preset,
		CRF:             spec.CRF,
	}

	if err := s.ffmpegProcessor.Normalize(ctx, item.LocalPath, outputPath, opts); err != nil {
		return fmt.Errorf("failed to normalize: %w", err)
	}

	item.ProcessedPath = outputPath
	return nil
}

func (s *Service) processWithCustom(ctx context.Context, item *WorkItem, spec ProcessingSpec, outputPath string) error {
	if spec.Normalize {
		return s.processWithNormalize(ctx, item, spec, outputPath)
	}

	item.ProcessedPath = item.LocalPath
	return nil
}

func (s *Service) mergeItems(ctx context.Context, items []*WorkItem, outputPath string) error {
	if s.ffmpegProcessor == nil {
		return fmt.Errorf("processor not configured")
	}

	var inputPaths []string
	for _, item := range items {
		if item.ProcessedPath != "" {
			inputPaths = append(inputPaths, item.ProcessedPath)
		} else if item.LocalPath != "" {
			inputPaths = append(inputPaths, item.LocalPath)
		}
	}

	if len(inputPaths) == 0 {
		return fmt.Errorf("no valid input paths to merge")
	}

	if err := s.ffmpegProcessor.MergeInputs(ctx, inputPaths, outputPath); err != nil {
		return fmt.Errorf("failed to merge inputs: %w", err)
	}

	return nil
}
