package processor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// processStep normalizes/processes the video if needed.
func (p *Processor) processStep(ctx context.Context, input *asset.ProcessInput, rawPath, processedPath string) (string, error) {
	shouldNormalize := input.Normalize == nil || *input.Normalize

	// If normalization is not requested, just move the file.
	if !shouldNormalize {
		p.log.Info("skipping normalization as requested, moving raw to processed path", zap.String("id", input.ID))
		return p.moveRawToProcessed(rawPath, processedPath)
	}

	// Nil guard for ffmpeg.
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
		// Check if properties match (with some tolerance for FPS).
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
		// If rename fails (cross-device), try copy.
		p.log.Warn("rename failed, attempting copy", zap.Error(err))
		if err := fileutil.CopyFile(rawPath, processedPath); err != nil {
			return "", fmt.Errorf("failed to move raw file to processed path: %w", err)
		}
	}
	return processedPath, nil
}

// processRenditions preserves the raw source as an immutable master and
// generates mezzanine, proxy, thumbnail, and storyboard renditions under
// input.OutputDir. It returns the populated renditions in the canonical
// order: master, mezzanine, proxy, thumbnail, storyboard.
func (p *Processor) processRenditions(ctx context.Context, input *asset.ProcessInput, rawPath string) ([]asset.RenditionOutput, error) {
	baseName := textutil.SafeName(input.Name) + " " + input.ID
	baseDir := input.OutputDir

	// 1. Master: copy the raw source and make it read-only.
	masterDir := filepath.Join(baseDir, "master")
	masterExt := filepath.Ext(rawPath)
	if masterExt == "" {
		masterExt = ".mp4"
	}
	masterPath := filepath.Join(masterDir, baseName+masterExt)
	if err := os.MkdirAll(masterDir, 0o755); err != nil {
		return nil, fmt.Errorf("create master dir: %w", err)
	}
	if err := fileutil.CopyFile(rawPath, masterPath); err != nil {
		return nil, fmt.Errorf("copy master: %w", err)
	}
	if err := os.Chmod(masterPath, 0o444); err != nil {
		p.log.Warn("failed to make master read-only", zap.String("path", masterPath), zap.Error(err))
	}

	// 2. Mezzanine: normalize/process the master.
	mezzanineDir := filepath.Join(baseDir, "mezzanine")
	mezzaninePath := filepath.Join(mezzanineDir, baseName+".mp4")
	if err := os.MkdirAll(mezzanineDir, 0o755); err != nil {
		return nil, fmt.Errorf("create mezzanine dir: %w", err)
	}
	mezzaninePath, err := p.processStep(ctx, input, masterPath, mezzaninePath)
	if err != nil {
		return nil, fmt.Errorf("mezzanine processing failed: %w", err)
	}

	// 3. Proxy: 720p H.264 from mezzanine.
	proxyDir := filepath.Join(baseDir, "proxy")
	proxyPath := filepath.Join(proxyDir, baseName+".mp4")
	if err := os.MkdirAll(proxyDir, 0o755); err != nil {
		return nil, fmt.Errorf("create proxy dir: %w", err)
	}
	if err := p.ffmpeg.GenerateProxy(ctx, mezzaninePath, proxyPath); err != nil {
		p.log.Warn("proxy generation failed", zap.String("id", input.ID), zap.Error(err))
	}

	// 4. Thumbnail: center frame from mezzanine.
	thumbnailDir := filepath.Join(baseDir, "thumbnail")
	thumbnailPath := filepath.Join(thumbnailDir, baseName+".jpg")
	if err := os.MkdirAll(thumbnailDir, 0o755); err != nil {
		return nil, fmt.Errorf("create thumbnail dir: %w", err)
	}
	thumbnailTimestamp := 1.0
	if info, err := p.ffmpeg.Probe(ctx, mezzaninePath); err == nil && info.Duration > 0 {
		thumbnailTimestamp = info.Duration.Seconds() / 2
	}
	if err := p.ffmpeg.ExtractFrame(ctx, mezzaninePath, thumbnailPath, thumbnailTimestamp); err != nil {
		p.log.Warn("thumbnail generation failed", zap.String("id", input.ID), zap.Error(err))
	}

	// 5. Storyboard: tiled key frames from mezzanine.
	storyboardDir := filepath.Join(baseDir, "storyboard")
	storyboardPath := filepath.Join(storyboardDir, baseName+".jpg")
	if err := os.MkdirAll(storyboardDir, 0o755); err != nil {
		return nil, fmt.Errorf("create storyboard dir: %w", err)
	}
	if err := p.ffmpeg.GenerateStoryboard(ctx, mezzaninePath, storyboardPath, 10, 5, 5); err != nil {
		p.log.Warn("storyboard generation failed", zap.String("id", input.ID), zap.Error(err))
	}

	// Build rendition outputs.
	renditions := []asset.RenditionOutput{
		p.buildRenditionOutput(ctx, asset.RenditionKindMaster, masterPath),
		p.buildRenditionOutput(ctx, asset.RenditionKindMezzanine, mezzaninePath),
	}
	if fileExists(proxyPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, asset.RenditionKindProxy, proxyPath))
	}
	if fileExists(thumbnailPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, asset.RenditionKindThumbnail, thumbnailPath))
	}
	if fileExists(storyboardPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, asset.RenditionKindStoryboard, storyboardPath))
	}

	return renditions, nil
}

// buildRenditionOutput populates a RenditionOutput from a local file,
// probing it for technical metadata when possible.
func (p *Processor) buildRenditionOutput(ctx context.Context, kind asset.RenditionKind, path string) asset.RenditionOutput {
	out := asset.RenditionOutput{
		Kind:      kind,
		LocalPath: path,
		Filename:  filepath.Base(path),
	}
	if info, err := os.Stat(path); err == nil {
		out.SizeBytes = info.Size()
	}
	if hash, err := fileutil.HashFile(path, sha256.New()); err == nil {
		out.FileHash = hash
	}
	if info, err := p.ffmpeg.Probe(ctx, path); err == nil {
		out.Width = info.Width
		out.Height = info.Height
		out.FPS = info.FPS
		out.Bitrate = info.BitRate
		if kind != asset.RenditionKindThumbnail && kind != asset.RenditionKindStoryboard {
			out.Codec = info.VideoCodec
		}
	}
	out.MimeType = mimeTypeForPath(path)
	out.Container = filepath.Ext(path)
	if out.Container != "" {
		out.Container = out.Container[1:] // remove leading dot
	}
	return out
}

// mimeTypeForPath returns a best-effort MIME type based on the file extension.
func mimeTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	default:
		return "application/octet-stream"
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
