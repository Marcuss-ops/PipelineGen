package youtube

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bundle's LanguageCode must be the language of the FILE THAT WAS READ.
// The old code stamped the FIRST configured CSV entry onto every bundle, so
// a video whose Italian preference silently fell back to English
// auto-captions produced an English transcript labelled "it" — which then
// poisoned asset_text_tracks, the translation fan-out and the burned
// captions ("the subs are wrong although the language is configured
// correctly").
func TestFetchSegmentSubtitles_LabelsBundleWithResolvedFileLanguage(t *testing.T) {
	dir := t.TempDir()
	// Only the English track exists on disk; the operator prefers it,en.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.en.vtt"), []byte(sampleVTT), 0o644))

	runner := &subtitleCaptureRunner{}
	a := newSubtitleAdapterForTest(t, runner, dir, "it,en")

	bundle, err := a.FetchSegmentSubtitles(context.Background(), "506AyzC7d-k", 0, 0)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	assert.Equal(t, "en", bundle.LanguageCode,
		"the bundle must carry the language of the file actually read, never the configured preference")
	assert.Equal(t, "en", bundle.SourceLanguageCode)
	assert.Equal(t, 0, runner.calls, "a configured-language cache hit must not re-invoke yt-dlp")
}

// A cached track whose language is OUTSIDE the configured CSV must not
// short-circuit the fetch: the configured language is what the operator
// asked for, so yt-dlp gets a chance to produce it first. When it produces
// nothing, the out-of-config track is still surfaced — honestly labelled
// with its own language instead of being adopted under the configured one.
func TestFetchSegmentSubtitles_OutOfConfiguredCacheStillRefetches(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.fr.vtt"), []byte(sampleVTT), 0o644))

	runner := &subtitleCaptureRunner{}
	a := newSubtitleAdapterForTest(t, runner, dir, "it,en")

	bundle, err := a.FetchSegmentSubtitles(context.Background(), "506AyzC7d-k", 0, 0)
	require.NoError(t, err)
	require.NotNil(t, bundle, "an out-of-config track must still be surfaced rather than dropped")

	assert.Equal(t, 1, runner.calls,
		"an out-of-config leftover must not look like a configured-language cache hit")
	assert.Equal(t, "fr", bundle.LanguageCode,
		"the out-of-config file must be labelled with ITS language, not the configured one")
}

// A filename language the BCP-47 normalizer cannot parse (script subtags
// like zh-Hans are rejected by the canonical rules) collapses to the "und"
// marker — never to a configured guess. The orchestrator's
// PreferredLanguages filter then treats it as non-matching and the chain
// falls through to Whisper instead of adopting a track it cannot name.
func TestFetchSegmentSubtitles_UnparseableFilenameLanguageIsUnd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.zh-Hans.vtt"), []byte(sampleVTT), 0o644))

	runner := &subtitleCaptureRunner{}
	a := newSubtitleAdapterForTest(t, runner, dir, "it,en")

	bundle, err := a.FetchSegmentSubtitles(context.Background(), "506AyzC7d-k", 0, 0)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	assert.Equal(t, "und", bundle.LanguageCode,
		"an unparseable filename language must surface as BCP-47 und, not as a configured language")
}

// The bare legacy <id>.vtt carries no language in its name: the configured
// CSV is the only available signal, so the first entry stays the label.
func TestFetchSegmentSubtitles_BareFileUsesConfiguredLanguage(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "506AyzC7d-k.vtt"), []byte(sampleVTT), 0o644))

	runner := &subtitleCaptureRunner{}
	a := newSubtitleAdapterForTest(t, runner, dir, "it,en")

	bundle, err := a.FetchSegmentSubtitles(context.Background(), "506AyzC7d-k", 0, 0)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	assert.Equal(t, "it", bundle.LanguageCode)
	assert.Equal(t, 0, runner.calls, "a bare cached VTT is a cache hit")
}

// vttLanguageToken strips the id prefix and the optional -orig suffix yt-dlp
// appends to translated tracks.
func TestVTTLanguageToken(t *testing.T) {
	cases := map[string]string{
		"/cache/506AyzC7d-k.vtt":         "",
		"/cache/506AyzC7d-k.it.vtt":      "it",
		"/cache/506AyzC7d-k.it-orig.vtt": "it",
		"/cache/506AyzC7d-k.pt-BR.vtt":   "pt-BR",
	}
	for path, want := range cases {
		assert.Equal(t, want, vttLanguageToken("506AyzC7d-k", path), "path %s", path)
	}
}
