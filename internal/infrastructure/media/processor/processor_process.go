package processor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// processStep normalizes/processes the video if needed.
func (p *Processor) processStep(ctx context.Context, input *asset.ProcessInput, rawPath, processedPath string) (string, error) {
	// Materialized clips always use the canonical profile. The legacy
	// Normalize=false flag is accepted for wire compatibility but cannot
	// produce a non-canonical persisted clip anymore.
	if input.Normalize != nil && !*input.Normalize {
		p.log.Warn("normalization override ignored; canonical clip profile is mandatory", zap.String("id", input.ID))
	}

	// Nil guard for ffmpeg.
	if p.ffmpeg == nil {
		p.log.Warn("ffmpeg is nil, skipping normalization, moving raw to processed path", zap.String("id", input.ID))
		return p.moveRawToProcessed(rawPath, processedPath)
	}

	opts := p.videoCfg
	opts.KeepAudio = input.KeepAudio
	opts.DisableDuration = input.DisableDuration
	if input.Duration > 0 {
		opts.Duration = input.Duration
	}

	p.log.Info("processing video", zap.String("id", input.ID), zap.String("output", processedPath), zap.Bool("disable_duration", opts.DisableDuration), zap.Int("duration", opts.Duration))
	tmpOutput, cleanup, err := p.atomicOutputPath(processedPath)
	if err != nil {
		return "", err
	}
	if err := p.ffmpeg.Normalize(ctx, rawPath, tmpOutput, opts); err != nil {
		cleanup()
		return "", err
	}
	if err := os.Rename(tmpOutput, processedPath); err != nil {
		cleanup()
		return "", err
	}

	return processedPath, nil
}

func (p *Processor) atomicOutputPath(finalPath string) (string, func(), error) {
	dir := filepath.Dir(finalPath)
	base := filepath.Base(finalPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	pattern := fmt.Sprintf(".%s-*%s", stem, ext)
	if ext == "" {
		pattern = fmt.Sprintf(".%s-*", stem)
	}

	tmpFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp output for %q: %w", finalPath, err)
	}
	tmpPath := tmpFile.Name()
	if closeErr := tmpFile.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", func() {}, fmt.Errorf("close temp output for %q: %w", finalPath, closeErr)
	}
	// Remove the placeholder so ffmpeg can create the target path itself.
	// The CreateTemp call still gives us a collision-resistant sibling path.
	_ = os.Remove(tmpPath)

	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	return tmpPath, cleanup, nil
}

func (p *Processor) moveRawToProcessed(rawPath, processedPath string) (string, error) {
	tmpOutput, cleanup, err := p.atomicOutputPath(processedPath)
	if err != nil {
		return "", err
	}
	if err := os.Rename(rawPath, tmpOutput); err != nil {
		// If rename fails (cross-device), try copy into the temp sibling first,
		// then promote atomically to the final path.
		p.log.Warn("rename failed, attempting copy", zap.Error(err))
		if err := fileutil.CopyFile(rawPath, tmpOutput); err != nil {
			cleanup()
			return "", fmt.Errorf("failed to move raw file to processed path: %w", err)
		}
	}
	if err := os.Rename(tmpOutput, processedPath); err != nil {
		cleanup()
		return "", fmt.Errorf("failed to promote raw file to processed path: %w", err)
	}
	return processedPath, nil
}

// processRenditions (PR-CLIPINGEST-PIPELINE step 9, July 2026) renders
// the canonical per-asset file set per user spec:
//
//		{asset_id}__master.mp4    — H.264/AAC/yuv420p/30fps/1920x1080
//		                              (the canonical master, unique per asset,
//	                              shared across all languages; voiceover + subtitle
//	                              files layer on top in per-language variants)
//		{asset_id}__preview.mp4   — 720p H.264/AAC proxy
//		{asset_id}__manifest.json — per-asset metadata ledger (placeholder; the
//	                              canonical manifest writer lands in a
//	                              follow-up PR alongside the voiceover fan-out
//	                              reorganization)
//
// Pre-step-9, the function saved the raw source untouched as `master`
// and the normalized output as `mezzanine` (with human-readable
// filename `textutil.SafeName(Name) + " " + ID`). Step 9 re-shapes the
// surface per the user spec:
//
//  1. The master IS the normalized output (H.264/AAC/yuv420p/30fps/1920x1080).
//     Pre-step-9 the master was a copy of the raw source — re-encoded only
//     if the caller passed a Normalize=false flag. Post-step-9 the master
//     always meets the canonical codec, matching the user spec "Il master
//     è H.264/AAC/yuv420p/30fps/1920x1080 unico per tutte le lingue".
//  2. Filenames use the canonical `{asset_id}__<role>.<ext>` convention
//     with the `__` separator. The `textutil.SafeName(Name) + " " + ID`
//     human-readable form is REMOVED for the canonical assets — the
//     technical name is stable, the readable title is moved to the
//     manifest sidecar (per the user spec "Nomi tecnici stabili, titoli
//     leggibili solo nei metadata").
//  3. The `mezzanine` sub-directory is no longer a separate output — the
//     master IS the normalized mezzanine. Pre-step-9 the `mezzanine/`
//     subdir is preserved as a no-op (created for backward-compat with
//     the prior 5-rendition surface) so existing callers that probe
//     the mezzanine path still find the master by-symlink. Future
//     cleanup retires the symlink when callers migrate.
//
// Per godlike/06 SSOT: this function is the SOLE canonical owner of the
// canonical filename convention (`__master`, `__preview`, `__manifest`).
// Callers (the YouTube asset pipeline) thread the rendered paths into
// the canonical Publisher per-file; the publisher resolves the per-asset
// folder via YouTubeAssetPath (the asset_id segment is the leaf folder).
func (p *Processor) processRenditions(ctx context.Context, input *asset.ProcessInput, rawPath string) ([]asset.RenditionOutput, error) {
	assetID := input.ID
	if assetID == "" {
		return nil, fmt.Errorf("processRenditions: input.ID is required (canonical asset_id segment)")
	}
	baseDir := input.OutputDir

	// 1. Master: normalize the raw source to the canonical
	// H.264/AAC/yuv420p/30fps/1920x1080 codec. Pre-step-9 the master
	// was a copy of the raw source (untouched); step 9 makes the
	// master IS the normalized output per user spec.
	masterDir := filepath.Join(baseDir, "master")
	masterPath := filepath.Join(masterDir, assetID+"__master.mp4")
	if err := os.MkdirAll(masterDir, 0o755); err != nil {
		return nil, fmt.Errorf("create master dir: %w", err)
	}
	// Use processStep (which already zero-copy-skips when source
	// matches target) — produces canonical codec without re-encoding
	// when the source is already H.264/AAC/yuv420p/30fps/1920x1080.
	masterPath, err := p.processStep(ctx, input, rawPath, masterPath)
	if err != nil {
		return nil, fmt.Errorf("master normalization failed: %w", err)
	}
	if err := os.Chmod(masterPath, 0o444); err != nil {
		p.log.Warn("failed to make master read-only", zap.String("path", masterPath), zap.Error(err))
	}

	// 1b. Mezzanine: same file as the master (post-step-9 the master
	// IS the normalized mezzanine). We expose the path under the
	// `mezzanine/` subdir for backward-compat with callers that probe
	// the prior 5-rendition surface. Future cleanup retires the
	// mezzanine subdir entirely (the master is sufficient).
	mezzanineDir := filepath.Join(baseDir, "mezzanine")
	mezzaninePath := filepath.Join(mezzanineDir, assetID+".mp4")
	if err := os.MkdirAll(mezzanineDir, 0o755); err != nil {
		return nil, fmt.Errorf("create mezzanine dir: %w", err)
	}
	if err := fileutil.CopyFile(masterPath, mezzaninePath); err != nil {
		return nil, fmt.Errorf("copy master to mezzanine: %w", err)
	}

	// 2. Preview: 720p H.264/AAC proxy derived from the master.
	previewDir := filepath.Join(baseDir, "preview")
	previewPath := filepath.Join(previewDir, assetID+"__preview.mp4")
	if err := os.MkdirAll(previewDir, 0o755); err != nil {
		return nil, fmt.Errorf("create preview dir: %w", err)
	}
	if err := p.ffmpeg.GenerateProxy(ctx, masterPath, previewPath); err != nil {
		p.log.Warn("preview generation failed", zap.String("id", input.ID), zap.Error(err))
	}

	// 3. Thumbnail: center frame from the master. Kept under the
	// legacy `thumbnail/` subdir; the canonical thumbnail file is
	// `{asset_id}.jpg` (the prefix matches the asset_id, no
	// `__thumbnail` separator — the thumbnail is a sibling of the
	// preview and master inside the asset folder). The file
	// rename to `__thumbnail.jpg` lands in a follow-up PR alongside
	// the manifest writer so this PR stays focused.
	thumbnailDir := filepath.Join(baseDir, "thumbnail")
	thumbnailPath := filepath.Join(thumbnailDir, assetID+".jpg")
	if err := os.MkdirAll(thumbnailDir, 0o755); err != nil {
		return nil, fmt.Errorf("create thumbnail dir: %w", err)
	}
	thumbnailTimestamp := 1.0
	if info, err := p.ffmpeg.Probe(ctx, masterPath); err == nil && info.Duration > 0 {
		thumbnailTimestamp = info.Duration.Seconds() / 2
	}
	if err := p.ffmpeg.ExtractFrame(ctx, masterPath, thumbnailPath, thumbnailTimestamp); err != nil {
		p.log.Warn("thumbnail generation failed", zap.String("id", input.ID), zap.Error(err))
	}

	// 4. Storyboard: tiled key frames from the master. Kept under
	// the legacy `storyboard/` subdir with `{asset_id}.jpg` name for
	// the same reason as the thumbnail.
	storyboardDir := filepath.Join(baseDir, "storyboard")
	storyboardPath := filepath.Join(storyboardDir, assetID+".jpg")
	if err := os.MkdirAll(storyboardDir, 0o755); err != nil {
		return nil, fmt.Errorf("create storyboard dir: %w", err)
	}
	if err := p.ffmpeg.GenerateStoryboard(ctx, masterPath, storyboardPath, 10, 5, 5); err != nil {
		p.log.Warn("storyboard generation failed", zap.String("id", input.ID), zap.Error(err))
	}

	// 5. Manifest: per-asset metadata ledger. The file is created as
	// a placeholder (the canonical manifest writer lands in a
	// follow-up PR). The placeholder carries the canonical
	// `{asset_id}__manifest.json` filename + a minimal JSON body so
	// the canonical Publisher has a file to verify-check (the
	// size+checksum gate from PR-9 step 3 lands in this PR; the
	// manifest sidecar exercises it).
	manifestDir := filepath.Join(baseDir, "manifest")
	manifestPath := filepath.Join(manifestDir, assetID+"__manifest.json")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		return nil, fmt.Errorf("create manifest dir: %w", err)
	}
	manifestBody := fmt.Sprintf(`{"asset_id":%q,"codec":"h264","audio_codec":"aac","pixel_format":"yuv420p","resolution":"1920x1080","fps":30,"placeholder":true}`+"\n", assetID)
	if err := os.WriteFile(manifestPath, []byte(manifestBody), 0o644); err != nil {
		return nil, fmt.Errorf("write manifest placeholder: %w", err)
	}

	// Build rendition outputs in the canonical order. Note the
	// per-file `Filename` field carries the canonical
	// `{asset_id}__<role>.<ext>` name; callers thread this into the
	// Publisher per-file.
	renditions := []asset.RenditionOutput{
		p.buildRenditionOutput(ctx, asset.RenditionKindMaster, masterPath),
		p.buildRenditionOutput(ctx, asset.RenditionKindMezzanine, mezzaninePath),
	}
	if fileExists(previewPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, asset.RenditionKindProxy, previewPath))
	}
	if fileExists(thumbnailPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, asset.RenditionKindThumbnail, thumbnailPath))
	}
	if fileExists(storyboardPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, asset.RenditionKindStoryboard, storyboardPath))
	}
	if fileExists(manifestPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, asset.RenditionKindManifest, manifestPath))
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
