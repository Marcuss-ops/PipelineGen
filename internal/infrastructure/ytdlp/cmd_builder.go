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

// canonicalYouTubePlayerClient is the player client used by BaseArgs for
// the first (primary) download attempt. Alternate clients from
// config.External.YoutubePlayerClientFallback are only tried after a
// YouTube bot-check. This constant is the SOLE owner of the
// player_client= policy value; all consumers (BaseArgs, BaseArgsForClient,
// the downloader fallback loop) reference it instead of re-declaring the
// literal.
const canonicalYouTubePlayerClient = "android_creator"

// defaultYouTubePlayerClientFallback is the fallback order used when the
// config list is empty (deterministic default for manually-assembled
// configs that skip the loader). The primary client still runs first.
var defaultYouTubePlayerClientFallback = []string{"android", "ios", "web_creator", "tv"}

// CommandBuilder centralizes yt-dlp CLI argument construction. The builder
// owns the resolved binary path plus the cookie and JS-runtime locations;
// callers append their operation-specific flags to BaseArgs or FormatArgs.
type CommandBuilder struct {
	Path               string
	cookiesPath        string
	jsRuntimePath      string
	ytPlayerClientList []string
}

// NewCommandBuilder reads yt-dlp configuration from cfg.External.
// Path is resolved via cfg.External.ResolvedYtdlpPath(); cookies and JS
// runtime paths honour the same zero-value → default fallback that the
// pre-Bloco-5 callers applied independently. The YouTube player-client
// fallback list defaults to android,ios,web_creator,tv when unset.
func NewCommandBuilder(cfg *config.Config) *CommandBuilder {
	if cfg == nil {
		cfg = &config.Config{}
	}
	cookiesPath := cfg.External.ResolveYouTubeCookiesPath()
	jsRuntimePath := cfg.External.YouTubeJSRuntimePath
	if jsRuntimePath == "" {
		jsRuntimePath = "node"
	}
	return &CommandBuilder{
		Path:               cfg.External.ResolvedYtdlpPath(),
		cookiesPath:        cookiesPath,
		jsRuntimePath:      jsRuntimePath,
		ytPlayerClientList: normalizeClientList(cfg.External.YoutubePlayerClientFallback),
	}
}

// normalizeClientList trims whitespace and drops empty or duplicate entries.
func normalizeClientList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// PrimaryYouTubePlayerClient returns the canonical first-try player client.
func (b *CommandBuilder) PrimaryYouTubePlayerClient() string {
	return canonicalYouTubePlayerClient
}

// FallbackYouTubePlayerClients returns a copy of the alternate player
// clients tried after a YouTube bot-check, in configured order. Never
// includes the primary client. Returns the built-in default list when the
// builder was constructed without a configured list.
func (b *CommandBuilder) FallbackYouTubePlayerClients() []string {
	list := b.ytPlayerClientList
	if len(list) == 0 {
		list = defaultYouTubePlayerClientFallback
	}
	out := make([]string, 0, len(list))
	out = append(out, list...)
	return out
}

// YouTubeCookiesConfigured reports whether the canonical resolver returned
// a path. It never stats or reads the cookie file.
func (b *CommandBuilder) YouTubeCookiesConfigured() bool {
	return b != nil && strings.TrimSpace(b.cookiesPath) != ""
}

// BaseArgs returns the common prefix args every yt-dlp call should include.
//
// The returned slice covers:
//   - --cookies <path>   (when useCookies is true and URL is YouTube)
//   - --js-runtime <path> + --remote-components ejs:github
//   - --no-warnings       (suppresses Python deprecation warnings on stderr)
//   - --extractor-args youtube:player_client=...  (canonical YouTube client)
//
// Callers MUST append their operation-specific flags (--dump-json,
// --flat-playlist, --download-sections, -f, -o, etc.) to the returned
// slice. The returned slice is freshly allocated and safe to mutate.
func (b *CommandBuilder) BaseArgs(url string, useCookies bool) []string {
	return b.BaseArgsForClient(url, useCookies, canonicalYouTubePlayerClient)
}

// BaseArgsForClient is like BaseArgs but selects the given YouTube player
// client for the --extractor-args pair. Used by the downloader fallback
// loop (internal/infrastructure/downloader) to re-run a bot-checked
// download with an alternate client. A blank playerClient resolves to the
// canonical client. For non-YouTube URLs the extractor-args pair is never
// emitted, exactly like BaseArgs.
func (b *CommandBuilder) BaseArgsForClient(url string, useCookies bool, playerClient string) []string {
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
		if playerClient == "" {
			playerClient = canonicalYouTubePlayerClient
		}
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
		args = append(args, "--extractor-args", "youtube:player_client="+playerClient)
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
