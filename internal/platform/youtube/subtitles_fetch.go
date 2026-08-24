// Package youtube — subtitles_fetch.go is the download/cache-lookup
// leaf of the 5-file subtitle split. It owns ONLY the yt-dlp
// subprocess invocation + cached-VTT read; no parsing, no language
// normalization, no priority-chain logic.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The yt-dlp argv prefix lives in
//     internal/platform/ytdlp/cmd_builder.go::BaseArgs. This
//     leaf DELEGATES to a.cmdBuilder.BaseArgs so the canonical
//     --cookies / --js-runtime / --remote-components / --extractor-args
//     prefix is preserved verbatim (regression guard for the
//     n-challenge + age-restricted case that PR-SUBTITLES-BASEARGS-
//     MIGRATION closes).
//   - The priority chain (payload → DB → manual subs → auto subs
//     → Whisper) live elsewhere — the application-layer
//     orchestrator (text_track_resolver.go::AcquireSegmentText,
//     commit c0bae1612). This leaf does NOT decide fallback; the
//     facade's FetchSegmentSubtitles interprets the "no VTT
//     landed" outcome and signals the orchestrator via the
//     canonical (nil, nil) sentinel (subtitles_fallback.go).
//
// godlike/06 SSOT wiring (corrected Aug 2026): this adapter IS wired
// in the production composition root. build_bundles_domain_media.go
// constructs the concrete *ytinfra.SubtitleFetcherAdapter and wires it
// into (1) TextTrackResolver.Subtitles (acquisition priorities 3+4),
// (2) youtube.ServiceAdapterDeps.SubtitleFetcher, and (3)
// DomainBundle.SubtitleFetcher, which composition.go threads into the
// backfill AcquireService. The separate
// platformyoutube.YTDLPSubtitleAdapter
// (internal/platform/youtube/ytdlp_subtitles.go) is a distinct adapter
// scoped to the channel-monitor scheduler path (lifecycle_scheduler.go)
// — not the clip acquisition chain.
package youtube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// FetchFullVTT downloads the auto-generated transcript for videoURL
// via yt-dlp --write-auto-subs, returns the cached entries as
// []TimedEntry. If no transcript is available the function returns
// (nil, nil) without error so the facade can surface the canonical
// (nil, nil) fallback sentinel (the application-layer orchestrator
// then falls through to Whisper).
//
// PR-SUBTITLES-BASEARGS-MIGRATION (2026-07-06): delegate the
// canonical yt-dlp argv prefix to a.cmdBuilder.BaseArgs. Pre-PR
// this slice manually appended --no-warnings and bypassed the
// helper entirely, dropping --cookies (required for n-challenge +
// age-restricted videos), --js-runtime + --remote-components
// ejs:github (required for node-based signature resolution), and
// --extractor-args youtube:player_client=web,android (the f3f1ee90
// web-first policy). Mirror metadata.go: BaseArgs returns the
// prefix WITHOUT the URL, so the URL is appended at the end
// alongside the -o output template (yt-dlp accepts global options
// before OR after the positional URL).
func (a *SubtitleFetcherAdapter) FetchFullVTT(ctx context.Context, videoURL string) ([]TimedEntry, error) {
	if a.cacheDir == "" {
		return nil, fmt.Errorf("subtitles: cacheDir is required")
	}
	if videoURL == "" {
		return nil, fmt.Errorf("subtitles: videoURL is required")
	}
	id := extractIDFromURL(videoURL)
	cachedPath := filepath.Join(a.cacheDir, id+".vtt")

	if _, err := os.Stat(cachedPath); err == nil {
		return ParseVTTEntries(cachedPath, 0, 0)
	}
	if err := os.MkdirAll(a.cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("subtitles: mkdir cache: %w", err)
	}
	args := a.cmdBuilder.BaseArgs(videoURL, a.useCookies)
	args = append(args,
		"--write-auto-subs",
		"--write-subs",
		"--skip-download",
		"--sub-langs", a.langs,
		"--sub-format", "vtt",
		"--convert-subs", "vtt",
	)
	args = append(args, videoURL)
	args = append(args, "-o", filepath.Join(a.cacheDir, "%(id)s.%(ext)s"))
	// best-effort: no error if yt-dlp can't fetch subs.
	_, _, _ = a.runner.Run(ctx, a.ytdlpPath, args)
	if _, err := os.Stat(cachedPath); err != nil {
		return nil, nil
	}
	return ParseVTTEntries(cachedPath, 0, 0)
}

// SliceSubtitles reads the cached VTT for videoID, applies the
// rolling dedup pass via ParseVTTFile, filters to [startSec, endSec],
// writes the cleaned text to outputPath. (No priority-chain logic;
// this is a leaf operation.)
func (a *SubtitleFetcherAdapter) SliceSubtitles(_ context.Context, videoID string, startSec, endSec int, outputPath string) error {
	if a.cacheDir == "" {
		return fmt.Errorf("subtitles: cacheDir is required")
	}
	if outputPath == "" {
		return fmt.Errorf("subtitles: outputPath is required")
	}
	vttPath := filepath.Join(a.cacheDir, videoID+".vtt")
	if _, err := os.Stat(vttPath); err != nil {
		if writeErr := os.WriteFile(outputPath, []byte{}, 0o644); writeErr != nil {
			return fmt.Errorf("subtitles: write empty transcript at %s: %w", outputPath, writeErr)
		}
		if endSec > startSec {
			return fmt.Errorf("subtitles: no cached VTT for %s in %s", videoID, a.cacheDir)
		}
		return nil
	}
	text, err := ParseVTTFile(vttPath, float64(startSec), float64(endSec))
	if err != nil {
		return fmt.Errorf("subtitles: parse %s: %w", vttPath, err)
	}
	return os.WriteFile(outputPath, []byte(text), 0o644)
}
