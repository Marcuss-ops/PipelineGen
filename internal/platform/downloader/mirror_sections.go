// internal/platform/downloader/mirror_sections.go —
//
// Section-via-mirror (October 2026 GPU cutover, plan A of
// YOUTUBE-EXTRACTION-PROBLEMS-2026-10-14.md §5).
//
// Problem: every --download-sections fetch re-downloads its window from the
// network (~130 MB per 600s block at ~9.3 MB/min) and re-pays a software
// AV1 decode + re-encode. Twenty-six contiguous blocks of one video fetched
// ~3.4 GB with wide overlaps, and every retry re-fetched the same bytes —
// while PIPELINEGEN_YTDLP_MIRROR_ROOT stayed empty because sectioned
// requests were explicitly excluded from mirroring.
//
// Fix: when mirroring is enabled, a sectioned request first ensures ONE
// full-source fetch per video (single-flighted, content-addressed, reused by
// every later window of the same video) and then cuts each window LOCALLY
// with ffmpeg (NVENC when available, libx264 fallback). The network is
// touched once per video instead of once per window, and retries never
// re-download anything.
//
// Fail-soft by design: any parse/fetch/cut failure returns ok=false and the
// caller falls through to the pre-existing per-section network path. A
// mirror optimization must never fail an extraction.
package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/process"
)

// EnvYTDLPMirrorSections gates the section-via-mirror path
// (PIPELINEGEN_YTDLP_MIRROR_SECTIONS). Default is ON whenever the mirror
// root is configured; the literal value "off" restores the legacy
// per-section network behaviour without disabling full-source mirroring.
const EnvYTDLPMirrorSections = "PIPELINEGEN_YTDLP_MIRROR_SECTIONS"

// mirrorSectionsEnabled reports whether sectioned requests may be served
// from a mirrored full source. Mirroring itself stays opt-in via
// PIPELINEGEN_YTDLP_MIRROR_ROOT (see ytdlp_mirror.go).
func mirrorSectionsEnabled() bool {
	root := strings.TrimSpace(os.Getenv(EnvYTDLPMirrorRoot))
	if root == "" || strings.EqualFold(root, "off") {
		return false
	}
	if v := strings.TrimSpace(os.Getenv(EnvYTDLPMirrorSections)); strings.EqualFold(v, "off") {
		return false
	}
	return true
}

// sectionRange is one parsed --download-sections window in absolute source
// seconds: cutting [start,end) from the mirrored full source publishes
// exactly the window the network path would have fetched.
type sectionRange struct {
	start, end float64
}

// parseSectionRange parses a yt-dlp --download-sections value of the form
// "*START-END" (the "*" prefix selects an absolute time range; see
// sectionDownloadString in the youtube usecase and SanitizeTimestamp for the
// allowed alphabet). Each bound accepts SS[.frac], MM:SS[.frac] or
// HH:MM:SS[.frac]. ok=false means "unparseable — use the network path".
func parseSectionRange(s string) (r sectionRange, ok bool) {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "*")
	sep := strings.LastIndex(t, "-")
	if sep < 0 {
		return sectionRange{}, false
	}
	start, ok1 := parseSectionTimestamp(t[:sep])
	end, ok2 := parseSectionTimestamp(t[sep+1:])
	if !ok1 || !ok2 || end <= start || start < 0 {
		return sectionRange{}, false
	}
	return sectionRange{start: start, end: end}, true
}

// parseSectionTimestamp parses one SS / MM:SS / HH:MM:SS bound.
func parseSectionTimestamp(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, false
	}
	var total float64
	for _, p := range parts {
		if p == "" {
			return 0, false
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || v < 0 || v >= 360000 {
			return 0, false
		}
		total = total*60 + v
	}
	return total, true
}

// fullSourceFormatSignature is the mirror-key signature for the canonical
// full-source fetch backing section cuts: canonical format argument plus the
// caller's format override and merge policy, so two requests that would
// produce different bytes never share a full-source entry.
func (d *YTDLPDownloader) fullSourceFormatSignature(formatOverride, mergeFormat string) string {
	parts := append([]string{}, d.cmdBuilder.FormatArg(true)...)
	parts = append(parts, formatOverride, mergeFormat)
	return strings.Join(parts, "\x1f")
}

// mirrorFullSourceEntry validates the cached full-source entry for key and
// returns its path. ok=false means "no usable entry; fetch from network".
func mirrorFullSourceEntry(root, key string) (entry string, ok bool) {
	metaPath := filepath.Join(root, key+".json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return "", false
	}
	var meta mirrorEntryMeta
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Ext == "" || meta.Size <= 0 {
		_ = os.Remove(metaPath)
		return "", false
	}
	entry = filepath.Join(root, key+"."+meta.Ext)
	info, err := os.Stat(entry)
	if err != nil || info.Size() != meta.Size {
		_ = os.Remove(entry)
		_ = os.Remove(metaPath)
		return "", false
	}
	return entry, true
}

// ffmpegPath resolves the ffmpeg binary for local section cuts.
func ffmpegPath() string {
	if p := strings.TrimSpace(os.Getenv("FFMPEG_PATH")); p != "" {
		return p
	}
	return "ffmpeg"
}

// ensureFullSourceCached returns the local path of the mirrored full source
// for videoID, fetching it once (single-flighted per key) on a miss. The
// fetch reuses the full-source network argv — canonical format, fragment
// concurrency, and aria2c acceleration — so a bulk fetch is not throttled
// like a section fetch. Failures are returned; the caller degrades to the
// per-section network path.
func (d *YTDLPDownloader) ensureFullSourceCached(ctx context.Context, videoURL, videoID, formatOverride, mergeFormat string, timeout time.Duration) (string, error) {
	root, ok := d.mirrorRoot()
	if !ok || videoID == "" {
		return "", fmt.Errorf("mirror unavailable")
	}
	key := mirrorKey(videoID, d.fullSourceFormatSignature(formatOverride, mergeFormat))
	lockMirrorKey(key).Lock()
	defer lockMirrorKey(key).Unlock()

	if entry, ok := mirrorFullSourceEntry(root, key); ok {
		return entry, nil
	}

	fetchTemplate := filepath.Join(root, ".fetch-"+key+".%(ext)s")
	buildArgs := func(playerClient string) []string {
		args := []string{"--no-playlist", "--continue"}
		args = append(args, d.cmdBuilder.BaseArgsForClient(videoURL, false, playerClient)...)
		args = append(args, d.cmdBuilder.FormatArg(true)...)
		args = append(args, d.cmdBuilder.ConcurrentFragmentsArg()...)
		args = d.addExternalDownloaderArgs(args)
		if formatOverride != "" {
			args = append(args, "-f", formatOverride)
		}
		mf := mergeFormat
		if mf == "" {
			mf = "mp4"
		}
		args = append(args, "--merge-output-format", mf)
		args = append(args, "-o", fetchTemplate)
		args = append(args, d.youtubeSleepIntervalArgs(videoURL)...)
		args = append(args, videoURL)
		return args
	}

	fullTimeout := timeout * 2
	if fullTimeout <= 0 {
		fullTimeout = 2 * defaultDownloadTimeout
	}
	_, err := d.runWithTransportRetry(ctx, videoURL, func() (*process.Result, error) {
		return d.runWithClientFallback(ctx, videoURL, process.Options{
			Timeout:        fullTimeout,
			CombinedOutput: true,
		}, buildArgs)
	})
	if err != nil {
		return "", fmt.Errorf("mirror full-source fetch: %w", err)
	}
	resolved, resolveErr := ResolveDownloadedSegmentPath(fetchTemplate)
	if resolveErr != nil {
		return "", fmt.Errorf("mirror full-source fetch succeeded but output missing: %w", resolveErr)
	}
	if verifyErr := d.verifier.VerifyFile(resolved); verifyErr != nil {
		return "", verifyErr
	}
	mirrorStore(root, key, videoID, resolved)
	entry, ok := mirrorFullSourceEntry(root, key)
	if !ok {
		return "", fmt.Errorf("mirror full-source store unverifiable")
	}
	return entry, nil
}

// cutSectionLocal cuts [r.start,r.end) from the mirrored full source into
// destPath (a .mp4 file) with a single GPU-accelerated re-encode when
// available, falling back to libx264 on CPU-only hosts. The re-encode keeps
// the exact-start guarantee the staging layer requires (the same guarantee
// --force-keyframes-at-cuts provided on the network path), but runs at NVENC
// speed on the already-local bytes instead of after a network fetch.
func (d *YTDLPDownloader) cutSectionLocal(ctx context.Context, srcPath, destPath string, r sectionRange) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("cut section mkdir: %w", err)
	}
	duration := r.end - r.start
	startArg := strconv.FormatFloat(r.start, 'f', 3, 64)
	durArg := strconv.FormatFloat(duration, 'f', 3, 64)
	ff := ffmpegPath()
	cutTimeout := 20 * time.Minute
	// Encoder policy is owned by the central resolver (percheck_video_encoder_policy):
	// the NVENC leg uses the canonical NVENC constant and the CPU fallback uses the
	// configured codec/preset/CRF instead of call-site literals.
	policy := config.VideoConfig{}.EncoderPolicy()

	nvencArgs := []string{
		"-hide_banner", "-y",
		"-ss", startArg,
		"-i", srcPath,
		"-t", durArg,
		"-c:v", config.EncoderNVENCH264, "-preset", "p1",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k",
		destPath,
	}
	if res, err := d.runner.Run(ctx, ff, nvencArgs, process.Options{Timeout: cutTimeout, CombinedOutput: true}); err == nil && res != nil {
		if verifyErr := d.verifier.VerifyFile(destPath); verifyErr == nil {
			return nil
		}
		_ = os.Remove(destPath)
		log.Printf("downloader: nvenc section cut failed verification, retrying libx264: %v", err)
	} else if err != nil {
		_ = os.Remove(destPath)
		log.Printf("downloader: nvenc section cut failed (%v), falling back to libx264", err)
	}

	cpuArgs := []string{
		"-hide_banner", "-y",
		"-ss", startArg,
		"-i", srcPath,
		"-t", durArg,
		"-c:v", policy.Codec, "-preset", policy.Preset, "-crf", strconv.Itoa(policy.CRF),
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k",
		destPath,
	}
	if _, err := d.runner.Run(ctx, ff, cpuArgs, process.Options{Timeout: cutTimeout, CombinedOutput: true}); err != nil {
		return fmt.Errorf("cut section libx264 fallback: %w", err)
	}
	if verifyErr := d.verifier.VerifyFile(destPath); verifyErr != nil {
		return verifyErr
	}
	return nil
}

// tryDownloadSectionsViaMirror serves every requested section from the
// mirrored full source. ok=false (with nil error) means "not applicable or
// failed — caller must use the network path"; a non-nil error is only
// returned when destPaths could not even be derived.
func (d *YTDLPDownloader) tryDownloadSectionsViaMirror(ctx context.Context, req *DownloadRequest, destPaths []string) (ok bool, err error) {
	if !mirrorSectionsEnabled() || req == nil || len(req.DownloadSections) == 0 || len(destPaths) != len(req.DownloadSections) {
		return false, nil
	}
	videoID := youtubeVideoID(req.URL)
	if videoID == "" {
		return false, nil
	}
	ranges := make([]sectionRange, len(req.DownloadSections))
	for i, s := range req.DownloadSections {
		r, ok := parseSectionRange(s)
		if !ok {
			return false, nil
		}
		ranges[i] = r
	}
	mergeFormat := req.MergeFormat
	if mergeFormat == "" {
		mergeFormat = "mp4"
	}
	src, err := d.ensureFullSourceCached(ctx, req.URL, videoID, req.Format, mergeFormat, d.effectiveDownloadTimeout(req.Timeout))
	if err != nil {
		log.Printf("downloader: mirror full-source unavailable for %s (%v); using per-section network path", videoID, err)
		return false, nil
	}
	for i, r := range ranges {
		if err := ctx.Err(); err != nil {
			return false, nil
		}
		if err := d.cutSectionLocal(ctx, src, destPaths[i], r); err != nil {
			log.Printf("downloader: local section cut failed for %s (%v); using per-section network path", videoID, err)
			return false, nil
		}
	}
	return true, nil
}

// sectionDestPaths derives the exact output files the network section path
// would produce, so a mirror hit is indistinguishable to callers.
func sectionDestPaths(req *DownloadRequest) []string {
	basePath := strings.TrimSuffix(req.OutputPath, filepath.Ext(req.OutputPath))
	out := make([]string, len(req.DownloadSections))
	for i := range req.DownloadSections {
		if len(req.DownloadSections) == 1 {
			out[i] = basePath + ".mp4"
		} else {
			out[i] = fmt.Sprintf("%s_%03d.mp4", basePath, i+1)
		}
	}
	return out
}
