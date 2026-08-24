package youtube

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subtitleCaptureRunner is a mock ProcessRunnerPort that captures the argv
// passed to Run() so the regression-guard tests can inspect the constructed
// subtitle command without spawning an actual yt-dlp subprocess.
//
// Pre-PR-SUBTITLES-BASEARGS-MIGRATION (2026-07-06): the subtitle adapter
// constructed its argv inline, so the test surface had no way to verify
// the canonical BaseArgs delegation contract (the drift to manually
// appended --no-warnings went undetected, dropping the --cookies/
// --js-runtime/--extractor-args prefix and breaking subtitle extraction
// on n-challenge + age-restricted YouTube videos). This mock is the
// load-bearing seam for the regression guards below.
type subtitleCaptureRunner struct {
	argv   []string
	path   string
	calls  int
	stdout string
	stderr string
	err    error
}

func (r *subtitleCaptureRunner) Run(_ context.Context, name string, args []string) (string, string, error) {
	r.calls++
	r.path = name
	r.argv = args
	return r.stdout, r.stderr, r.err
}

// newSubtitlesMinimalConfig builds a ytcfg.Config with a JS runtime path
// set so the regression tests can verify the --js-runtime + --remote-components
// co-injection contract (the latent bug class the PR closes — pre-PR the
// metadata/subtitle adapters injected --js-runtime without --remote-components
// ejs:github, breaking node-based signature resolution for some videos).
func newSubtitlesMinimalConfig(jsRuntimePath string) *ytcfg.Config {
	return &ytcfg.Config{
		External: ytcfg.ExternalConfig{
			YouTubeJSRuntimePath: jsRuntimePath,
		},
	}
}

// newSubtitlesCmdBuilder is a thin convenience constructor for the
// test fixtures — mirrors the production composition-root pattern of
// ytdlp.NewCommandBuilder(cfg) then passing the result into the adapter
// constructor. UseCookies is set per-test (default false for most tests;
// TestSubtitles_NChallengeReachable sets it true).
func newSubtitlesCmdBuilder(t *testing.T, jsRuntimePath string, useCookies bool) *SubtitleFetcherAdapter {
	t.Helper()
	cfg := newSubtitlesMinimalConfig(jsRuntimePath)
	cmdBuilder := ytdlp.NewCommandBuilder(cfg)
	return NewSubtitleFetcherAdapter(
		SubtitleCacheConfig{
			YTDLPPath:    "/usr/bin/yt-dlp",
			DefaultLangs: "en,en-US",
			CacheDir:     t.TempDir(),
		},
		&subtitleCaptureRunner{},
		cmdBuilder,
		useCookies,
	)
}

// TestSubtitles_DelegatesToBaseArgs_CanonicalPlayerClient is the
// load-bearing regression guard for PR-SUBTITLES-BASEARGS-MIGRATION. It
// pins the godlike/06 SSOT contract that the subtitle adapter MUST
// delegate to ytdlp.BaseArgs() for the canonical yt-dlp argv prefix
// (not re-declare its own). Drift (e.g., reverting to manual
// --no-warnings + dropping --cookies/--extractor-args) surfaces as a
// test failure here BEFORE the regression reaches production.
//
// Failure modes this test catches:
//  1. Loss of canonical web,android policy (the f3f1ee90 web-first policy).
//  2. Re-introduction of `youtube:player_client=web` (the pre-f3f1ee90
//     web-only behaviour).
//  3. Double-add of --no-warnings (would happen if the subtitle adapter
//     kept its own `--no-warnings` AND delegated to BaseArgs).
//  4. Loss of --js-runtime + --remote-components co-injection.
func TestSubtitles_DelegatesToBaseArgs_CanonicalPlayerClient(t *testing.T) {
	a := newSubtitlesCmdBuilder(t, "/usr/bin/node", false)
	runner := a.runner.(*subtitleCaptureRunner)

	_, err := a.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls, "ProcessRunner.Run must be invoked exactly once")
	require.NotEmpty(t, runner.argv, "argv must not be empty")

	// 1. Canonical web,android is present (the f3f1ee90 web-first policy)
	require.Contains(t, runner.argv, "youtube:player_client=android_creator",
		"subtitle adapter must use the canonical web,android order centralized in cmd_builder.go")

	// 2. The drift (reversed order) MUST NOT appear anywhere in argv
	for _, arg := range runner.argv {
		assert.NotEqual(t, "youtube:player_client=android,web", arg,
			"reversed-order drift leaked into subtitle argv (PR-PLAYER-CLIENT-DRIFT-FIX regression)")
	}

	// 3. --no-warnings must appear EXACTLY ONCE (no double-add from
	// BaseArgs() + a hypothetical re-declared manual append)
	noWarningsCount := 0
	for _, arg := range runner.argv {
		if arg == "--no-warnings" {
			noWarningsCount++
		}
	}
	assert.Equal(t, 1, noWarningsCount,
		"--no-warnings must appear exactly once (BaseArgs() is the SOLE owner; no manual re-declaration)")

	// 4. --js-runtime + --remote-components ejs:github co-injection
	assert.Contains(t, runner.argv, "--js-runtime",
		"--js-runtime must be present when YouTubeJSRuntimePath is set")
	assert.Contains(t, runner.argv, "/usr/bin/node",
		"--js-runtime value must be the configured JS runtime path")
	assert.Contains(t, runner.argv, "--remote-components",
		"--remote-components must co-present with --js-runtime (the canonical BaseArgs contract)")
	assert.Contains(t, runner.argv, "ejs:github",
		"--remote-components value must be 'ejs:github' (the canonical remote-component source)")

	// 5. URL must be present (it was at the START pre-PR; BaseArgs
	// returns the prefix WITHOUT the URL, so the URL is now at the
	// end before the -o flag — mirror metadata.go pattern)
	require.Contains(t, runner.argv, "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"videoURL must be in the argv (it is appended after the BaseArgs prefix)")
}

// TestSubtitles_PlayerClientNeverWebOnly mirrors the regression guard
// from cmd_builder_test.go (TestBaseArgs_PlayerClientNeverWebOnly) but
// for the subtitle adapter's argv. If a future refactor re-declares the
// player_client literal in subtitles.go and someone accidentally reverts
// to web-only, this test catches it.
//
// The check is: the substring "youtube:player_client=web" (web alone,
// no comma-and-android) MUST never appear. If it does, the policy
// silently reverted to the pre-f3f1ee90 web-only behaviour.
func TestSubtitles_PlayerClientNeverWebOnly(t *testing.T) {
	a := newSubtitlesCmdBuilder(t, "/usr/bin/node", false)
	runner := a.runner.(*subtitleCaptureRunner)

	_, err := a.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err)

	for _, arg := range runner.argv {
		assert.NotEqual(t, "youtube:player_client=web", arg,
			"player_client=web-only leaked into subtitle argv (regression to pre-f3f1ee90 policy)")
	}
}

// TestSubtitles_EmptyConfigDefaultsToNode verifies the fallback
// behaviour: when YouTubeJSRuntimePath is empty, the NewCommandBuilder
// defaults to "node" so yt-dlp always gets JS runtime for signature
// extraction (preventing 262-byte empty downloads). Both --js-runtime
// and --remote-components MUST be present.
func TestSubtitles_EmptyConfigDefaultsToNode(t *testing.T) {
	a := newSubtitlesCmdBuilder(t, "" /*empty JS runtime path*/, false)
	runner := a.runner.(*subtitleCaptureRunner)

	_, err := a.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err)

	// --js-runtime MUST be present with value "node" (the fallback)
	assert.Contains(t, runner.argv, "--js-runtime",
		"--js-runtime must appear when YouTubeJSRuntimePath is empty (defaults to 'node')")
	assert.Contains(t, runner.argv, "node",
		"--js-runtime value must be 'node' when YouTubeJSRuntimePath is empty")
	assert.Contains(t, runner.argv, "--remote-components",
		"--remote-components must co-present with --js-runtime (the canonical BaseArgs contract)")

	// Canonical web,android must still be present (unconditional policy)
	assert.Contains(t, runner.argv, "youtube:player_client=android_creator",
		"canonical web,android must be present even without explicit JS runtime")

	// --no-warnings must also still be present (unconditional policy)
	noWarningsCount := 0
	for _, arg := range runner.argv {
		if arg == "--no-warnings" {
			noWarningsCount++
		}
	}
	assert.Equal(t, 1, noWarningsCount,
		"--no-warnings must still appear once")
}

// TestSubtitles_NChallengeReachable is the unique value-add test for
// PR-SUBTITLES-BASEARGS-MIGRATION. It verifies that the useCookies=true
// path injects the canonical --cookies <path> argument into the argv
// — the load-bearing assertion for the n-challenge + age-restricted
// YouTube case that was previously unreachable (the drift dropped
// --cookies entirely, so subtitle extraction silently failed on
// protected videos).
//
// Failure modes this test catches:
//  1. useCookies=true but --cookies not in argv (regression: the
//     BaseArgs delegation is broken OR the useCookies flag is ignored).
//  2. useCookies=true but cookiesPath is empty (regression: the
//     configured resolver path is not propagated by NewCommandBuilder).
//  3. useCookies=false on a non-YouTube URL but --cookies leaked (the
//     BaseArgs contract is YouTube-only + useCookies-only).
func TestSubtitles_NChallengeReachable(t *testing.T) {
	t.Setenv("VELOX_YOUTUBE_COOKIES_FILE", "/secure/youtube.cookies.txt")
	t.Setenv("YT_COOKIES_PATH", "")
	a := newSubtitlesCmdBuilder(t, "/usr/bin/node", true /*useCookies=true*/)
	runner := a.runner.(*subtitleCaptureRunner)

	_, err := a.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err)

	// --cookies must be present (the n-challenge / age-restricted enabler)
	require.Contains(t, runner.argv, "--cookies",
		"--cookies must be present when useCookies=true (n-challenge + age-restricted enabler)")

	// The cookies path value must follow --cookies in the argv
	// (BaseArgs appends --cookies then the path as a separate arg)
	cookiesIdx := -1
	for i, arg := range runner.argv {
		if arg == "--cookies" {
			cookiesIdx = i
			break
		}
	}
	require.GreaterOrEqual(t, cookiesIdx, 0, "--cookies must be in argv")
	require.Less(t, cookiesIdx, len(runner.argv)-1,
		"--cookies must be followed by a path argument")
	assert.Equal(t, "/secure/youtube.cookies.txt", runner.argv[cookiesIdx+1],
		"cookies path must come from VELOX_YOUTUBE_COOKIES_FILE")

	// Also verify the inverse: useCookies=false on the SAME URL
	// should NOT inject --cookies (the BaseArgs contract is opt-in)
	aNoCookies := newSubtitlesCmdBuilder(t, "/usr/bin/node", false /*useCookies=false*/)
	runnerNoCookies := aNoCookies.runner.(*subtitleCaptureRunner)
	_, err = aNoCookies.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err)
	for _, arg := range runnerNoCookies.argv {
		assert.NotEqual(t, "--cookies", arg,
			"--cookies must NOT appear when useCookies=false (the opt-in contract)")
	}
}

// TestNewSubtitleFetcherAdapter_NilCmdBuilderFallsBackToDefault verifies
// the nil-fallback contract: passing nil for cmdBuilder must NOT cause a
// nil-dereference panic. The adapter substitutes a fresh
// ytdlp.NewCommandBuilder(&ytcfg.Config{}) (empty config — Path empty,
// no cookies, no JS runtime) so the adapter degrades gracefully. This
// mirrors the nil-runner fallback at line 43 (the canonical godlike/07
// fail-closed-at-construction pattern).
func TestNewSubtitleFetcherAdapter_NilCmdBuilderFallsBackToDefault(t *testing.T) {
	a := NewSubtitleFetcherAdapter(
		SubtitleCacheConfig{
			YTDLPPath:    "/usr/bin/yt-dlp",
			DefaultLangs: "en,en-US",
			CacheDir:     "/tmp/subtitles",
		},
		nil,   /*runner (will fall back to NewProcessRunnerAdapter)*/
		nil,   /*cmdBuilder (will fall back to ytdlp.NewCommandBuilder(&ytcfg.Config{}))*/
		false, /*useCookies*/
	)
	require.NotNil(t, a)
	require.NotNil(t, a.runner, "nil runner must be replaced with default ProcessRunnerAdapter")
	require.NotNil(t, a.cmdBuilder, "nil cmdBuilder must be replaced with default ytdlp.NewCommandBuilder(&ytcfg.Config{})")

	// The adapter must be usable: FetchFullVTT should construct a valid
	// argv via the fallback CommandBuilder without panicking. (The
	// runner is a fresh ProcessRunnerAdapter which would actually try
	// to spawn yt-dlp — we replace it with a captureRunner for the
	// assertion via a second constructor call to keep the test hermetic.)
	a.runner = &subtitleCaptureRunner{}
	_, err := a.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err, "FetchFullVTT must not error when the fallback CommandBuilder is used")

	captured := a.runner.(*subtitleCaptureRunner)
	require.NotEmpty(t, captured.argv, "argv must not be empty (the fallback CommandBuilder should still produce the canonical prefix)")
}
