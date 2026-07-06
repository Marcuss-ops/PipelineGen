package transcripts

import (
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ytdlp"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// newMinimalCmdBuilder constructs a *ytdlp.CommandBuilder with minimal
// cfg for the BaseArgs delegation tests. Mirrors the pattern in
// internal/infrastructure/youtube/subtitles_test.go::newSubtitlesCmdBuilder.
// Per the prior dead-code disclosure in the infrastructure ctor, the
// builder falls back to the empty-config form when nil is passed; the
// newMinimalCmdBuilder mirrors the production use case where a real
// cfg is wired.
func newMinimalCmdBuilder(t *testing.T, jsRuntimePath string, useCookies bool) *ytdlp.CommandBuilder {
	t.Helper()
	cfg := &ytcfg.ExternalConfig{
		YtdlpPath:            "yt-dlp",
		YouTubeJSRuntimePath: jsRuntimePath,
		YouTubeCookiesPath:   "cookies.txt", // path content not asserted in this test surface
	}
	_ = useCookies // cookiesPath is read directly by the builder; useCookies is a per-call flag
	return ytdlp.NewCommandBuilder(&ytcfg.Config{External: *cfg})
}

// TestYTDLPSubtitleAdapter_DelegatesToBaseArgs is the load-bearing
// PR-WIRE-SUBTITLE-FETCHER-ADAPTER regression guard: it asserts the
// canonical yt-dlp argv prefix from ytdlp.BaseArgs is present in the
// built args slice. Pre-PR the adapter manually appended only
// --write-auto-subs / --write-subs / --skip-download / --sub-langs en /
// --sub-format vtt / -o / <url> and bypassed the helper entirely,
// dropping --cookies / --js-runtime / --remote-components /
// --no-warnings / --extractor-args youtube:player_client=web,android.
// This test fails if a future agent reverts the BaseArgs delegation.
func TestYTDLPSubtitleAdapter_DelegatesToBaseArgs(t *testing.T) {
	cmdBuilder := newMinimalCmdBuilder(t, "/usr/bin/node", false)
	a := &YTDLPSubtitleAdapter{
		ytdlp:      &downloader.YTDLPDownloader{}, // not exercised by buildSubtitleArgs
		cmdBuilder: cmdBuilder,
		useCookies: false,
		log:        zap.NewNop(),
	}

	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")

	// Canonical BaseArgs output must be present.
	baseArgs := cmdBuilder.BaseArgs(videoURL, false)
	baseArgsStr := strings.Join(baseArgs, " ")
	for _, expected := range []string{"--no-warnings", "--extractor-args"} {
		if !strings.Contains(baseArgsStr, expected) {
			t.Fatalf("BaseArgs did not produce %q; the helper itself regressed (test infra issue, not the adapter)", expected)
		}
		if !containsFlag(args, expected) {
			t.Fatalf("expected %q in built args (BaseArgs delegation regressed); got %v", expected, args)
		}
	}

	// Operation-specific flags must be present (NOT drift — these are
	// the subtitle adapter's caller-specific args per cmd_builder.go
	// godoc: "Callers MUST append their operation-specific flags...
	// to the returned slice.").
	for _, expected := range []string{
		"--write-auto-subs", "--write-subs", "--skip-download",
		"--sub-langs", "en", "--sub-format", "vtt", "-o", videoURL,
	} {
		if !containsFlagOrValue(args, expected) {
			t.Fatalf("expected %q in built args; got %v", expected, args)
		}
	}
}

// TestYTDLPSubtitleAdapter_PlayerClientNeverWebOnly asserts the
// canonical --extractor-args youtube:player_client=web,android is
// present (NOT the legacy web-only client). Pre-PR the adapter
// dropped --extractor-args entirely (a godlike/07 silent-success
// regression on YouTube's n-challenge + age-restricted videos).
func TestYTDLPSubtitleAdapter_PlayerClientNeverWebOnly(t *testing.T) {
	a := &YTDLPSubtitleAdapter{
		ytdlp:      &downloader.YTDLPDownloader{},
		cmdBuilder: newMinimalCmdBuilder(t, "/usr/bin/node", false),
		useCookies: false,
		log:        zap.NewNop(),
	}

	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")

	extractorArgIdx := -1
	for i, a := range args {
		if a == "--extractor-args" && i+1 < len(args) {
			extractorArgIdx = i + 1
			break
		}
	}
	if extractorArgIdx == -1 {
		t.Fatalf("--extractor-args not found; got %v", args)
	}
	val := args[extractorArgIdx]
	if !strings.Contains(val, "youtube:player_client=") {
		t.Fatalf("expected --extractor-args value to contain 'youtube:player_client=', got %q", val)
	}
	if strings.Contains(val, "youtube:player_client=web") && !strings.Contains(val, "android") {
		t.Fatalf("--extractor-args is web-only; the f3f1ee90 web-first policy requires web,android. got %q", val)
	}
}

// TestYTDLPSubtitleAdapter_EmptyConfigDefaultsToNode asserts that
// --js-runtime IS injected with value "node" when
// YouTubeJSRuntimePath is empty (the NewCommandBuilder fallback
// contract: empty → "node" so yt-dlp always gets JS runtime for
// signature extraction, preventing 262-byte empty downloads).
func TestYTDLPSubtitleAdapter_EmptyConfigDefaultsToNode(t *testing.T) {
	a := &YTDLPSubtitleAdapter{
		ytdlp:      &downloader.YTDLPDownloader{},
		cmdBuilder: newMinimalCmdBuilder(t, "", false), // empty JS runtime path
		useCookies: false,
		log:        zap.NewNop(),
	}

	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")

	if !containsFlag(args, "--js-runtime") {
		t.Fatalf("--js-runtime must appear when YouTubeJSRuntimePath is empty (defaults to 'node'); got %v", args)
	}
	// Verify the defaulted value is "node"
	runtimeIdx := -1
	for i, a := range args {
		if a == "--js-runtime" && i+1 < len(args) {
			runtimeIdx = i + 1
			break
		}
	}
	if runtimeIdx == -1 || args[runtimeIdx] != "node" {
		t.Fatalf("--js-runtime value must be 'node' when YouTubeJSRuntimePath is empty; got %v", args)
	}
}

// TestYTDLPSubtitleAdapter_NChallengeReachable is the canonical
// PR-WIRE-SUBTITLE-FETCHER-ADAPTER n-challenge regression guard.
// Pre-PR the adapter bypassed BaseArgs entirely, so --cookies was
// NEVER injected — n-challenge / age-restricted YouTube videos
// silently failed. Post-PR (useCookies=true) the BaseArgs prefix
// injects --cookies, and the n-challenge case is reachable.
func TestYTDLPSubtitleAdapter_NChallengeReachable(t *testing.T) {
	cfg := &ytcfg.ExternalConfig{
		YtdlpPath:          "yt-dlp",
		YouTubeCookiesPath: "cookies.txt",
	}
	a := &YTDLPSubtitleAdapter{
		ytdlp:      &downloader.YTDLPDownloader{},
		cmdBuilder: ytdlp.NewCommandBuilder(&ytcfg.Config{External: *cfg}),
		useCookies: true, // n-challenge path
		log:        zap.NewNop(),
	}

	videoURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")

	// --cookies must be present (useCookies=true).
	cookiesIdx := -1
	for i, a := range args {
		if a == "--cookies" && i+1 < len(args) {
			cookiesIdx = i + 1
			break
		}
	}
	if cookiesIdx == -1 {
		t.Fatalf("--cookies not found when useCookies=true; the n-challenge case is unreachable. got %v", args)
	}
	if got := args[cookiesIdx]; got != "cookies.txt" {
		t.Fatalf("--cookies value mismatch: expected %q, got %q", "cookies.txt", got)
	}

	// Inverse case: useCookies=false must NOT inject --cookies.
	a.useCookies = false
	args2 := a.buildSubtitleArgs(videoURL, "/tmp/test/subs.%(ext)s")
	if containsFlag(args2, "--cookies") {
		t.Fatalf("--cookies must NOT appear when useCookies=false; got %v", args2)
	}
}

// TestNewYTDLPSubtitleAdapter_NilCmdBuilderFallsBackToDefault verifies
// the nil-fallback contract (godlike/07 minimum-blast-radius): when
// CmdBuilder is nil, the constructor falls back to
// ytdlp.NewCommandBuilder(&ytcfg.Config{}) so the adapter degrades
// gracefully rather than nil-dereferencing at fetch time.
func TestNewYTDLPSubtitleAdapter_NilCmdBuilderFallsBackToDefault(t *testing.T) {
	a := NewYTDLPSubtitleAdapter(Deps{
		Ytdlp:      &downloader.YTDLPDownloader{},
		CmdBuilder: nil, // explicit nil → fallback expected
		UseCookies: false,
		Log:        zap.NewNop(),
	})
	if a.cmdBuilder == nil {
		t.Fatalf("expected cmdBuilder to fall back to default (ytdlp.NewCommandBuilder(&ytcfg.Config{})); got nil")
	}

	// Case 1: non-YouTube URL — the empty-config fallback should
	// produce NO anti-bot args (BaseArgs isYouTube-gated).
	nonYTURL := "https://vimeo.com/12345"
	args := a.buildSubtitleArgs(nonYTURL, "/tmp/test/subs.%(ext)s")
	if containsFlag(args, "--extractor-args") {
		t.Fatalf("empty-config fallback should not inject --extractor-args for non-YouTube URL; got %v", args)
	}
	if containsFlag(args, "--cookies") {
		t.Fatalf("empty-config fallback should not inject --cookies (useCookies=false); got %v", args)
	}
	if containsFlag(args, "--js-runtime") {
		t.Fatalf("empty-config fallback should not inject --js-runtime for non-YouTube URL; got %v", args)
	}

	// Case 2: YouTube URL — the canonical anti-bot args are
	// present (godlike/06 SSOT contract: ytdlp.BaseArgs is the SOLE
	// emitter of --no-warnings + --extractor-args for YouTube URLs,
	// regardless of the cfg content). --js-runtime defaults to
	// "node" when cfg is empty (the fallback contract).
	ytURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	args2 := a.buildSubtitleArgs(ytURL, "/tmp/test/subs.%(ext)s")
	if !containsFlag(args2, "--no-warnings") {
		t.Fatalf("empty-config fallback should still inject --no-warnings for YouTube URL; got %v", args2)
	}
	if !containsFlag(args2, "--extractor-args") {
		t.Fatalf("empty-config fallback should still inject --extractor-args for YouTube URL (godlike/06 SSOT); got %v", args2)
	}
	if containsFlag(args2, "--cookies") {
		t.Fatalf("empty-config fallback should not inject --cookies (useCookies=false + empty cfg); got %v", args2)
	}
	// --js-runtime defaults to "node" when cfg is empty
	if !containsFlag(args2, "--js-runtime") {
		t.Fatalf("empty-config fallback should inject --js-runtime with default 'node' for YouTube URL; got %v", args2)
	}
	// Verify the defaulted value is "node"
	runtimeIdx := -1
	for i, a := range args2 {
		if a == "--js-runtime" && i+1 < len(args2) {
			runtimeIdx = i + 1
			break
		}
	}
	if runtimeIdx == -1 || args2[runtimeIdx] != "node" {
		t.Fatalf("--js-runtime value must be 'node' when cfg is empty; got %v", args2)
	}
}

// containsFlag reports whether args contains the exact flag string.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// containsFlagOrValue reports whether args contains the flag OR a
// value matching needle. Used for the operation-specific flag check
// where some needles are flags ("--sub-langs") and others are values
// ("en", the URL).
func containsFlagOrValue(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}
