package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
// for the canonical yt-dlp argv prefix (--no-warnings and the
// android_creator player-client value). Drift (e.g., reverting to inline
// --no-warnings) surfaces as a test failure here BEFORE the regression
// reaches production.
//
// Failure modes this test catches:
//  1. Loss of canonical android_creator policy — would surface as the
//     player-client value being absent from argv.
//  2. Re-introduction of `youtube:player_client=android,web` (the
//     pre-f3f1ee90 reversed-order drift).
//  3. Double-add of --no-warnings (would happen if Download()
//     re-declared --no-warnings AND delegated to BaseArgs).
//  4. Loss of the canonical player-client value on YouTube URLs.
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

	// Canonical android_creator is present on the primary attempt.
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

// TestDownload_FullSource_IncludesContinueFlag pins the resume contract:
// every full-source yt-dlp download MUST carry --continue so an interrupted
// download (e.g. killed by a graceful server restart) resumes from its
// on-disk .part file on the next attempt instead of restarting from 0%.
//
// The stock pipeline's staging root (acquisition.FilesystemStager) is
// persistent across restarts, so this flag is what lets a re-claimed job
// continue an in-flight download rather than re-fetching the whole source.
func TestDownload_FullSource_IncludesContinueFlag(t *testing.T) {
	setupTestAllowlist(t)
	d, runner := newTestDownloader(t, "")

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		OutputPath: outputPath,
	})
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls)

	assert.True(t, hasFlagArg(runner.argv, "--continue"),
		"full-source downloads must include --continue so interrupted downloads resume from .part")
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

// ─── Fallback player client + rate-limit pacing (August 2026) ────────────

// scriptedCall is one canned ProcessRunner outcome for scriptedRunner.
type scriptedCall struct {
	result *process.Result
	err    error
}

// scriptedRunner is a ProcessRunner mock that replays a canned outcome
// sequence, then repeats the final outcome for any extra calls. It records
// the argv of every invocation so tests can assert the player client that
// each fallback attempt used.
type scriptedRunner struct {
	script []scriptedCall
	argv   [][]string
	calls  int
}

// Compile-time pin (godlike/06 SSOT): scriptedRunner MUST satisfy the
// ProcessRunner port.
var _ ProcessRunner = (*scriptedRunner)(nil)

func (r *scriptedRunner) Run(_ context.Context, name string, args []string, _ process.Options) (*process.Result, error) {
	r.calls++
	r.argv = append(r.argv, append([]string{}, args...))
	if len(r.script) == 0 {
		return &process.Result{ExitCode: 0}, nil
	}
	idx := r.calls - 1
	if idx >= len(r.script) {
		idx = len(r.script) - 1
	}
	c := r.script[idx]
	if c.result == nil {
		c.result = &process.Result{ExitCode: 0}
	}
	return c.result, c.err
}

// lastArgv joins the argv of the most recent invocation for substring
// assertions (player client selection, sleep flags).
func (r *scriptedRunner) lastArgv() string {
	if r.calls == 0 {
		return ""
	}
	return strings.Join(r.argv[r.calls-1], " ")
}

// botCheckError mirrors the yt-dlp error text process.Run embeds in the
// returned error for the YouTube "Sign in to confirm you're not a bot"
// rate-limit gate.
func botCheckError(videoID string) error {
	return fmt.Errorf("command yt-dlp failed: exit status 1 (output: ERROR: [youtube] %s: Sign in to confirm you're not a bot. Use --cookies-from-browser or --cookies for the authentication)", videoID)
}

// newTestDownloaderCfg builds a YTDLPDownloader from an explicit config
// (fallback clients + sleep pacing) with the given runner injected.
func newTestDownloaderCfg(t *testing.T, cfg *ytcfg.Config, runner ProcessRunner) *YTDLPDownloader {
	t.Helper()
	d := NewYTDLP(cfg)
	d.runner = runner
	d.transportSleep = func(context.Context, time.Duration) error { return nil }
	return d
}

const youTubeWatchURL = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

// TestDownload_YouTubeBotCheck_FallsBackToAlternateClients is the load-bearing
// hot-IP recovery contract: when the primary client hits the YouTube bot-check
// gate, the downloader retries with the configured fallback client instead of
// failing the job. Success on the fallback attempt must surface as a nil error,
// and the fallback attempt argv MUST select the alternate player client (routed
// through cmd_builder.BaseArgsForClient, never a re-declared literal).
func TestDownload_YouTubeBotCheck_FallsBackToAlternateClients(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubePlayerClientFallback: []string{"ios"},
	}}
	runner := &scriptedRunner{script: []scriptedCall{
		{err: botCheckError("dQw4w9WgXcQ")},
		{result: &process.Result{ExitCode: 0}},
	}}
	d := newTestDownloaderCfg(t, cfg, runner)

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        youTubeWatchURL,
		OutputPath: outputPath,
	})
	require.NoError(t, err, "fallback retry with an alternate client must succeed")
	require.Equal(t, 2, runner.calls, "primary + 1 fallback attempt")

	assert.Contains(t, runner.lastArgv(), "youtube:player_client=ios",
		"fallback attempt must select the configured alternate client")
	assert.Contains(t, strings.Join(runner.argv[0], " "), "youtube:player_client=android_creator",
		"first attempt must keep the canonical primary client")
}

// TestDownload_YouTubeBotCheck_ExhaustsClients_ReturnsError pins the
// fail-closed contract: when every client (primary + all fallbacks) hits the
// bot-check gate, the downloader MUST return the last error rather than
// swallowing it into a silent success.
func TestDownload_YouTubeBotCheck_ExhaustsClients_ReturnsError(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubePlayerClientFallback: []string{"ios", "web_creator"},
	}}
	runner := &scriptedRunner{script: []scriptedCall{
		{err: fmt.Errorf("android_creator failed: Sign in to confirm you're not a bot")},
		{err: fmt.Errorf("web_creator failed: Sign in to confirm you're not a bot")},
		{err: fmt.Errorf("tv failed: Sign in to confirm you're not a bot")},
	}}
	d := newTestDownloaderCfg(t, cfg, runner)

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        youTubeWatchURL,
		OutputPath: outputPath,
	})
	require.Error(t, err, "exhausted clients must fail closed (never a silent no-op)")
	require.Equal(t, 3, runner.calls, "primary + 2 fallback attempts, all bot-checked")
	assert.Contains(t, err.Error(), "tv failed",
		"the final error must be the last exhausted client's error")
	assert.Contains(t, err.Error(), "Sign in to confirm",
		"the returned error must preserve the bot-check signal")
}

// TestDownload_YouTube_NonRetryableError_NoRetry pins the retry boundary:
// permanent errors such as a missing video must abort immediately. A
// different player client cannot fix them and retrying would hide the real
// cause behind extra latency.
func TestDownload_YouTube_NonRetryableError_NoRetry(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubePlayerClientFallback: []string{"ios"},
	}}
	nonBotErr := fmt.Errorf("command yt-dlp failed: exit status 1 (output: ERROR: [youtube] Video unavailable: HTTP 404: Not Found)")
	runner := &scriptedRunner{script: []scriptedCall{
		{err: nonBotErr},
	}}
	d := newTestDownloaderCfg(t, cfg, runner)

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        youTubeWatchURL,
		OutputPath: outputPath,
	})
	require.Error(t, err)
	require.Equal(t, 1, runner.calls,
		"non-retryable 404 errors must NOT trigger the fallback client loop")
	assert.Contains(t, err.Error(), "404")
}

func TestDownload_TransientTransportError_RetriesSameClientAndSucceeds(t *testing.T) {
	setupTestAllowlist(t)
	runner := &scriptedRunner{script: []scriptedCall{
		{err: fmt.Errorf("command yt-dlp failed: [Errno 101] Network is unreachable")},
		{result: &process.Result{ExitCode: 0}},
	}}
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, runner)
	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	require.NoError(t, d.Download(context.Background(), &DownloadRequest{
		URL: youTubeWatchURL, OutputPath: outputPath,
	}))
	require.Equal(t, 2, runner.calls)
	assert.Contains(t, strings.Join(runner.argv[0], " "), "youtube:player_client=android_creator")
	assert.Contains(t, strings.Join(runner.argv[1], " "), "youtube:player_client=android_creator",
		"transport retry must restart with the primary client")
}

func TestDownload_TransientTransportError_IsBounded(t *testing.T) {
	setupTestAllowlist(t)
	runner := &scriptedRunner{script: []scriptedCall{{
		err: fmt.Errorf("connection reset by peer"),
	}}}
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, runner)

	err := d.Download(context.Background(), &DownloadRequest{
		URL: youTubeWatchURL, OutputPath: filepath.Join(t.TempDir(), "source.mp4"),
	})
	require.Error(t, err)
	require.Equal(t, transportRetryAttempts, runner.calls)
}

func TestDownload_TransportRetry_DoesNotRetryCancellation(t *testing.T) {
	setupTestAllowlist(t)
	runner := &scriptedRunner{script: []scriptedCall{{err: context.Canceled}}}
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, runner)

	err := d.Download(context.Background(), &DownloadRequest{
		URL: youTubeWatchURL, OutputPath: filepath.Join(t.TempDir(), "source.mp4"),
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, runner.calls)
}

// TestDownload_InvalidURL_DoesNotInvokeFallback verifies that validation
// happens before the player-client loop. Invalid input must fail closed
// without invoking yt-dlp or trying alternate clients.
func TestDownload_InvalidURL_DoesNotInvokeFallback(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubePlayerClientFallback: []string{"web_creator", "tv"},
	}}
	runner := &scriptedRunner{}
	d := newTestDownloaderCfg(t, cfg, runner)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:        "not-a-url",
		OutputPath: filepath.Join(t.TempDir(), "source.mp4"),
	})
	require.Error(t, err)
	require.Equal(t, 0, runner.calls,
		"invalid URLs must fail before the fallback runner is invoked")
	assert.Contains(t, err.Error(), "invalid URL")
}

// TestDownload_YouTube_FormatError_FallsBackToNextClient guards the failure
// mode observed in production: a valid YouTube video can expose no formats
// for one client while another client exposes a usable stream.
func TestDownload_YouTube_FormatError_FallsBackToNextClient(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubePlayerClientFallback: []string{"web_creator", "tv"},
	}}
	runner := &scriptedRunner{script: []scriptedCall{
		{err: fmt.Errorf("command yt-dlp failed: ERROR: [youtube] Requested format is not available")},
		{result: &process.Result{ExitCode: 0}},
	}}
	d := newTestDownloaderCfg(t, cfg, runner)
	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{URL: youTubeWatchURL, OutputPath: outputPath})
	require.NoError(t, err)
	require.Equal(t, 2, runner.calls)
	assert.Contains(t, runner.lastArgv(), "youtube:player_client=web_creator")
}

// TestDownload_YouTube_RetryableErrors_UseBoundedFallbackChain verifies the
// canonical three-client ladder and that a media-host 403 remains retryable.
func TestDownload_YouTube_RetryableErrors_UseBoundedFallbackChain(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubePlayerClientFallback: []string{"web_creator", "tv"},
	}}
	runner := &scriptedRunner{script: []scriptedCall{
		{err: fmt.Errorf("command yt-dlp failed: googlevideo HTTP Error 403: Forbidden")},
		{err: fmt.Errorf("command yt-dlp failed: ERROR: no video formats found")},
		{result: &process.Result{ExitCode: 0}},
	}}
	d := newTestDownloaderCfg(t, cfg, runner)
	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	err := d.Download(context.Background(), &DownloadRequest{URL: youTubeWatchURL, OutputPath: outputPath})
	require.NoError(t, err)
	require.Equal(t, 3, runner.calls)
	assert.Contains(t, strings.Join(runner.argv[0], " "), "youtube:player_client=android_creator")
	assert.Contains(t, strings.Join(runner.argv[1], " "), "youtube:player_client=web_creator")
	assert.Contains(t, strings.Join(runner.argv[2], " "), "youtube:player_client=tv")
}

// TestDownloadSections_YouTubeBotCheck_FallsBackAndFails pins the fallback
// loop on the stock pipeline path (DownloadSections): a bot-checked section
// download tries the primary client then the configured alternate clients and
// fails closed with the last error once exhausted. DownloadSections returns
// the raw section; canonical materialization belongs to its consumer.
func TestDownloadSections_YouTubeBotCheck_FallsBackAndFails(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubePlayerClientFallback: []string{"web_creator"},
	}}
	runner := &scriptedRunner{script: []scriptedCall{
		{err: botCheckError("dQw4w9WgXcQ")},
		{err: botCheckError("dQw4w9WgXcQ")},
	}}
	d := newTestDownloaderCfg(t, cfg, runner)

	outputPath := filepath.Join(t.TempDir(), "clip.mp4")

	_, err := d.DownloadSections(context.Background(), &DownloadRequest{
		URL:              youTubeWatchURL,
		OutputPath:       outputPath,
		DownloadSections: []string{"*00:01:00-00:01:05"},
	})
	require.Error(t, err)
	require.Equal(t, 2, runner.calls, "primary + 1 fallback attempt on the sections path")
	assert.Contains(t, runner.lastArgv(), "youtube:player_client=web_creator",
		"fallback attempt must select the configured alternate client")
	assert.Contains(t, err.Error(), "Sign in to confirm")
	assert.False(t, hasFlagArg(runner.argv[0], "--external-downloader"),
		"section downloads must not delegate to aria2c")
	require.Contains(t, runner.argv[0], "--download-sections",
		"section downloads must retain the yt-dlp time-range selector")
	require.Contains(t, runner.argv[0], "*00:01:00-00:01:05",
		"the requested section must be passed unchanged")
}

// TestDownloadRange_DoesNotUseExternalDownloader pins the same boundary for
// the single-range path. The runner fails before output resolution, allowing
// this argv contract to stay hermetic while proving --download-sections is
// still present and aria2c is absent even when installed on the host.
func TestDownloadRange_DoesNotUseExternalDownloader(t *testing.T) {
	setupTestAllowlist(t)
	runner := &scriptedRunner{script: []scriptedCall{{
		err: fmt.Errorf("section probe failure"),
	}}}
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, runner)

	_, err := d.DownloadRange(context.Background(), &DownloadRequest{
		URL:              youTubeWatchURL,
		OutputPath:       filepath.Join(t.TempDir(), "clip.mp4"),
		DownloadSections: []string{"*00:02:00-00:02:05"},
	})
	require.Error(t, err)
	require.Equal(t, 1, runner.calls)
	assert.False(t, hasFlagArg(runner.argv[0], "--external-downloader"),
		"range downloads must not delegate to aria2c")
	require.Contains(t, runner.argv[0], "--download-sections")
	require.Contains(t, runner.argv[0], "*00:02:00-00:02:05")
}

// TestDownload_SectionRequest_DoesNotUseExternalDownloader covers the
// generic Download entry point when callers provide a section selector.
// This keeps the policy true even if a future caller routes a section through
// Download instead of DownloadRange/DownloadSections.
func TestDownload_SectionRequest_DoesNotUseExternalDownloader(t *testing.T) {
	setupTestAllowlist(t)
	runner := &scriptedRunner{script: []scriptedCall{{
		err: fmt.Errorf("section probe failure"),
	}}}
	d := newTestDownloaderCfg(t, &ytcfg.Config{}, runner)

	err := d.Download(context.Background(), &DownloadRequest{
		URL:              youTubeWatchURL,
		OutputPath:       filepath.Join(t.TempDir(), "clip.mp4"),
		DownloadSections: []string{"*00:03:00-00:03:05"},
	})
	require.Error(t, err)
	require.Equal(t, 1, runner.calls)
	assert.False(t, hasFlagArg(runner.argv[0], "--external-downloader"),
		"section requests through Download must not delegate to aria2c")
	require.Contains(t, runner.argv[0], "--download-sections")
	require.Contains(t, runner.argv[0], "*00:03:00-00:03:05")
}

// TestDownload_YouTube_SleepArgs_Present pins the rate-limit pacing contract:
// when YTDLP_MIN/MAX_SLEEP_SECONDS are configured, every YouTube download
// argv carries the --min/--max-sleep-interval pair so yt-dlp sleeps a random
// delay before each request.
func TestDownload_YouTube_SleepArgs_Present(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubeMinSleepSeconds: 3,
		YoutubeMaxSleepSeconds: 8,
	}}
	runner := &scriptedRunner{}
	d := newTestDownloaderCfg(t, cfg, runner)

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	require.NoError(t, d.Download(context.Background(), &DownloadRequest{
		URL:        youTubeWatchURL,
		OutputPath: outputPath,
	}))
	require.Equal(t, 1, runner.calls)

	argv := runner.argv[0]
	require.Equal(t, "--min-sleep-interval", argv[flagIndex(argv, "--min-sleep-interval")])
	require.Equal(t, "--max-sleep-interval", argv[flagIndex(argv, "--max-sleep-interval")])
	// Values immediately follow their flags (range from config).
	assert.Equal(t, "3", argv[flagIndex(argv, "--min-sleep-interval")+1])
	assert.Equal(t, "8", argv[flagIndex(argv, "--max-sleep-interval")+1])
}

// flagIndex returns the index of the first occurrence of flag in args,
// or -1 when absent. Mirrors flagValueIndex but without a paired-value
// assertion for flags that may legitimately appear once.
func flagIndex(args []string, flag string) int {
	for i, a := range args {
		if a == flag {
			return i
		}
	}
	return -1
}

// TestDownload_YouTube_SleepArgs_Absent_WhenDisabled pins the opt-out
// contract: with zero sleep config (the fail-closed default for
// manually-assembled configs) no pacing flags leak into argv.
func TestDownload_YouTube_SleepArgs_Absent_WhenDisabled(t *testing.T) {
	setupTestAllowlist(t)
	d, runner := newTestDownloader(t, "") // zero sleep config

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	require.NoError(t, d.Download(context.Background(), &DownloadRequest{
		URL:        youTubeWatchURL,
		OutputPath: outputPath,
	}))
	require.Equal(t, 1, runner.calls)
	assert.Equal(t, -1, flagIndex(runner.argv, "--min-sleep-interval"),
		"pacing flags must be absent when sleep is disabled")
	assert.Equal(t, -1, flagIndex(runner.argv, "--max-sleep-interval"),
		"pacing flags must be absent when sleep is disabled")
}

// TestDownload_NonYouTube_SleepArgs_Absent pins the URL gating: pacing flags
// and the player-client fallback are YouTube-specific; an Artlist download
// argv must never carry --min/--max-sleep-interval even when sleep is
// configured.
func TestDownload_NonYouTube_SleepArgs_Absent(t *testing.T) {
	setupTestAllowlist(t)
	cfg := &ytcfg.Config{External: ytcfg.ExternalConfig{
		YoutubeMinSleepSeconds: 3,
		YoutubeMaxSleepSeconds: 8,
	}}
	runner := &scriptedRunner{}
	d := newTestDownloaderCfg(t, cfg, runner)

	outputPath := filepath.Join(t.TempDir(), "source.mp4")
	writeDummyOutputFile(t, outputPath)

	require.NoError(t, d.Download(context.Background(), &DownloadRequest{
		URL:        "https://artlist.io/royalty-free-stock/boxing-knockout",
		OutputPath: outputPath,
	}))
	require.Equal(t, 1, runner.calls)
	assert.Equal(t, -1, flagIndex(runner.argv[0], "--min-sleep-interval"),
		"pacing flags must not appear for non-YouTube downloads")
}
