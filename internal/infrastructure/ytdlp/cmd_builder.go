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
	return &CommandBuilder{
		Path:          cfg.External.ResolvedYtdlpPath(),
		cookiesPath:   cookiesPath,
		jsRuntimePath: cfg.External.YouTubeJSRuntimePath,
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
		// When cookies are enabled, yt-dlp falls back to web-only extraction.
		// Android client and cookies are a brittle combination for YouTube.
		if useCookies {
			args = append(args, "--extractor-args", "youtube:player_client=web")
		} else {
			// July 2026 fix: web first, android as fallback. The android
			// client returns wrong duration (7s instead of 126s) and missing
			// formats for some videos when tried first. web,android order
			// tries web first (correct metadata) with android as fallback.
			args = append(args, "--extractor-args", "youtube:player_client=web,android")
		}
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
	return []string{
		"-f", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=1080]+bestaudio/best[height<=1080]/best",
	}
}
