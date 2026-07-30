// Package ytdlp centralizes yt-dlp command construction and output verification.
// Both internal/infrastructure/downloader (media retrieval) and
// internal/infrastructure/youtube (metadata/search) depend on yt-dlp for
// different operations. This package provides the shared baseline so neither
// domain package duplicates path resolution, cookie handling, JS runtime
// setup, warning suppression, or extractor-arg selection.
//
// Blocco 5 (July 2026): extracted from downloader.go + ytdlp.go.
package ytdlp

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// DefaultYouTubeFormatSelectors is the canonical yt-dlp `-f` selector — the
// single source of truth (godlike/06 SSOT) for every YouTube download path.
// Combined-first selection ensures downloaded clips contain an audio stream
// for transcription and subtitle generation. The progressive MP4 fallbacks
// cover videos that expose no separate audio format. Do NOT re-declare this
// literal elsewhere; both
// FormatArg and the processor downloader reference this constant so yt-dlp's
// "last -f wins" rule stays in lockstep. Verified against dtpF3BrSOto
// (2026-07-06) — previous bv*+ba-first order failed with "Requested format
// is not available".
const DefaultYouTubeFormatSelectors = "bv*[height<=1080][ext=mp4]+ba[ext=m4a]/b[height<=1080][ext=mp4]/best[height<=1080][ext=mp4]/best[ext=mp4]/best"

// CommandBuilder centralizes yt-dlp CLI argument construction. The builder
// owns the resolved binary path plus the cookie and JS-runtime locations;
// callers append their operation-specific flags to BaseArgs or FormatArgs.
type CommandBuilder struct {
	Path          string
	cookiesPath   string
	jsRuntimePath string
}

// NewCommandBuilder reads yt-dlp configuration from cfg.External.
// Path is resolved via cfg.External.ResolvedYtdlpPath(); cookies and JS
// runtime paths honour the same zero-value → default fallback that the
// pre-Bloco-5 callers applied independently.
func NewCommandBuilder(cfg *config.Config) *CommandBuilder {
	cookiesPath := cfg.External.YouTubeCookiesPath
	if cookiesPath == "" {
		cookiesPath = "cookies.txt"
	}
	jsRuntimePath := cfg.External.YouTubeJSRuntimePath
	if jsRuntimePath == "" {
		jsRuntimePath = "node"
	}
	return &CommandBuilder{
		Path:          cfg.External.ResolvedYtdlpPath(),
		cookiesPath:   cookiesPath,
		jsRuntimePath: jsRuntimePath,
	}
}

// BaseArgs returns the common prefix args every yt-dlp call should include.
//
// The returned slice covers:
//   - --cookies <path>   (when useCookies is true and URL is YouTube)
//   - --js-runtime <path> + --remote-components ejs:github
//   - --no-warnings       (suppresses Python deprecation warnings on stderr)
//   - --extractor-args youtube:player_client=...  (android+web, or web-only with cookies)
//
// Callers MUST append their operation-specific flags (--dump-json,
// --flat-playlist, --download-sections, -f, -o, etc.) to the returned
// slice. The returned slice is freshly allocated and safe to mutate.
func (b *CommandBuilder) BaseArgs(url string, useCookies bool) []string {
	var args []string
	isYouTube := strings.Contains(url, "youtube.com") || strings.Contains(url, "youtu.be")

	if isYouTube {
		if useCookies && b.cookiesPath != "" {
			args = append(args, "--cookies", b.cookiesPath)
		}
		if b.jsRuntimePath != "" {
			args = append(args, "--js-runtime", b.jsRuntimePath)
			args = append(args, "--remote-components", "ejs:github")
		}
	}

	// Suppress Python deprecation warnings that contaminate stderr
	args = append(args, "--no-warnings")

	if isYouTube {
		// Prefer web/android, then fall back to mweb/android_creator. The
		// latter pair remains usable when YouTube rate-limits the web
		// session while still serving public progressive formats.
		// The android client returns wrong durations + missing formats for
		// some videos when tried first; web,android order tries web first
		// (correct metadata) with android as fallback for videos like
		// dtpF3BrSOto that have no video formats with web-only.
		//
		// Pre-July-2026: cookies switched to web-only because android+
		// cookies was a brittle combination. YouTube now accepts cookies
		// with the combined client list, and web-only causes regressions
		// (no video formats) on several videos. Always use both.
		args = append(args, "--extractor-args", "youtube:player_client=android_creator")
	}

	return args
}

// FormatArg returns the -f format string for YouTube downloads when addFormat
// is true. Callers that need format selection (downloader) append this;
// metadata-only callers (--dump-json) skip it.
func (b *CommandBuilder) FormatArg(addFormat bool) []string {
	if !addFormat {
		return nil
	}
	// Reference the canonical constant — see DefaultYouTubeFormatSelectors
	// godoc for the full rationale. Duplicating the literal here would
	// drift from the processor's selector the next time one side is edited.
	return []string{
		"-f", DefaultYouTubeFormatSelectors,
	}
}
