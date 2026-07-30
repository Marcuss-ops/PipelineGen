package downloader

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/security"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAllowedHosts is the allowlist populated into the security package
// for the duration of each test. security.ValidateDownloadURL rejects
// URLs whose host is not in this list, so the test setup must populate
// it with the hosts the test URLs use. In production this list is
// populated from config at startup; in tests we populate it inline.
var testAllowedHosts = []string{
	"artlist.io",
	"www.youtube.com",
	"youtu.be",
}

// setupTestAllowlist populates the security allowlist for the test
// and registers a t.Cleanup to reset it (godlike/07 minimum-blast-radius:
// we don't leak the test allowlist into other test packages that may
// run in the same process).
func setupTestAllowlist(t *testing.T) {
	t.Helper()
	security.SetAllowedHosts(testAllowedHosts)
	t.Cleanup(func() {
		security.SetAllowedHosts(nil)
	})
}

// captureRunner is a mock ProcessRunner that captures the argv passed
// to Run() so the regression-guard tests can inspect the constructed
// download command without spawning an actual yt-dlp subprocess.
//
// PR-ARTLIST-COOKIES-CONFIG (2026-07-06) added the config-driven
// --cookies path; this mock is the load-bearing seam for the
// conditional-injection contract (godlike/07 fail-closed: empty
// ArtlistCookiesPath SKIPS the --cookies flag entirely). Mirrors the
// captureRunner pattern in internal/infrastructure/youtube/metadata_test.go
// and internal/infrastructure/youtube/subtitles_test.go.
type captureRunner struct {
	argv   []string
	path   string
	calls  int
	result *process.Result
	err    error
}

// Compile-time pin (godlike/06 SSOT): captureRunner MUST satisfy the
// ProcessRunner port. Drift in the Run signature surfaces as a build
// failure rather than a runtime panic.
var _ ProcessRunner = (*captureRunner)(nil)

func (r *captureRunner) Run(_ context.Context, name string, args []string, _ process.Options) (*process.Result, error) {
	r.calls++
	r.path = name
	r.argv = args
	if r.result == nil {
		r.result = &process.Result{ExitCode: 0}
	}
	return r.result, r.err
}

// hasFlagArg reports whether args contains the exact flag string.
// Used for asserting the presence/absence of single-token flags
// (e.g. "--cookies", "--no-warnings", "generic:impersonate").
func hasFlagArg(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

// flagValueIndex returns the index of the value following the given
// flag, or -1 if the flag is absent / not followed by a value. Used
// for asserting paired-flag values (e.g. "--cookies <path>").
func flagValueIndex(args []string, flag string) int {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return i + 1
		}
	}
	return -1
}

// newTestDownloader builds a YTDLPDownloader wired to a fresh
// captureRunner, with the given artlistCookiesPath. The runner is
// exposed for argv assertions.
//
// godlike/07 minimum-blast-radius: we construct via NewYTDLP (so
// the canonical cmdBuilder + verifier are populated) then override
// only the runner field via direct field assignment (same package
// access). This mirrors the canonical "production wiring + test
// override" pattern from the metadata/subtitle adapter tests.
func newTestDownloader(t *testing.T, artlistCookiesPath string) (*YTDLPDownloader, *captureRunner) {
	t.Helper()
	cfg := &ytcfg.Config{
		External: ytcfg.ExternalConfig{
			ArtlistCookiesPath: artlistCookiesPath,
		},
	}
	runner := &captureRunner{}
	d := NewYTDLP(cfg)
	d.runner = runner // inject mock (Pattern 0 port)
	return d, runner
}

// writeDummyOutputFile creates a non-empty file at the output path so
// ResolveDownloadedSegmentPath + d.verifier.VerifyFile pass in the
// test. Without this, the post-exec verification chain would fail
// on a non-existent output file even though the runner is mocked.
// godlike/07 minimum-blast-radius: the test owns the dummy file
// lifecycle (t.TempDir auto-cleanup), NOT the production code.
func writeDummyOutputFile(t *testing.T, outputPath string) {
	t.Helper()
	// OutputPath may end with .%(ext)s (the yt-dlp template) — strip
	// it to get the resolved file base. Write a .mp4 sibling so
	// ResolveDownloadedSegmentPath's glob finds it.
	base := strings.TrimSuffix(outputPath, ".%(ext)s")
	resolved := base + ".mp4"
	require.NoError(t, os.MkdirAll(filepath.Dir(resolved), 0o755))
	require.NoError(t, os.WriteFile(resolved, []byte("dummy video content for hermetic test"), 0o644))
}

// TestDownload_ArtlistEmptyCookiesPath_SkipsFlag is the load-bearing
// godlike/07 fail-closed contract test for PR-ARTLIST-COOKIES-CONFIG.
// When artlistCookiesPath is empty (the canonical default), the
// --cookies flag MUST NOT appear in argv so operators see a visible
// 403 from Artlist instead of a silent --cookies /nonexistent/path
// failure on a hardcoded path.
//
// Failure modes this test catches:
//  1. Re-introduction of the hardcoded `/tmp/artlist_cookies.txt`
//     default (the pre-PR drift).
//  2. Loss of the conditional guard `if d.artlistCookiesPath != ""`
//     in Download()'s Artlist branch.
//  3. Re-introduction of any "silent default path" that would mask
//     the missing dependency.
func TestDownload_ArtlistEmptyCookiesPath_SkipsFlag(t *testing.T) {
	setupTestAllowlist(t)
	d, runner := newTestDownloader(t, "" /*empty artlist cookies path = fail-closed default*/)

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        "https://artlist.io/royalty-free-stock/boxing-knockout",
		OutputPath: outputPath,
	})
	require.NoError(t, err, "Download should succeed (captureRunner returns success)")
	require.Equal(t, 1, runner.calls, "ProcessRunner.Run must be invoked exactly once")
	require.NotEmpty(t, runner.argv, "argv must not be empty")

	// godlike/07 fail-closed: --cookies MUST be skipped when path is empty
	assert.False(t, hasFlagArg(runner.argv, "--cookies"),
		"--cookies must NOT be present when artlistCookiesPath is empty (godlike/07 fail-closed contract)")

	// The 4 other static Artlist args MUST still be emitted (they're
	// artlist-specific impersonation, NOT drift; they must always be
	// present for artlist URLs).
	assert.True(t, hasFlagArg(runner.argv, "Referer:https://artlist.io/"),
		"Referer header must be present (artlist-specific impersonation)")
	assert.True(t, hasFlagArg(runner.argv, "Origin:https://artlist.io/"),
		"Origin header must be present (artlist-specific impersonation)")
	assert.True(t, hasFlagArg(runner.argv, "generic:impersonate"),
		"extractor-args generic:impersonate must be present (artlist-specific impersonation)")
}

// TestDownload_ArtlistCustomCookiesPath_InjectsFlag is the canonical
// success-case test: when artlistCookiesPath is set, the --cookies
// flag MUST be present in argv with the configured path as the next
// argument. This is the regression guard for the "operator set
// ARTLIST_COOKIES_PATH and expects it to be honored" path.
func TestDownload_ArtlistCustomCookiesPath_InjectsFlag(t *testing.T) {
	setupTestAllowlist(t)
	const cookiesPath = "/var/lib/pipelinegen/artlist_cookies.txt"
	d, runner := newTestDownloader(t, cookiesPath)

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        "https://artlist.io/royalty-free-stock/boxing-knockout",
		OutputPath: outputPath,
	})
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	// --cookies MUST be present
	require.True(t, hasFlagArg(runner.argv, "--cookies"),
		"--cookies must be present when artlistCookiesPath is set (canonical success case)")

	// The value following --cookies MUST be the configured path
	cookiesIdx := flagValueIndex(runner.argv, "--cookies")
	require.GreaterOrEqual(t, cookiesIdx, 0,
		"--cookies must be followed by a value (the cookies path)")
	assert.Equal(t, cookiesPath, runner.argv[cookiesIdx],
		"cookies path value must be the configured artlistCookiesPath")

	// The 4 other static Artlist args MUST still be emitted
	assert.True(t, hasFlagArg(runner.argv, "Referer:https://artlist.io/"),
		"Referer header must be present (artlist-specific impersonation)")
	assert.True(t, hasFlagArg(runner.argv, "Origin:https://artlist.io/"),
		"Origin header must be present (artlist-specific impersonation)")
	assert.True(t, hasFlagArg(runner.argv, "generic:impersonate"),
		"extractor-args generic:impersonate must be present (artlist-specific impersonation)")
}

// TestDownload_NonArtlistURL_NoArtlistArgs verifies the inverse
// contract: a non-Artlist URL (e.g. YouTube) MUST NOT inject any of
// the Artlist-specific args (no --cookies, no Referer/Origin, no
// generic:impersonate). This is the regression guard against future
// refactors that accidentally leak the Artlist branch into the
// default YouTube path.
//
// The test sets artlistCookiesPath to a non-empty value to ensure the
// guard is on the URL substring check, NOT on the config field being
// empty. If the guard were "artlistCookiesPath != \"\"" instead of
// "URL contains artlist", the test would FAIL on the --cookies
// injection for the YouTube URL.
func TestDownload_NonArtlistURL_NoArtlistArgs(t *testing.T) {
	setupTestAllowlist(t)
	d, runner := newTestDownloader(t, "/var/lib/pipelinegen/artlist_cookies.txt" /*non-empty: guard must be URL-based*/)

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputPath: outputPath,
	})
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	// None of the Artlist-specific args must appear for a YouTube URL
	assert.False(t, hasFlagArg(runner.argv, "--cookies"),
		"--cookies must NOT appear for non-Artlist URLs (the Artlist branch is gated on URL substring)")
	assert.False(t, hasFlagArg(runner.argv, "Referer:https://artlist.io/"),
		"Referer header must NOT appear for non-Artlist URLs")
	assert.False(t, hasFlagArg(runner.argv, "Origin:https://artlist.io/"),
		"Origin header must NOT appear for non-Artlist URLs")
	assert.False(t, hasFlagArg(runner.argv, "generic:impersonate"),
		"extractor-args generic:impersonate must NOT appear for non-Artlist URLs")
}

// TestDownload_DelegatesToBaseArgs is the BaseArgs regression guard
// for the downloader. Mirrors
// TestGetVideoMetadata_DelegatesToBaseArgs_CanonicalPlayerClient from
// metadata_test.go — the downloader MUST delegate to ytdlp.BaseArgs()
// for the canonical yt-dlp argv prefix (--no-warnings, --extractor-args
// youtube:player_client=web,android). Drift (e.g., reverting to inline
// --no-warnings) surfaces as a test failure here BEFORE the regression
// reaches production.
//
// Failure modes this test catches:
//  1. Loss of canonical web,android policy (the f3f1ee90 web-first
//     policy) — would surface as the substring
//     "youtube:player_client=web,android" being absent from argv.
//  2. Re-introduction of `youtube:player_client=android,web` (the
//     pre-f3f1ee90 reversed-order drift).
//  3. Double-add of --no-warnings (would happen if Download()
//     re-declared --no-warnings AND delegated to BaseArgs).
//  4. Loss of the canonical web,android substring on YouTube URLs.
func TestDownload_DelegatesToBaseArgs(t *testing.T) {
	setupTestAllowlist(t)
	d, runner := newTestDownloader(t, "")

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputPath: outputPath,
	})
	require.NoError(t, err)

	// Canonical web,android is present (the f3f1ee90 web-first policy)
	require.Contains(t, runner.argv, "youtube:player_client=android_creator",
		"downloader must use the canonical web,android order centralized in cmd_builder.go")

	// The drift (reversed order) MUST NOT appear anywhere in argv
	for _, arg := range runner.argv {
		assert.NotEqual(t, "youtube:player_client=android,web", arg,
			"reversed-order drift leaked into downloader argv (PR-PLAYER-CLIENT-DRIFT-FIX regression)")
	}

	// --no-warnings must appear EXACTLY ONCE (no double-add from
	// BaseArgs() + a hypothetical re-declared manual append)
	noWarningsCount := 0
	for _, arg := range runner.argv {
		if arg == "--no-warnings" {
			noWarningsCount++
		}
	}
	assert.Equal(t, 1, noWarningsCount,
		"--no-warnings must appear exactly once (BaseArgs() is the SOLE owner; no manual re-declaration)")
}

// TestDownload_FileURL_CopiesLocalSource is the hermetic replay path
// guard: file:// URLs MUST bypass yt-dlp and copy the local file into
// the requested output template. This is the fast path used by the
// stock pipeline replay when the source media already exists on disk.
func TestDownload_FileURL_CopiesLocalSource(t *testing.T) {
	d, runner := newTestDownloader(t, "")

	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.mp4")
	require.NoError(t, os.WriteFile(sourcePath, []byte("local-source-bytes"), 0o644))

	outputPath := filepath.Join(tmpDir, "output.mp4")
	err := d.Download(context.Background(), &DownloadRequest{
		URL:        "file://" + sourcePath,
		OutputPath: outputPath,
	})
	require.NoError(t, err)
	require.Equal(t, 0, runner.calls, "file:// download must not spawn yt-dlp")

	data, readErr := os.ReadFile(outputPath + ".mp4")
	require.NoError(t, readErr)
	assert.Equal(t, "local-source-bytes", string(data))
}

func TestListChannelVideos_UsesRunnerAndParsesJSON(t *testing.T) {
	setupTestAllowlist(t)
	d, runner := newTestDownloader(t, "")
	runner.result = &process.Result{
		ExitCode: 0,
		Stdout: strings.Join([]string{
			`{"id":"one","title":"First","view_count":10,"duration":1.5}`,
			`{"id":"two","title":"Second","view_count":20,"duration":2.5}`,
			"",
		}, "\n"),
	}

	videos, err := d.ListChannelVideos(context.Background(), ListChannelVideosRequest{
		ChannelURL:  "https://www.youtube.com/@example",
		PlaylistEnd: 2,
		DateAfter:   "20240101",
	})
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)
	require.Equal(t, d.Path(), runner.path)
	require.Len(t, videos, 2)
	assert.Equal(t, "one", videos[0].ID)
	assert.Equal(t, "Second", videos[1].Title)
	assert.Equal(t, int64(20), videos[1].Views)
}

func TestListChannelVideos_FailsOnInvalidJSONLine(t *testing.T) {
	setupTestAllowlist(t)
	d, runner := newTestDownloader(t, "")
	runner.result = &process.Result{
		ExitCode: 0,
		Stdout:   "{\"id\":\"one\"}\nnot-json\n",
	}

	videos, err := d.ListChannelVideos(context.Background(), ListChannelVideosRequest{
		ChannelURL: "https://www.youtube.com/@example",
	})
	require.Error(t, err)
	require.Nil(t, videos)
	require.Equal(t, 1, runner.calls)
	require.Contains(t, err.Error(), "failed to parse yt-dlp channel listing line")
}

func TestGetVideoMetadata_UsesRunner(t *testing.T) {
	setupTestAllowlist(t)
	d, runner := newTestDownloader(t, "")
	metaJSON := map[string]any{
		"id":          "abc123",
		"title":       "Video",
		"description": "desc",
		"duration":    12.5,
	}
	b, err := json.Marshal(metaJSON)
	require.NoError(t, err)
	runner.result = &process.Result{ExitCode: 0, Stdout: string(b)}

	meta, err := d.GetVideoMetadata(context.Background(), "https://www.youtube.com/watch?v=abc123")
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)
	require.Equal(t, d.Path(), runner.path)
	require.NotNil(t, meta)
	assert.Equal(t, "abc123", meta.ID)
	assert.Equal(t, "Video", meta.Title)
}
