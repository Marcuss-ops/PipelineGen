// Package ytdlp tests — TDD contract surface for CommandBuilder.BaseArgs.
// Pins the July 2026 ("web,android") player_client policy introduced in
// commit f3f1ee90 (AdminAdapter type assertion + cookies web,android
// policy). The web-only behaviour failed for videos like dtpF3BrSOto;
// the closed matrix below guards against future regressions where the
// flag reverts to web-only or the conditional branches reappear.
//
// godlike/06 SSOT: BaseArgs is the canonical sole owner of the
// yt-dlp argv prefix. Every consumer (downloader, youtube metadata
// extractor) appends operation-specific flags to its output.
package ytdlp

import (
	"reflect"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"slices"
	"testing"
)

// youTubeURLA is canonical YouTube URL used for the matrix.
const youTubeURLA = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

// nonYouTubeURLA is a non-YouTube source (Drive + direct MP4 + stock).
const nonYouTubeURLA = "https://drive.google.com/file/d/abc123/view"

// expectedExtractorArgs pins the f3f1ee90 web,android player client
// ordering. The constant exists so future regressions (silent revert
// to web-only) surface as a diff in this test file rather than as a
// runtime failure on a stock pipeline run.
var expectedExtractorArgs = []string{
	"--extractor-args", "youtube:player_client=android_creator",
}

// newTestBuilder returns a CommandBuilder with the resource paths the
// test scenario requests. keepCookiesPath=true wires an explicit fixture
// path; keepJsRuntime=true wires a non-empty JS runtime path.
func newTestBuilder(t *testing.T, keepCookiesPath, keepJsRuntime bool) *CommandBuilder {
	t.Helper()
	b := &CommandBuilder{
		Path: "/usr/bin/yt-dlp",
	}
	if keepCookiesPath {
		b.cookiesPath = "/secure/youtube.cookies.txt"
	}
	if keepJsRuntime {
		b.jsRuntimePath = "/usr/bin/node"
	}
	return b
}

// TestBaseArgs_YouTubeURL_NoCookies_NoJsRuntime_PinsWebAndroid is the
// canonical pin for commit f3f1ee90. Without this assertion, a future
// agent could silently revert to web-only and the symptom would only
// surface as "no video formats" at the yt-dlp subprocess layer.
//
// Expected argv (exact order matters for godlike/06 SSOT lockstep
// with consumer call sites that append operation-specific flags):
//
//	--no-warnings
//	--extractor-args
//	youtube:player_client=web,android
func TestBaseArgs_YouTubeURL_NoCookies_NoJsRuntime_PinsWebAndroid(t *testing.T) {
	b := newTestBuilder(t, false, false)
	got := b.BaseArgs(youTubeURLA, false)

	want := append([]string{"--no-warnings"}, expectedExtractorArgs...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\nwant: %v\ngot:  %v", want, got)
	}
}

// TestBaseArgs_YouTubeURL_WithCookies_AppendsCookiesFlag verifies that
// when useCookies is true and b.cookiesPath is non-empty, BaseArgs
// prepends --cookies <path> BEFORE the --no-warnings / --extractor-args
// pair. Cookies ordering matters: --cookies must be early because
// yt-dlp applies extractor-class flags after authentication.
func TestNewCommandBuilder_UsesCanonicalCookieEnv(t *testing.T) {
	t.Setenv("VELOX_YOUTUBE_COOKIES_FILE", "/secure/youtube.cookies.txt")
	t.Setenv("YT_COOKIES_PATH", "/legacy/youtube.cookies.txt")
	b := NewCommandBuilder(&config.Config{})
	got := b.BaseArgs(youTubeURLA, true)
	if len(got) < 2 || got[0] != "--cookies" || got[1] != "/secure/youtube.cookies.txt" {
		t.Fatalf("canonical cookie resolver not applied: %v", got)
	}
}

func TestBaseArgs_YouTubeURL_WithCookies_AppendsCookiesFlag(t *testing.T) {
	b := newTestBuilder(t, true, false)
	got := b.BaseArgs(youTubeURLA, true)

	want := append(
		[]string{"--cookies", "/secure/youtube.cookies.txt", "--no-warnings"},
		expectedExtractorArgs...,
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\nwant: %v\ngot:  %v", want, got)
	}
}

// TestBaseArgs_YouTubeURL_WithJsRuntime_AppendsJsFlags verifies the
// remote-component extraction path (Deno/Node ESM modules for
// YouTube's signature resolution).
func TestBaseArgs_YouTubeURL_WithJsRuntime_AppendsJsFlags(t *testing.T) {
	b := newTestBuilder(t, false, true)
	got := b.BaseArgs(youTubeURLA, false)

	want := append(
		[]string{
			"--js-runtime", "/usr/bin/node",
			"--remote-components", "ejs:github",
			"--no-warnings",
		},
		expectedExtractorArgs...,
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\nwant: %v\ngot:  %v", want, got)
	}
}

// TestBaseArgs_YouTubeURL_AllFlagsPresent verifies the full matrix
// (cookies + js-runtime + extractor args co-present) is byte-stable.
func TestBaseArgs_YouTubeURL_AllFlagsPresent(t *testing.T) {
	b := newTestBuilder(t, true, true)
	got := b.BaseArgs(youTubeURLA, true)

	want := append(
		[]string{
			"--cookies", "/secure/youtube.cookies.txt",
			"--js-runtime", "/usr/bin/node",
			"--remote-components", "ejs:github",
			"--no-warnings",
		},
		expectedExtractorArgs...,
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\nwant: %v\ngot:  %v", want, got)
	}
}

// TestBaseArgs_YouTubeURL_UseCookiesFalseWithCookiesPathSet_NoCookiesArg
// is the edge case: cookiesPath may be set in cfg but the per-call
// useCookies=false flag suppresses it. The BaseArgs API distinguishes
// "cookies available" from "use them this call" — a contract worth
// pinning so future refactors don't accidentally collapse them.
func TestBaseArgs_YouTubeURL_UseCookiesFalseWithCookiesPathSet_NoCookiesArg(t *testing.T) {
	b := newTestBuilder(t, true /*cookiesPath*/, false)
	got := b.BaseArgs(youTubeURLA, false /*useCookies*/)

	// --cookies must NOT appear even though b.cookiesPath is configured.
	for _, arg := range got {
		if arg == "--cookies" {
			t.Fatalf("--cookies leaked despite useCookies=false: %v", got)
		}
	}

	// --extractor-args MUST still be present (it's per-URL, not per-cookies).
	want := append([]string{"--no-warnings"}, expectedExtractorArgs...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\nwant: %v\ngot:  %v", want, got)
	}
}

// TestBaseArgs_NonYouTubeURL_NoExtractorArgs is the cross-domain guard:
// a stock-pipeline direct URL or Drive file MUST NOT --extractor-args
// (those are YouTube-only). Drift here would cause yt-dlp to fail
// with "no extractor" errors on direct stock downloads.
func TestBaseArgs_NonYouTubeURL_NoExtractorArgs(t *testing.T) {
	b := newTestBuilder(t, true, true)
	got := b.BaseArgs(nonYouTubeURLA, true)

	if slices.Contains(got, "--extractor-args") {
		t.Fatalf("--extractor-args leaked on non-YouTube URL: %v", got)
	}
	if slices.Contains(got, "--cookies") {
		t.Fatalf("--cookies leaked on non-YouTube URL: %v", got)
	}
	if slices.Contains(got, "--js-runtime") {
		t.Fatalf("--js-runtime leaked on non-YouTube URL: %v", got)
	}
	want := []string{"--no-warnings"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\nwant: %v\ngot:  %v", want, got)
	}
}

// TestBaseArgs_NonYouTubeURL_StillIncludesNoWarnings verifies the
// always-on --no-warnings flag (Python deprecation warning suppression
// contaminates stderr for ALL yt-dlp runs, not just YouTube).
func TestBaseArgs_NonYouTubeURL_StillIncludesNoWarnings(t *testing.T) {
	b := newTestBuilder(t, false, false)
	got := b.BaseArgs(nonYouTubeURLA, false)

	if !slices.Contains(got, "--no-warnings") {
		t.Fatalf("--no-warnings missing on non-YouTube URL: %v", got)
	}
}

// TestBaseArgs_PlayerClientNeverWebOnly is the explicit regression
// guard against the pre-f3f1ee90 web-only behaviour. The substring
// "youtube:player_client=web" (web alone, no comma-and-android) MUST
// never appear. If it does, the policy silently reverted.
func TestBaseArgs_PlayerClientNeverWebOnly(t *testing.T) {
	b := newTestBuilder(t, true, true)

	scenarios := []struct {
		name       string
		url        string
		useCookies bool
	}{
		{"YouTube/no-cookies/no-js", youTubeURLA, false},
		{"YouTube/cookies/no-js", youTubeURLA, true},
		{"YouTube/no-cookies/js", youTubeURLA, false},
		{"YouTube/cookies/js", youTubeURLA, true},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			args := b.BaseArgs(sc.url, sc.useCookies)
			found := false
			for _, a := range args {
				if a == "youtube:player_client=web" {
					found = true
					break
				}
			}
			if found {
				t.Fatalf("player_client=web-only leaked into argv (regression): %v", args)
			}
		})
	}
}

// TestBaseArgs_ReturnsFreshlyAllocatedSlice_NoMutationBleed is the
// load-bearing guarantee for the per-call allocation contract
// documented in the godoc: callers may mutate the returned slice
// without affecting subsequent calls (the package must NOT return
// a cached singleton). The mutation-blush assertion is the canonical
// signal — if BaseArgs returns a cached pointer shared with the
// caller, appending to it would make later calls see the marker.
func TestBaseArgs_ReturnsFreshlyAllocatedSlice_NoMutationBleed(t *testing.T) {
	b := newTestBuilder(t, true, true)

	first := b.BaseArgs(youTubeURLA, true)
	// Mutate first: append junk that should NOT bleed into second.
	first = append(first, "--MUTATION-MARKER")

	second := b.BaseArgs(youTubeURLA, true)
	if slices.Contains(second, "--MUTATION-MARKER") {
		t.Fatalf("mutation leaked into a later call (cached singleton HAZARD): %v", second)
	}
}

// TestBaseArgsForClient_AlternateClient pins the fallback-client surface
// (August 2026 hot-IP recovery): BaseArgsForClient must swap the
// player_client value to the requested client and MUST NOT emit the
// canonical android_creator literal at the same time (a single
// --extractor-args pair per invocation, like BaseArgs).
func TestBaseArgsForClient_AlternateClient(t *testing.T) {
	b := newTestBuilder(t, true, true)

	args := b.BaseArgsForClient(youTubeURLA, true, "ios")

	if !slices.Contains(args, "youtube:player_client=ios") {
		t.Fatalf("BaseArgsForClient must select the requested client, got %v", args)
	}
	if slices.Contains(args, "youtube:player_client=android_creator") {
		t.Fatalf("BaseArgsForClient must not keep the canonical client when overridden: %v", args)
	}

	extractorArgsCount := 0
	for _, a := range args {
		if a == "--extractor-args" {
			extractorArgsCount++
		}
	}
	if extractorArgsCount != 1 {
		t.Fatalf("expected exactly one --extractor-args pair, got %d in %v", extractorArgsCount, args)
	}
}

// TestBaseArgsForClient_BlankClient_FallsBackToCanonical pins the
// fail-closed contract: a blank playerClient resolves to the canonical
// android_creator client so the primary attempt argv is byte-identical to
// BaseArgs.
func TestBaseArgsForClient_BlankClient_FallsBackToCanonical(t *testing.T) {
	b := newTestBuilder(t, true, true)

	viaBase := b.BaseArgs(youTubeURLA, true)
	viaBlank := b.BaseArgsForClient(youTubeURLA, true, "")

	if !reflect.DeepEqual(viaBase, viaBlank) {
		t.Fatalf("blank-client argv must match BaseArgs exactly\nbase:  %v\nblank: %v", viaBase, viaBlank)
	}
}

// TestCommandBuilder_FallbackYouTubePlayerClients pins the config-driven
// fallback list surface: the configured list is honored, unset lists fall
// back to the deterministic android,ios,web_creator,tv default, and the
// returned slice is freshly allocated (caller mutation cannot bleed into
// the builder).
func TestCommandBuilder_FallbackYouTubePlayerClients(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		b := NewCommandBuilder(&config.Config{})
		got := b.FallbackYouTubePlayerClients()
		want := []string{"android", "ios", "web_creator", "tv"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("default fallback mismatch: got %v want %v", got, want)
		}
	})

	t.Run("configured list honored", func(t *testing.T) {
		cfg := &config.Config{External: config.ExternalConfig{
			YoutubePlayerClientFallback: []string{"web_creator", "ios"},
		}}
		b := NewCommandBuilder(cfg)
		if got, want := b.FallbackYouTubePlayerClients(), []string{"web_creator", "ios"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("configured fallback mismatch: got %v want %v", got, want)
		}
	})

	t.Run("whitespace and duplicates normalized", func(t *testing.T) {
		cfg := &config.Config{External: config.ExternalConfig{
			YoutubePlayerClientFallback: []string{" ios ", "ios", "", "web_creator"},
		}}
		b := NewCommandBuilder(cfg)
		want := []string{"ios", "web_creator"}
		if got := b.FallbackYouTubePlayerClients(); !reflect.DeepEqual(got, want) {
			t.Fatalf("normalization mismatch: got %v want %v", got, want)
		}
	})

	t.Run("freshly allocated", func(t *testing.T) {
		b := NewCommandBuilder(&config.Config{})
		first := b.FallbackYouTubePlayerClients()
		first = append(first, "--MUTATION-MARKER")
		if slices.Contains(b.FallbackYouTubePlayerClients(), "--MUTATION-MARKER") {
			t.Fatalf("fallback mutation leaked into the builder")
		}
	})
}

// TestCommandBuilder_PrimaryYouTubePlayerClient pins the canonical
// first-try client so the downloader fallback loop and the argv contract
// never drift from each other.
func TestCommandBuilder_PrimaryYouTubePlayerClient(t *testing.T) {
	b := NewCommandBuilder(&config.Config{})
	if got := b.PrimaryYouTubePlayerClient(); got != "android_creator" {
		t.Fatalf("primary client mismatch: got %q want %q", got, "android_creator")
	}
}
