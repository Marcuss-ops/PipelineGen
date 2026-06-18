package mediaasset

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/pkg/fileutil"
)

// processStep normalizes/processes the video if needed.
func (p *Processor) processStep(ctx context.Context, input AssetInput, rawPath, processedPath string) (string, error) {
	shouldNormalize := input.Normalize == nil || *input.Normalize

	// If normalization is not requested, just move the file
	if !shouldNormalize {
		p.log.Info("skipping normalization as requested, moving raw to processed path", zap.String("id", input.ID))
		return p.moveRawToProcessed(rawPath, processedPath)
	}

	// Nil guard for ffmpeg
	if p.ffmpeg == nil {
		p.log.Warn("ffmpeg is nil, skipping normalization, moving raw to processed path", zap.String("id", input.ID))
		return p.moveRawToProcessed(rawPath, processedPath)
	}

	// ZERO-COPY OPTIMIZATION:
	// Always probe the downloaded file to check if it already matches target specs.
	// If so, skip the expensive re-encode entirely.
	info, err := p.ffmpeg.Probe(ctx, rawPath)
	if err == nil && info != nil {
		target := p.videoCfg
		// Check if properties match (with some tolerance for FPS)
		fpsMatch := info.FPS >= float64(target.FPS)-0.1 && info.FPS <= float64(target.FPS)+0.1
		resMatch := info.Width == target.Width && info.Height == target.Height

		if resMatch && fpsMatch {
			p.log.Info("Zero-Copy Optimization: properties match, skipping normalization",
				zap.String("id", input.ID),
				zap.Int("width", info.Width),
				zap.Int("height", info.Height),
				zap.Float64("fps", info.FPS))
			return p.moveRawToProcessed(rawPath, processedPath)
		}
		p.log.Info("properties do not match target, proceeding with normalization",
			zap.String("id", input.ID),
			zap.Int("width", info.Width),
			zap.Int("height", info.Height),
			zap.Float64("fps", info.FPS),
			zap.Int("target_width", target.Width),
			zap.Int("target_height", target.Height),
			zap.Int("target_fps", target.FPS))
	} else if err != nil {
		p.log.Warn("failed to probe file for zero-copy optimization", zap.Error(err))
	}

	opts := p.videoCfg
	opts.KeepAudio = input.KeepAudio
	opts.DisableDuration = input.DisableDuration
	if input.Duration > 0 {
		opts.Duration = input.Duration
	}

	p.log.Info("processing video", zap.String("id", input.ID), zap.String("output", processedPath), zap.Bool("disable_duration", opts.DisableDuration), zap.Int("duration", opts.Duration))
	if err := p.ffmpeg.Normalize(ctx, rawPath, processedPath, opts); err != nil {
		return "", err
	}

	return processedPath, nil
}

func (p *Processor) moveRawToProcessed(rawPath, processedPath string) (string, error) {
	if err := os.Rename(rawPath, processedPath); err != nil {
		// If rename fails (cross-device), try copy
		p.log.Warn("rename failed, attempting copy", zap.Error(err))
		if err := fileutil.CopyFile(rawPath, processedPath); err != nil {
			return "", fmt.Errorf("failed to move raw file to processed path: %w", err)
		}
	}
	return processedPath, nil
}
