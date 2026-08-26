package processor

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	fileutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// processStep normalizes/processes the video if needed.
func (p *Processor) processStep(ctx context.Context, input *detail.ProcessInput, rawPath, processedPath string) (string, error) {
	// Normalize=false (reprocess contract fix, July 2026): honor the
	// flag — skip the ffmpeg normalize and promote the raw source to
	// the processed path (mux/copy only). The caller bypasses the
	// artifact cache when this flag is set, so a raw file is never
	// cached under the "normalize" key. The rendition layout path
	// forces the flag back on (the canonical master must always be
	// normalized).
	if input.Normalize != nil && !*input.Normalize {
		p.log.Info("normalization disabled by caller — promoting raw source to processed path", zap.String("id", input.ID))
		return p.moveRawToProcessed(rawPath, processedPath)
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

func capcacheKey(sourceSHA, operation string, params any, version string) capcache.Key {
	body, err := json.Marshal(params)
	if err != nil {
		body = []byte(`{}`)
	}
	return capcache.Key{SourceSHA256: sourceSHA, Operation: operation, ParametersJSON: string(body), ProcessorVersion: version}
}

func hashFileSHA256(path string) string {
	if path == "" {
		return ""
	}
	d, _, err := digest.SHA256File(path)
	if err != nil {
		return ""
	}
	return d
}

func (p *Processor) materializeCachedFile(ctx context.Context, key capcache.Key, destination string) (bool, string) {
	if p == nil || p.artifactCache == nil || key.SourceSHA256 == "" || destination == "" {
		return false, ""
	}
	var entry *capcache.Entry
	leaseID := ""
	var hit bool
	if claimer, supportsClaims := p.artifactCache.(capcache.ClaimStore); supportsClaims {
		claim, claimErr := claimer.Claim(ctx, key, 15*time.Minute, expectedWorkMS(key.Operation))
		if claimErr == nil {
			if claim.Acquired {
				return false, claim.LeaseID
			}
			entry, hit = claim.Entry, claim.Entry != nil
		} else {
			p.log.Warn("artifact cache claim failed; falling back to lookup", zap.Error(claimErr))
		}
	}
	if entry == nil && !hit {
		var lookupErr error
		entry, hit, lookupErr = p.artifactCache.Lookup(ctx, key, expectedWorkMS(key.Operation))
		if lookupErr != nil {
			p.log.Warn("artifact cache lookup failed; recomputing", zap.Error(lookupErr))
			return false, ""
		}
	}
	if !hit || entry == nil {
		return false, ""
	}
	reader, err := p.artifactCache.Open(ctx, entry)
	if err != nil {
		p.log.Warn("artifact cache open failed; invalidating entry", zap.Error(err))
		_ = p.artifactCache.Invalidate(ctx, key)
		return false, ""
	}
	defer reader.Close()
	if err := writeAtomicFromReader(destination, reader); err != nil {
		p.log.Warn("artifact cache materialization failed; invalidating entry", zap.Error(err))
		_ = p.artifactCache.Invalidate(ctx, key)
		return false, ""
	}
	return true, leaseID
}

func (p *Processor) storeCachedFile(ctx context.Context, key capcache.Key, leaseID, path, mime string) {
	if p == nil || p.artifactCache == nil || key.SourceSHA256 == "" || path == "" {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		p.releaseCachedClaim(ctx, key, leaseID, err.Error())
		return
	}
	defer file.Close()
	var storeErr error
	if leaseID != "" {
		if leaseStore, ok := p.artifactCache.(capcache.LeaseStore); ok {
			_, storeErr = leaseStore.StoreWithLease(ctx, key, leaseID, file, mime, expectedWorkMS(key.Operation))
		} else {
			_, storeErr = p.artifactCache.Store(ctx, key, file, mime, expectedWorkMS(key.Operation))
		}
	} else {
		_, storeErr = p.artifactCache.Store(ctx, key, file, mime, expectedWorkMS(key.Operation))
	}
	if storeErr != nil {
		p.releaseCachedClaim(ctx, key, leaseID, storeErr.Error())
		p.log.Warn("artifact cache store failed; generated artifact remains valid", zap.Error(storeErr))
	}
}

func (p *Processor) releaseCachedClaim(ctx context.Context, key capcache.Key, leaseID, reason string) {
	if leaseID == "" || p == nil || p.artifactCache == nil {
		return
	}
	if leaseStore, ok := p.artifactCache.(capcache.LeaseStore); ok {
		if err := leaseStore.ReleaseClaim(ctx, key, leaseID, reason); err != nil {
			p.log.Warn("artifact cache lease release failed", zap.Error(err))
		}
	} else {
		_ = p.artifactCache.Invalidate(ctx, key)
	}
}

func expectedWorkMS(operation string) int64 {
	switch operation {
	case "normalize":
		return 1000
	case "proxy":
		return 500
	case "thumbnail":
		return 100
	case "storyboard":
		return 750
	default:
		return 0
	}
}

func writeAtomicFromReader(destination string, reader io.Reader) error {
	if reader == nil {
		return fmt.Errorf("nil cached artifact reader")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".artifact-cache-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }
	defer cleanup()
	if _, err := io.Copy(tmp, reader); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destination)
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
//		{asset_id}__master.mp4    — H.264/AAC/yuv420p/24fps/1920x1080
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
//  1. The master IS the normalized output (H.264/AAC/yuv420p/24fps/1920x1080).
//     Pre-step-9 the master was a copy of the raw source — re-encoded only
//     if the caller passed a Normalize=false flag. Post-step-9 the master
//     always meets the canonical codec, matching the user spec "Il master
//     è H.264/AAC/yuv420p/24fps/1920x1080 unico per tutte le lingue".
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
func (p *Processor) processRenditions(ctx context.Context, input *detail.ProcessInput, rawPath string) ([]detail.RenditionOutput, error) {
	assetID := input.ID
	if assetID == "" {
		return nil, fmt.Errorf("processRenditions: input.ID is required (canonical asset_id segment)")
	}
	baseDir := input.OutputDir

	// 1. Master: normalize the raw source to the canonical
	// H.264/AAC/yuv420p/24fps/1920x1080 codec. Pre-step-9 the master
	// was a copy of the raw source (untouched); step 9 makes the
	// master IS the normalized output per user spec.
	masterDir := filepath.Join(baseDir, "master")
	masterPath := filepath.Join(masterDir, assetID+"__master.mp4")
	if err := os.MkdirAll(masterDir, 0o755); err != nil {
		return nil, fmt.Errorf("create master dir: %w", err)
	}
	// Use processStep (which already zero-copy-skips when source
	// matches target) — produces canonical codec without re-encoding
	// when the source is already H.264/AAC/yuv420p/24fps/1920x1080.
	// Normalize=false is NOT honored in the rendition layout: the
	// canonical master must always be normalized (step-9 spec), so
	// the flag is forced off for this call.
	masterInput := *input
	masterInput.Normalize = nil
	rawSHA := hashFileSHA256(rawPath)
	masterCached := false
	masterLeaseID := ""
	masterKey := capcache.Key{}
	if rawSHA != "" {
		masterKey = capcacheKey(rawSHA, "normalize", p.videoCfg, "media-normalize/v1")
		masterCached, masterLeaseID = p.materializeCachedFile(ctx, masterKey, masterPath)
	}
	if !masterCached {
		masterPath, err := p.processStep(ctx, &masterInput, rawPath, masterPath)
		if err != nil {
			p.releaseCachedClaim(ctx, masterKey, masterLeaseID, err.Error())
			return nil, fmt.Errorf("master normalization failed: %w", err)
		}
		if rawSHA != "" {
			p.storeCachedFile(ctx, masterKey, masterLeaseID, masterPath, "video/mp4")
		}
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
	masterSHA := hashFileSHA256(masterPath)
	proxyKey := capcache.Key{}
	if masterSHA != "" {
		proxyKey = capcacheKey(masterSHA, "proxy", map[string]any{"profile": p.videoCfg.Profile}, "media-proxy/v1")
	}
	proxyCached := false
	proxyLeaseID := ""
	if proxyKey.SourceSHA256 != "" {
		proxyCached, proxyLeaseID = p.materializeCachedFile(ctx, proxyKey, previewPath)
	}
	if !proxyCached {
		if err := p.ffmpeg.GenerateProxy(ctx, masterPath, previewPath); err != nil {
			p.releaseCachedClaim(ctx, proxyKey, proxyLeaseID, err.Error())
			return nil, fmt.Errorf("preview generation failed: %w", err)
		}
		if proxyKey.SourceSHA256 != "" {
			p.storeCachedFile(ctx, proxyKey, proxyLeaseID, previewPath, "video/mp4")
		}
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
	thumbnailKey := capcache.Key{}
	if masterSHA != "" {
		thumbnailKey = capcacheKey(masterSHA, "thumbnail", map[string]any{"timestamp_seconds": thumbnailTimestamp, "format": "jpg"}, "media-thumbnail/v1")
	}
	thumbnailCached := false
	thumbnailLeaseID := ""
	if thumbnailKey.SourceSHA256 != "" {
		thumbnailCached, thumbnailLeaseID = p.materializeCachedFile(ctx, thumbnailKey, thumbnailPath)
	}
	if !thumbnailCached {
		if err := p.ffmpeg.ExtractFrame(ctx, masterPath, thumbnailPath, thumbnailTimestamp); err != nil {
			p.releaseCachedClaim(ctx, thumbnailKey, thumbnailLeaseID, err.Error())
			p.log.Warn("thumbnail generation failed", zap.String("id", input.ID), zap.Error(err))
		} else if thumbnailKey.SourceSHA256 != "" {
			p.storeCachedFile(ctx, thumbnailKey, thumbnailLeaseID, thumbnailPath, "image/jpeg")
		}
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
	// The placeholder must report the CANONICAL frame rate (config profile,
	// 24/1), not a hardcoded 30 — the normalized master is encoded at the
	// resolved profile frame rate, so a divergent literal makes the manifest
	// lie about the asset's real frame rate.
	manifestFPSNum, manifestFPSDen := p.videoCfg.Profile.FrameRate()
	if manifestFPSNum <= 0 || manifestFPSDen <= 0 {
		manifestFPSNum, manifestFPSDen = 24, 1
	}
	manifestBody := fmt.Sprintf(`{"asset_id":%q,"codec":"h264","audio_codec":"aac","pixel_format":"yuv420p","resolution":"1920x1080","fps_num":%d,"fps_den":%d,"placeholder":true}`+"\n", assetID, manifestFPSNum, manifestFPSDen)
	if err := os.WriteFile(manifestPath, []byte(manifestBody), 0o644); err != nil {
		return nil, fmt.Errorf("write manifest placeholder: %w", err)
	}

	// Build rendition outputs in the canonical order. Note the
	// per-file `Filename` field carries the canonical
	// `{asset_id}__<role>.<ext>` name; callers thread this into the
	// Publisher per-file.
	renditions := []detail.RenditionOutput{
		p.buildRenditionOutput(ctx, detail.RenditionKindMaster, masterPath),
		p.buildRenditionOutput(ctx, detail.RenditionKindMezzanine, mezzaninePath),
	}
	if fileExists(previewPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, detail.RenditionKindProxy, previewPath))
	}
	if fileExists(thumbnailPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, detail.RenditionKindThumbnail, thumbnailPath))
	}
	if fileExists(storyboardPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, detail.RenditionKindStoryboard, storyboardPath))
	}
	if fileExists(manifestPath) {
		renditions = append(renditions, p.buildRenditionOutput(ctx, detail.RenditionKindManifest, manifestPath))
	}

	return renditions, nil
}

// buildRenditionOutput populates a RenditionOutput from a local file,
// probing it for technical metadata when possible.
func (p *Processor) buildRenditionOutput(ctx context.Context, kind detail.RenditionKind, path string) detail.RenditionOutput {
	out := detail.RenditionOutput{
		Kind:      kind,
		LocalPath: path,
		Filename:  filepath.Base(path),
	}
	if info, err := os.Stat(path); err == nil {
		out.SizeBytes = info.Size()
	}
	if hash, _, err := digest.SHA256File(path); err == nil {
		out.LegacyFileMD5 = hash
	}
	if info, err := p.ffmpeg.Probe(ctx, path); err == nil {
		out.Width = info.Width
		out.Height = info.Height
		out.FPS = info.FPS
		out.Bitrate = info.BitRate
		if kind != detail.RenditionKindThumbnail && kind != detail.RenditionKindStoryboard {
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
