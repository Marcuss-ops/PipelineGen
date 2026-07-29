// Package youtube — subtitles.go is the FACADE of the 5-file subtitle
// split (commit forthcoming). It owns:
//   - the SubtitleFetcherAdapter struct (concrete impl of the
//     SubtitleFetcher port);
//   - the SubtitleCacheConfig struct + NewSubtitleFetcherAdapter
//     constructor;
//   - the canonical compile-time assertion that the adapter
//     satisfies the SubtitleFetcher port;
//   - the high-level FetchSegmentSubtitles orchestrator that
//     composes the 4 leaf files (subtitles_fetch.go for the
//     yt-dlp download/cache lookup, subtitles_parse.go for the
//     VTT parser + dedup, subtitles_normalize.go for language
//   - text collapse, subtitles_fallback.go for the no-content
//     sentinels that the application-layer orchestrator
//     interprets as "fallback to Whisper").
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The 5-level acquisition chain (payload → DB → manual subs
//     → auto subs → Whisper) lives in
//     internal/application/youtube/usecase/text_track_resolver.go
//     (commit c0bae1612). This infra-level adapter is the priority-3
//     / priority-4 surface ONLY; the (nil, nil) return from
//     FetchSegmentSubtitles is the canonical signal that the
//     orchestrator falls through to Whisper.
//   - The canonical yt-dlp argv prefix lives in
//     internal/infrastructure/ytdlp/cmd_builder.go::BaseArgs.
//     This facade delegates to a.cmdBuilder.BaseArgs (DRIFT
//     PREVENTION pin: PR-SUBTITLES-BASEARGS-MIGRATION).
//
// 5-file split (July 2026):
//
//	subtitles.go           -- facade (this file) — owner of the
//	                           SubtitleFetcherAdapter surface
//	subtitles_fetch.go     -- download / cache lookup via yt-dlp
//	subtitles_parse.go     -- VTT/SRT parser + cue timestamp
//	                           extraction + rolling-cue dedup
//	subtitles_normalize.go -- BCP-47 language normalization +
//	                           text-collapse heuristics + VTT
//	                           time regex
//	subtitles_fallback.go  -- (nil, nil) sentinels + content-empty
//	                           / vtt-missing decision helpers
package youtube

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// SubtitleFetcherAdapter is the concrete impl of the SubtitleFetcher
// port declared in the package. It owns:
//
//   - process-runner field for the yt-dlp --write-auto-subs download
//     (PR-SUBTITLES-BASEARGS-MIGRATION, 2026-07-06 — see fetch.go);
//   - a per-cacheDir store so SliceSubtitles / FetchSegmentSubtitles
//     can locate the previously downloaded .vtt for a videoID;
//   - a *ytdlp.CommandBuilder + useCookies flag for the canonical
//     BaseArgs prefix delegation (the SSOT for the yt-dlp argv
//     prefix);
//   - the langs CSV (UTF-8 BCP-47 alphabet, comma-separated,
//     forwarded from cfg.Media.Multilingual.MaterializeLanguages
//     at wire-time).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): the constructor
// no longer silently defaults DefaultLangs to "en,en-US" — an empty
// DefaultLangs is the canonical "no preference" signal. The
// godlike/07 no-fake-availability invariant is preserved: the chain
// never substitutes "en" for an empty input; empty collapses to the
// BCP-47 "und" marker (the normalize.go leaf returns "und" when
// no CSV entry BCP-47-normalizes to a real language).
type SubtitleFetcherAdapter struct {
	ytdlpPath  string
	cacheDir   string
	langs      string
	runner     ProcessRunnerPort
	cmdBuilder *ytdlp.CommandBuilder
	useCookies bool
}

// SubtitleCacheConfig configures the adapter. YTDLPPath + DefaultLangs
// + CacheDir are all set at composition time in
// internal/app/build_bundles_domain_media.go.
type SubtitleCacheConfig struct {
	YTDLPPath    string
	DefaultLangs string
	CacheDir     string
}

// Compile-time assertion: *SubtitleFetcherAdapter satisfies the
// SubtitleFetcher port declared in the package. The facade MUST own
// this assertion (drift surface: a future refactor that breaks the
// port contract is caught at build time, here).
var _ SubtitleFetcher = (*SubtitleFetcherAdapter)(nil)

// NewSubtitleFetcherAdapter wires the adapter. cacheDir must be set.
// cmdBuilder is the canonical owner of the yt-dlp argv prefix
// (godlike/06 SSOT, see internal/infrastructure/ytdlp/cmd_builder.go);
// nil falls back to ytdlp.NewCommandBuilder(&ytcfg.Config{}) so the
// adapter degrades gracefully rather than nil-dereferencing. useCookies
// =true is required for age-restricted and n-challenge-protected
// YouTube videos.
func NewSubtitleFetcherAdapter(cfg SubtitleCacheConfig, runner ProcessRunnerPort, cmdBuilder *ytdlp.CommandBuilder, useCookies bool) *SubtitleFetcherAdapter {
	if runner == nil {
		runner = NewProcessRunnerAdapter()
	}
	if cmdBuilder == nil {
		cmdBuilder = ytdlp.NewCommandBuilder(&ytcfg.Config{})
	}
	return &SubtitleFetcherAdapter{
		ytdlpPath:  cfg.YTDLPPath,
		cacheDir:   cfg.CacheDir,
		langs:      cfg.DefaultLangs,
		runner:     runner,
		cmdBuilder: cmdBuilder,
		useCookies: useCookies,
	}
}

// FetchSegmentSubtitles returns the canonical typed subtitle track for
// [startSec, endSec] as an *asset.ResolvedTextBundle. The implementation
// composes:
//   - subtitles_fetch.go::FetchFullVTT — yt-dlp --write-auto-subs
//     download + cache lookup,
//   - subtitles_parse.go::ParseVTTFile + ParseVTTEntries — rolling-cue
//     dedup + per-cue window filter,
//   - subtitles_normalize.go::normalizeSubtitleLanguage — BCP-47 of
//     the configured langs CSV (strict; rejects underscore separators
//     like "pt_BR"),
//   - subtitles_fallback.go::isVttMissing + isContentEmpty +
//     triggerWhisperFallback — sentinel (nil, nil) pattern that the
//     application-layer orchestrator interprets as "fall through to
//     Whisper".
//
// godlike/06 SSOT: this is the SOLE canonical typed surface for
// subtitle-track acquisition. TextTrackResolver.AcquireSegmentText
// (c0bae1612) calls this method; no handler may reimplement the
// VTT-parsing step inline.
//
// Returns (nil, nil) when no subtitles are available for the clip
// (the canonical "not found" signal — caller falls through to
// Whisper). Returns a typed error on network failure, parse failure,
// or missing cache configuration.
func (a *SubtitleFetcherAdapter) FetchSegmentSubtitles(ctx context.Context, videoID string, startSec, endSec int) (*asset.ResolvedTextBundle, error) {
	if a.cacheDir == "" {
		return nil, fmt.Errorf("subtitles.FetchSegmentSubtitles: cacheDir is required")
	}
	if videoID == "" {
		return nil, fmt.Errorf("subtitles.FetchSegmentSubtitles: videoID is required")
	}

	// Build the canonical watch URL so FetchFullVTT triggers the
	// yt-dlp --write-auto-subs download when no cached VTT exists
	// yet. url.QueryEscape guards against videoIDs containing stray
	// characters — yt-dlp accepts both but the canonical form is
	// query-escaped.
	vttURL := "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID)
	if _, fetchErr := a.FetchFullVTT(ctx, vttURL); fetchErr != nil {
		return nil, fmt.Errorf("subtitles.FetchSegmentSubtitles: fetch: %w", fetchErr)
	}

	vttPath := filepath.Join(a.cacheDir, videoID+".vtt")
	if isVttMissing(vttPath) {
		// No VTT landed (e.g. yt-dlp succeeded but wrote nothing).
		// The fallback sentinel (nil, nil) lets the application-layer
		// orchestrator (text_track_resolver.go::AcquireSegmentText)
		// fall through to Whisper. A typed error here would mask the
		// "no subtitles available" semantic.
		return triggerWhisperFallback()
	}

	plain, parseErr := ParseVTTFile(vttPath, float64(startSec), float64(endSec))
	if parseErr != nil {
		return nil, fmt.Errorf("subtitles.FetchSegmentSubtitles: parse plaintext: %w", parseErr)
	}

	// Structured per-cue view for asset_text_track_segments (Fase 2).
	// ParseVTTEntries is the canonical cue extractor.
	entries, cueErr := ParseVTTEntries(vttPath, float64(startSec), float64(endSec))
	if cueErr != nil {
		return nil, fmt.Errorf("subtitles.FetchSegmentSubtitles: parse cues: %w", cueErr)
	}
	cues := make([]asset.TimedCue, 0, len(entries))
	for _, e := range entries {
		if e.End <= float64(startSec) || e.Start >= float64(endSec) {
			continue
		}
		cues = append(cues, asset.TimedCue{
			StartMs: int64(e.Start * 1000),
			EndMs:   int64(e.End * 1000),
			Text:    e.Text,
		})
	}

	lang := normalizeSubtitleLanguage(a.langs)

	if isContentEmpty(len(cues), plain) {
		// No usable content for the requested window — the fallback
		// sentinel lets the orchestrator fall through to Whisper.
		return triggerWhisperFallback()
	}

	return &asset.ResolvedTextBundle{
		LanguageCode:       lang,
		SourceLanguageCode: lang, // YT subtitle is original language
		PlainText:          plain,
		Cues:               cues,
		SourceType:         asset.TextSourceYouTubeSubtitle,
		IsOriginal:         true,
		Provider:           "yt-dlp",
		ModelName:          "yt-auto",
		ModelVersion:       "",
		Confidence:         nil,
	}, nil
}
