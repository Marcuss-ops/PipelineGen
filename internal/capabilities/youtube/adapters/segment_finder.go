package adapters

import (
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
)

// ── Segment discovery constants ──────────────────────────────────────────

// ── Segment splitting ────────────────────────────────────────────────────

// ── Segment cache ────────────────────────────────────────────────────────

// buildSubtitleArgs assembles the canonical yt-dlp argv for a subtitle
// fetch used by the segment finder. Extracted from findSegmentsFromSubtitles
// so hermetic TDD tests can assert the BaseArgs delegation contract
// WITHOUT spawning a real yt-dlp subprocess.
//
// PR-SEGMENT-FINDER-BASEARGS-MIGRATION (2026-07-06): the canonical
// yt-dlp argv is built in 3 layers —
//  1. baseArgs (the canonical 4-5 anti-bot flags from ytdlp.BaseArgs)
//  2. operation-specific flags (--write-auto-subs / --write-subs /
//     --skip-download / --sub-langs en / --sub-format vtt / -o)
//  3. positional URL (appended last; yt-dlp accepts global options
//     before OR after the positional URL)
//
// useCookies is hardcoded to false (godlike/07 minimum-blast-radius):
// the segment finder operates on PUBLIC videos for clip segmentation,
// not age-restricted / n-challenge boundary cases. The monitor (which
// DOES need cookies) routes through the separate YTDLPSubtitleAdapter.
//
// godlike/07 minimum-blast-radius: the CommandBuilder is constructed
// per-call (no Service struct change). The segment_finder is called
// once per video, so the per-call overhead is negligible. A future
// optimization could hoist this to the Service struct if profiling
// shows it matters.
func buildSubtitleArgs(ytdlpPath, cookiesPath, videoURL, outputTemplate string) []string {
	cfg := &ytcfg.Config{
		External: ytcfg.ExternalConfig{
			YtdlpPath:          ytdlpPath,
			YouTubeCookiesPath: cookiesPath,
		},
	}
	builder := ytdlp.NewCommandBuilder(cfg)

	baseArgs := builder.BaseArgs(videoURL, builder.YouTubeCookiesConfigured())
	args := append([]string{}, baseArgs...)
	args = append(args,
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en", "--sub-format", "vtt",
		"-o", outputTemplate,
		videoURL,
	)
	return args
}

// ── Interesting segments (priority: subtitles > chapters) ────────────────
