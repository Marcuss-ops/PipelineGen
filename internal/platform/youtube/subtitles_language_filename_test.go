package youtube

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ytdlp"
)

// sampleVTT is a minimal but real WebVTT body (two cues).
const sampleVTT = "WEBVTT\n\n" +
	"00:00:01.000 --> 00:00:03.000\n" +
	"Ciao, sono Venanzio.\n\n" +
	"00:00:03.500 --> 00:00:06.000\n" +
	"Questa e una intervista.\n"

// writingSubtitleRunner simulates yt-dlp: on Run it writes the subtitle file
// with the language INSERTED into the name (`<id>.<lang>.vtt`), exactly what
// the canonical `-o %(id)s.%(ext)s` invocation produces on disk.
type writingSubtitleRunner struct {
	cacheDir string
	videoID  string
	lang     string
	calls    int
}

func (r *writingSubtitleRunner) Run(_ context.Context, _ string, _ []string) (string, string, error) {
	r.calls++
	p := filepath.Join(r.cacheDir, r.videoID+"."+r.lang+".vtt")
	if err := os.WriteFile(p, []byte(sampleVTT), 0o644); err != nil {
		return "", "", err
	}
	return "", "", nil
}

// newSubtitleAdapterForTest builds an adapter with a caller-supplied runner,
// cache dir and langs so the language-suffixed filename resolution can be
// tested hermetically (no yt-dlp subprocess).
func newSubtitleAdapterForTest(t *testing.T, runner ProcessRunnerPort, cacheDir, langs string) *SubtitleFetcherAdapter {
	t.Helper()
	return NewSubtitleFetcherAdapter(
		SubtitleCacheConfig{
			YTDLPPath:    "/usr/bin/yt-dlp",
			DefaultLangs: langs,
			CacheDir:     cacheDir,
		},
		runner,
		ytdlp.NewCommandBuilder(newSubtitlesMinimalConfig("/usr/bin/node")),
		false,
	)
}

// TestSubtitles_FindsLanguageSuffixedCachedVTT is the regression guard for the
// bug this closes: yt-dlp ALWAYS writes `<id>.<lang>.vtt`, but the fetcher
// probed only `<id>.vtt`, so a successful --skip-download fetch was reported
// as "no captions" and fell through to Whisper. A cached
// `<id>.<configured-lang>.vtt` must be found without re-running yt-dlp.
func TestSubtitles_FindsLanguageSuffixedCachedVTT(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.en.vtt"), []byte(sampleVTT), 0o644))

	runner := &subtitleCaptureRunner{}
	a := newSubtitleAdapterForTest(t, runner, dir, "en,en-US")

	entries, err := a.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=506AyzC7d-k")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "a cached <id>.<lang>.vtt must be parsed, not reported as missing")
	assert.Equal(t, 0, runner.calls, "a cache hit must not re-invoke yt-dlp")
}

// TestSubtitles_FindsOutsideConfiguredLangsViaGlob pins the fallback: a track
// whose language (here `it-orig`) is not in the configured CSV is still
// surfaced rather than silently dropped.
func TestSubtitles_FindsOutsideConfiguredLangsViaGlob(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.it-orig.vtt"), []byte(sampleVTT), 0o644))

	a := newSubtitleAdapterForTest(t, &subtitleCaptureRunner{}, dir, "en,en-US")

	entries, err := a.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=506AyzC7d-k")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "an out-of-config track must be found by the <id>.*.vtt fallback")
}

// TestSubtitles_FetchResolvesAfterDownload pins the post-download path: the
// file only appears DURING the yt-dlp run (simulated by the writing runner),
// so resolution must happen after Run returns.
func TestSubtitles_FetchResolvesAfterDownload(t *testing.T) {
	dir := t.TempDir()
	runner := &writingSubtitleRunner{cacheDir: dir, videoID: "506AyzC7d-k", lang: "it"}
	a := newSubtitleAdapterForTest(t, runner, dir, "it,en")

	entries, err := a.FetchFullVTT(context.Background(), "https://www.youtube.com/watch?v=506AyzC7d-k")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the language-suffixed file written by yt-dlp must be resolved after the run")
	assert.Equal(t, 1, runner.calls)
}

// TestSubtitles_NoVTTIsHonestNoCaptions pins the negative arm: with no VTT on
// disk and a runner that writes nothing, the adapter reports "no captions"
// (nil, nil) rather than a fabricated bundle.
func TestSubtitles_NoVTTIsHonestNoCaptions(t *testing.T) {
	a := newSubtitleAdapterForTest(t, &subtitleCaptureRunner{}, t.TempDir(), "en")

	bundle, err := a.FetchSegmentSubtitles(context.Background(), "506AyzC7d-k", 0, 0)
	require.NoError(t, err)
	assert.Nil(t, bundle, "no captions must be the (nil, nil) fallback sentinel")
}

// TestSliceSubtitles_FindsLanguageSuffixedVTT is the SliceSubtitles arm of the
// same regression: the slice-only consumer (legacy pre-Fase-2 cut) must locate
// the `<id>.<lang>.vtt` yt-dlp wrote. Pre-fix it looked for `<id>.vtt`, found
// nothing and wrote an EMPTY transcript file with no error for the whole-video
// window (0/0) — silently returning "no transcript" for material that was on
// disk. The write must now contain the parsed text.
func TestSliceSubtitles_FindsLanguageSuffixedVTT(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.it.vtt"), []byte(sampleVTT), 0o644))
	out := filepath.Join(t.TempDir(), "segment.txt")

	a := newSubtitleAdapterForTest(t, &subtitleCaptureRunner{}, dir, "it,en")

	require.NoError(t, a.SliceSubtitles(context.Background(), "506AyzC7d-k", 0, 0, out))
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	require.NotEmpty(t, raw, "the sliced transcript must not be empty: the language-suffixed VTT must be found")
	assert.Contains(t, strings.ToLower(string(raw)), "venanzio", "the written text must come from the VTT cue")
}

// TestSliceSubtitles_AppliesRequestedWindow pins that the window filter still
// reaches the parser once the file is found: a cue fully outside [start, end]
// is excluded from the written slice.
func TestSliceSubtitles_AppliesRequestedWindow(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.it.vtt"), []byte(sampleVTT), 0o644))
	out := filepath.Join(t.TempDir(), "window.txt")

	a := newSubtitleAdapterForTest(t, &subtitleCaptureRunner{}, dir, "it")

	// Window [4,6] covers only the second cue ("Questa e una intervista.").
	require.NoError(t, a.SliceSubtitles(context.Background(), "506AyzC7d-k", 4, 6, out))
	raw, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(raw)), "venanzio", "a cue ending before startSec must be excluded")
}

// TestSliceSubtitles_MissingVTTIsHonest pins the negative arms of the leaf: a
// missing VTT with a real window is an error (and still leaves an empty file),
// while the whole-video window degrades to an empty file with no error.
func TestSliceSubtitles_MissingVTTIsHonest(t *testing.T) {
	a := newSubtitleAdapterForTest(t, &subtitleCaptureRunner{}, t.TempDir(), "en")

	errOut := filepath.Join(t.TempDir(), "err.txt")
	require.Error(t, a.SliceSubtitles(context.Background(), "506AyzC7d-k", 0, 120, errOut),
		"a missing VTT with a non-empty window must be reported")
	errRaw, readErr := os.ReadFile(errOut)
	require.NoError(t, readErr)
	assert.Empty(t, errRaw, "the empty transcript must still be written before the error")

	okOut := filepath.Join(t.TempDir(), "ok.txt")
	require.NoError(t, a.SliceSubtitles(context.Background(), "506AyzC7d-k", 0, 0, okOut),
		"the whole-video window degrades to an empty file with no error")
	okRaw, readErr := os.ReadFile(okOut)
	require.NoError(t, readErr)
	assert.Empty(t, okRaw)
}

// TestSubtitles_EndpointPathReturnsBundleWithCues is the end-to-end guard for
// GET /api/clips/transcript: a cached `<id>.<lang>.vtt` must yield a bundle
// with BOTH plain text and per-cue timings for the whole-video (0/0) case.
func TestSubtitles_EndpointPathReturnsBundleWithCues(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.it.vtt"), []byte(sampleVTT), 0o644))

	a := newSubtitleAdapterForTest(t, &subtitleCaptureRunner{}, dir, "it")

	bundle, err := a.FetchSegmentSubtitles(context.Background(), "506AyzC7d-k", 0, 0)
	require.NoError(t, err)
	require.NotNil(t, bundle, "the transcript endpoint must find the language-suffixed VTT")
	assert.NotEmpty(t, bundle.PlainText, "plain text must be populated")
	assert.Len(t, bundle.Cues, 2, "the whole-video case must keep every cue (second filter used to drop them all)")
	assert.Equal(t, int64(1000), bundle.Cues[0].StartMs)
}
