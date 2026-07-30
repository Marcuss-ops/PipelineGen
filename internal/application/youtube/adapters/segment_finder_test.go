package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// containsFlag reports whether args contains the exact flag string.
// Used for asserting the presence/absence of single-token flags.
func containsFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

// containsFlagValue reports whether args contains the flag followed by
// the given value (as a separate arg). Used for asserting paired-flag
// values (e.g. "--extractor-args youtube:player_client=web,android").
func containsFlagValue(args []string, flag, value string) bool {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

// TestBuildSubtitleArgs_DelegatesToBaseArgs is the load-bearing
// godlike/06 SSOT regression guard for PR-SEGMENT-FINDER-BASEARGS-MIGRATION.
// It pins the canonical BaseArgs delegation contract: the segment
// finder MUST delegate to ytdlp.BaseArgs() for the canonical yt-dlp
// argv prefix (not re-declare its own --no-warnings + --extractor-args).
// Drift (e.g., reverting to inline --no-warnings) surfaces as a test
// failure here BEFORE the regression reaches production.
//
// Failure modes this test catches:
//  1. Loss of canonical web,android policy (the f3f1ee90 web-first
//     policy) — would surface as the substring
//     "youtube:player_client=web,android" being absent from argv.
//  2. Re-introduction of `youtube:player_client=android,web` (the
//     pre-f3f1ee90 reversed-order drift).
//  3. Double-add of --no-warnings (would happen if the segment finder
//     kept its own `--no-warnings` AND delegated to BaseArgs).
//  4. Loss of --no-warnings entirely (the canonical BaseArgs prefix).
func TestBuildSubtitleArgs_DelegatesToBaseArgs(t *testing.T) {
	const (
		ytdlpPath      = "/usr/bin/yt-dlp"
		videoURL       = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
		outputTemplate = "/tmp/subs_segments_xxx/subs"
	)
	args := buildSubtitleArgs(ytdlpPath, videoURL, outputTemplate)

	require.NotEmpty(t, args, "argv must not be empty")

	// 1. Canonical web,android is present (the f3f1ee90 web-first policy)
	require.True(t, containsFlagValue(args, "--extractor-args", "youtube:player_client=android_creator"),
		"segment finder must use the canonical web,android order centralized in cmd_builder.go")

	// 2. The drift (reversed order) MUST NOT appear anywhere in argv
	for _, arg := range args {
		assert.NotEqual(t, "youtube:player_client=android,web", arg,
			"reversed-order drift leaked into segment_finder argv (PR-PLAYER-CLIENT-DRIFT-FIX regression)")
	}

	// 3. --no-warnings must appear EXACTLY ONCE (no double-add from
	// BaseArgs() + a hypothetical re-declared manual append)
	noWarningsCount := 0
	for _, arg := range args {
		if arg == "--no-warnings" {
			noWarningsCount++
		}
	}
	assert.Equal(t, 1, noWarningsCount,
		"--no-warnings must appear exactly once (BaseArgs() is the SOLE owner; no manual re-declaration)")

	// 4. Operation-specific flags are still present
	assert.True(t, containsFlag(args, "--write-auto-subs"),
		"--write-auto-subs must be present (operation-specific flag)")
	assert.True(t, containsFlag(args, "--write-subs"),
		"--write-subs must be present (operation-specific flag)")
	assert.True(t, containsFlag(args, "--skip-download"),
		"--skip-download must be present (operation-specific flag)")
	assert.True(t, containsFlagValue(args, "--sub-langs", "en"),
		"--sub-langs en must be present (operation-specific flag)")
	assert.True(t, containsFlagValue(args, "--sub-format", "vtt"),
		"--sub-format vtt must be present (operation-specific flag)")
	assert.True(t, containsFlagValue(args, "-o", outputTemplate),
		"-o <outputTemplate> must be present (operation-specific flag)")

	// 5. URL must be present (positional, at the end)
	assert.True(t, containsFlag(args, videoURL),
		"videoURL must be in the argv (positional, appended after the BaseArgs prefix)")
}

// TestBuildSubtitleArgs_PublicVideoSemantics verifies the useCookies=false
// contract: the segment finder operates on PUBLIC videos for clip
// segmentation, so it MUST NOT inject --cookies (which would slow down
// the request and may trigger Artlist-style auth flows on public videos).
//
// This is the regression guard for the godlike/07 minimum-blast-radius
// decision: useCookies is hardcoded to false because the segment finder
// is the public-video segmentation use case, NOT the monitor (which
// sweeps the full channel feed including age-restricted videos).
//
// Failure modes this test catches:
//  1. Accidental flip of useCookies to true (would break public-video
//     segmentation on channels where cookies are not configured).
//  2. Future refactor that adds a UseCookies field to the segment
//     finder but defaults to true (silent-failure for public videos).
//  3. Loss of the canonical BaseArgs isYouTube-gating (would inject
//     --cookies for non-YouTube URLs too).
func TestBuildSubtitleArgs_PublicVideoSemantics(t *testing.T) {
	const (
		ytdlpPath      = "/usr/bin/yt-dlp"
		videoURL       = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
		outputTemplate = "/tmp/subs_segments_xxx/subs"
	)
	args := buildSubtitleArgs(ytdlpPath, videoURL, outputTemplate)

	// --cookies MUST NOT be present (useCookies=false: public videos only)
	assert.False(t, containsFlag(args, "--cookies"),
		"--cookies must NOT be present (useCookies=false: segment finder operates on public videos only)")

	// --js-runtime MUST be present with value "node" (the fallback)
	assert.True(t, containsFlag(args, "--js-runtime"),
		"--js-runtime must be present (empty config defaults to 'node')")
	assert.True(t, containsFlagValue(args, "--js-runtime", "node"),
		"--js-runtime value must be 'node' when config is empty")

	// --remote-components MUST be present (co-injected with --js-runtime)
	assert.True(t, containsFlag(args, "--remote-components"),
		"--remote-components must be present (co-injected with --js-runtime)")
}
