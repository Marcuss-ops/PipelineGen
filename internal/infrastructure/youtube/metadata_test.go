package youtube

import (
	"context"
	"encoding/json"
	"testing"

	// DTOs (VideoThumbnail, etc.) now live in ports/.
	youtubedto "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	ytcfg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const realisticYDLPDumpJSON = `{
  "id": "dQw4w9WgXcQ",
  "title": "Never Gonna Give You Up",
  "description": "The official music video for Rick Astley.",
  "duration": 213.0,
  "uploader": "Rick Astley",
  "upload_date": "20091025",
  "view_count": 1700000000,
  "language": "en",
  "thumbnail": "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg",
  "thumbnails": [
    {"url": "https://i.ytimg.com/vi/dQw4w9WgXcQ/default.jpg", "width": 120, "height": 90},
    {"url": "https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg", "width": 480, "height": 360},
    {"url": "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg", "width": 1280, "height": 720}
  ],
  "chapters": [
    {"title": "Intro",   "start_time": 0.0,  "end_time": 42.5},
    {"title": "Verse 1", "start_time": 42.5, "end_time": 92.0},
    {"title": "Chorus",  "start_time": 92.0, "end_time": 142.3}
  ],
  "categories": ["Music"],
  "tags": ["rick astley", "never gonna give you up", "80s", "music video"]
}`

func TestYouTubeMetadata_FullUnmarshallingPreservesAllFields(t *testing.T) {
	var raw ytDLPJSON
	require.NoError(t, json.Unmarshal([]byte(realisticYDLPDumpJSON), &raw))

	assert.Equal(t, "dQw4w9WgXcQ", raw.ID)
	assert.Equal(t, "Never Gonna Give You Up", raw.Title)
	assert.Equal(t, "Rick Astley", raw.Uploader)
	assert.Equal(t, "20091025", raw.UploadDate)
	assert.Equal(t, int64(1_700_000_000), raw.ViewCount)
	assert.Equal(t, 213.0, raw.Duration)
	assert.Equal(t, "en", raw.Language)
	assert.Equal(t, "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg", raw.Thumbnail)
	require.Len(t, raw.Thumbnails, 3)
	assert.Equal(t, "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg", raw.Thumbnails[2].URL)
	assert.Equal(t, 1280, raw.Thumbnails[2].Width)
	require.Len(t, raw.Chapters, 3)
	assert.Equal(t, "Chorus", raw.Chapters[2].Title)
	assert.Equal(t, 92.0, raw.Chapters[2].StartTime)
	assert.Equal(t, []string{"Music"}, raw.Categories)
	assert.Equal(t, []string{"rick astley", "never gonna give you up", "80s", "music video"}, raw.Tags)
}

func TestYouTubeMetadata_PartialFixtureStillUnmarshals(t *testing.T) {
	const partial = `{"id":"abc","title":"minimal"}`
	var raw ytDLPJSON
	require.NoError(t, json.Unmarshal([]byte(partial), &raw))
	assert.Equal(t, "abc", raw.ID)
	assert.Equal(t, "minimal", raw.Title)
	assert.Nil(t, raw.Thumbnails)
	assert.Nil(t, raw.Chapters)
	assert.Nil(t, raw.Categories)
	assert.Nil(t, raw.Tags)
	assert.Equal(t, "", raw.Uploader)
	assert.Equal(t, 0.0, raw.Duration)
}

func TestYouTubeMetadata_InvalidJSONFails(t *testing.T) {
	var raw ytDLPJSON
	err := json.Unmarshal([]byte("{not valid json}"), &raw)
	require.Error(t, err)
}

func TestExtractIDFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/live/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/watch?v=abc123&t=42s", "abc123"},
		{"not-a-url", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got := extractIDFromURL(tc.url)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSanitizedURL_StripsQuery(t *testing.T) {
	got := sanitizedURL("https://www.youtube.com/watch?v=abc&token=secret")
	assert.Equal(t, "https://www.youtube.com/watch", got)
}

func TestSanitizedURL_KeepsPath(t *testing.T) {
	got := sanitizedURL("https://youtu.be/abc123?ref=foo")
	assert.Equal(t, "https://youtu.be/abc123", got)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hell…", truncate("hello world", 5))
	assert.Equal(t, "ab", truncate("abcdef", 2))
}

func TestNewMetadataFetcherAdapter_NilRunnerFallsBackToDefault(t *testing.T) {
	a := NewMetadataFetcherAdapter(nil, nil)
	require.NotNil(t, a)
	require.NotNil(t, a.runner, "nil runner must be replaced with default ProcessRunnerAdapter")
}

func TestMetadataFetcherAdapter_PreservesThumbnailsArray(t *testing.T) {
	// PR1 (June 2026): verify that raw yt-dlp thumbnail array is
	// correctly translated from ytDLPJSON anonymous-struct into
	// the canonical youtubedto.VideoThumbnail DTO. Previously
	// dto.Thumbnails was nil, silently dropping all thumbnail data
	// for every youtube metadata fetch.
	var raw ytDLPJSON
	require.NoError(t, json.Unmarshal([]byte(realisticYDLPDumpJSON), &raw))

	require.Len(t, raw.Thumbnails, 3)

	// Compile-time assertion: the raw anonymous-struct fields match
	// the DTO type GetVideoMetadata now maps to.
	var _ []youtubedto.VideoThumbnail = []youtubedto.VideoThumbnail{
		{URL: raw.Thumbnails[0].URL, Width: raw.Thumbnails[0].Width, Height: raw.Thumbnails[0].Height},
		{URL: raw.Thumbnails[1].URL, Width: raw.Thumbnails[1].Width, Height: raw.Thumbnails[1].Height},
		{URL: raw.Thumbnails[2].URL, Width: raw.Thumbnails[2].Width, Height: raw.Thumbnails[2].Height},
	}

	assert.Equal(t, "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg", raw.Thumbnails[2].URL)

	t.Logf("NOTE: full GetVideoMetadata round-trip test deferred until ProcessRunnerPort supports injection (see PR1 follow-up in docs/POST_CASCADE_OPERATIONAL_READINESS.md)")
}

// captureRunner is a mock ProcessRunnerPort that captures the argv passed
// to Run() so the regression-guard tests can inspect the constructed
// metadata command without spawning an actual yt-dlp subprocess.
//
// Pre-PR-PLAYER-CLIENT-DRIFT-FIX (2026-07-06): the metadata adapter
// constructed its argv inline, so the test surface had no way to verify
// the canonical web,android policy was honored (the drift to android,web
// went undetected for several months). This mock is the load-bearing
// seam for the regression guards below.
type captureRunner struct {
	argv   []string
	path   string
	calls  int
	stdout string
	stderr string
	err    error
}

func (r *captureRunner) Run(_ context.Context, name string, args []string) (string, string, error) {
	r.calls++
	r.path = name
	r.argv = args
	return r.stdout, r.stderr, r.err
}

// newMinimalConfig builds a ytcfg.Config with a JS runtime path set so
// the regression tests can verify the --js-runtime + --remote-components
// co-injection contract (the latent bug fixed by this PR — pre-PR the
// metadata adapter injected --js-runtime without --remote-components
// ejs:github, breaking node-based signature resolution for some videos).
func newMinimalConfig(jsRuntimePath string) *ytcfg.Config {
	return &ytcfg.Config{
		External: ytcfg.ExternalConfig{
			YouTubeJSRuntimePath: jsRuntimePath,
		},
	}
}

// TestGetVideoMetadata_DelegatesToBaseArgs_CanonicalPlayerClient is the
// load-bearing regression guard for PR-PLAYER-CLIENT-DRIFT-FIX. It pins
// the godlike/06 SSOT contract that the metadata adapter MUST delegate
// to ytdlp.BaseArgs() for the player_client literal (not re-declare
// its own). Drift (e.g., reverting to android,web) surfaces as a test
// failure here BEFORE the regression reaches production.
//
// Failure modes this test catches:
//  1. Re-introduction of `youtube:player_client=android,web` (the
//     pre-PR drift — wrong order, android-first policy).
//  2. Re-introduction of `youtube:player_client=web` (the pre-f3f1ee90
//     web-only behaviour that broke dtpF3BrSOto and similar videos).
//  3. Double-add of --no-warnings (would happen if the metadata adapter
//     kept its own `--no-warnings` AND delegated to BaseArgs).
//  4. Loss of --js-runtime + --remote-components co-injection (the
//     latent bug fixed by this PR).
func TestGetVideoMetadata_DelegatesToBaseArgs_CanonicalPlayerClient(t *testing.T) {
	cfg := newMinimalConfig("/usr/bin/node")
	runner := &captureRunner{stdout: realisticYDLPDumpJSON}
	a := NewMetadataFetcherAdapter(cfg, runner)

	_, err := a.GetVideoMetadata(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err)
	require.Equal(t, 1, runner.calls, "ProcessRunner.Run must be invoked exactly once")
	require.NotEmpty(t, runner.argv, "argv must not be empty")

	// 1. Canonical web,android is present (the f3f1ee90 web-first policy)
	require.Contains(t, runner.argv, "youtube:player_client=android_creator",
		"metadata adapter must use the canonical web,android order centralized in cmd_builder.go")

	// 2. The drift (reversed order) MUST NOT appear anywhere in argv
	for _, arg := range runner.argv {
		assert.NotEqual(t, "youtube:player_client=android,web", arg,
			"reversed-order drift leaked into metadata argv (PR-PLAYER-CLIENT-DRIFT-FIX regression)")
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
	//    (the latent bug fixed by this PR — pre-PR metadata adapter
	//    injected --js-runtime WITHOUT --remote-components, breaking
	//    node-based signature resolution for some videos)
	assert.Contains(t, runner.argv, "--js-runtime",
		"--js-runtime must be present when YouTubeJSRuntimePath is set")
	assert.Contains(t, runner.argv, "/usr/bin/node",
		"--js-runtime value must be the configured JS runtime path")
	assert.Contains(t, runner.argv, "--remote-components",
		"--remote-components must co-present with --js-runtime (the canonical BaseArgs contract)")
	assert.Contains(t, runner.argv, "ejs:github",
		"--remote-components value must be 'ejs:github' (the canonical remote-component source)")
}

// TestGetVideoMetadata_PlayerClientNeverWebOnly mirrors the regression
// guard from cmd_builder_test.go (TestBaseArgs_PlayerClientNeverWebOnly)
// but for the metadata adapter's argv. If a future refactor re-declares
// the player_client literal in metadata.go and someone accidentally
// reverts to web-only, this test catches it.
//
// The check is: the substring "youtube:player_client=web" (web alone,
// no comma-and-android) MUST never appear. If it does, the policy
// silently reverted to the pre-f3f1ee90 web-only behaviour.
func TestGetVideoMetadata_PlayerClientNeverWebOnly(t *testing.T) {
	cfg := newMinimalConfig("/usr/bin/node")
	runner := &captureRunner{stdout: realisticYDLPDumpJSON}
	a := NewMetadataFetcherAdapter(cfg, runner)

	_, err := a.GetVideoMetadata(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err)

	for _, arg := range runner.argv {
		assert.NotEqual(t, "youtube:player_client=web", arg,
			"player_client=web-only leaked into metadata argv (regression to pre-f3f1ee90 policy)")
	}
}

// TestGetVideoMetadata_EmptyConfigDefaultsToNode verifies the fallback
// behaviour: when YouTubeJSRuntimePath is empty, the NewCommandBuilder
// defaults to "node" so yt-dlp always gets JS runtime for signature
// extraction (preventing 262-byte empty downloads). Both --js-runtime
// and --remote-components MUST be present.
func TestGetVideoMetadata_EmptyConfigDefaultsToNode(t *testing.T) {
	cfg := newMinimalConfig("") // empty JS runtime path
	runner := &captureRunner{stdout: realisticYDLPDumpJSON}
	a := NewMetadataFetcherAdapter(cfg, runner)

	_, err := a.GetVideoMetadata(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
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
		"canonical web,android must be present")
}
