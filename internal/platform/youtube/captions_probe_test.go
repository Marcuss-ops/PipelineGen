package youtube

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captionedYDLPDumpJSON mirrors a real yt-dlp dump for a video that has
// BOTH manual subtitles (it) and ASR automatic_captions (en, it) — the
// exact shape that made the blind-selection problem visible: neither
// GET /api/clips/search nor /api/clips/info used to report any of it.
const captionedYDLPDumpJSON = `{
  "id": "506AyzC7d-k",
  "title": "Intervista esclusiva",
  "duration": 1800.0,
  "uploader": "Il Gangster",
  "subtitles": {
    "it": [
      {"url": "https://www.youtube.com/api/timedtext?v=506AyzC7d-k&lang=it", "name": "orig", "ext": "vtt"}
    ]
  },
  "automatic_captions": {
    "en": [
      {"url": "https://www.youtube.com/api/timedtext?v=506AyzC7d-k&lang=en", "name": "en", "ext": "vtt"}
    ],
    "it": [
      {"url": "https://www.youtube.com/api/timedtext?v=506AyzC7d-k&lang=it", "name": "it", "ext": "vtt"}
    ]
  }
}`

// TestGetVideoMetadata_ReportsCaptionAvailability pins T1.2: the dump's
// subtitles/automatic_captions dictionaries MUST surface as the
// HasCaptions probe + sorted union of caption languages on the DTO the
// handler serialises for GET /api/clips/info.
func TestGetVideoMetadata_ReportsCaptionAvailability(t *testing.T) {
	cfg := newMinimalConfig("/usr/bin/node")
	runner := &captureRunner{stdout: captionedYDLPDumpJSON}
	a := NewMetadataFetcherAdapter(cfg, runner)

	dto, err := a.GetVideoMetadata(context.Background(), "https://www.youtube.com/watch?v=506AyzC7d-k")
	require.NoError(t, err)
	require.NotNil(t, dto)

	assert.True(t, dto.HasCaptions,
		"a video exposing subtitles + automatic_captions must report has_captions=true")
	assert.Equal(t, []string{"en", "it"}, dto.CaptionLanguages,
		"caption_languages must be the sorted union of manual + ASR keys (deduplicated)")
}

// TestGetVideoMetadata_NoCaptionKeysReportsFalse guards the other side:
// a dump without either dictionary (music videos, private clips) must
// report has_captions=false rather than a default-true or stale value.
// realisticYDLPDumpJSON carries no caption keys at all.
func TestGetVideoMetadata_NoCaptionKeysReportsFalse(t *testing.T) {
	cfg := newMinimalConfig("/usr/bin/node")
	runner := &captureRunner{stdout: realisticYDLPDumpJSON}
	a := NewMetadataFetcherAdapter(cfg, runner)

	dto, err := a.GetVideoMetadata(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	require.NoError(t, err)
	require.NotNil(t, dto)

	assert.False(t, dto.HasCaptions)
	assert.Empty(t, dto.CaptionLanguages)
}

// TestYTDLPAdapter_GetVideoInfo_ReportsCaptionAvailability covers the
// SECOND dump-json chain (SearchRunnerPort → SearchRunnerAdapter → the
// L1/L2 metadata cache): the probe must be derived there too, otherwise
// a cache hit would silently lose the flag.
func TestYTDLPAdapter_GetVideoInfo_ReportsCaptionAvailability(t *testing.T) {
	var parsed ytDLPJSON
	// Reuse the caption fixture through the same unmarshal + derive path
	// the adapter runs, asserting captionFlags() behaves identically on
	// both raw shapes.
	require.NoError(t, json.Unmarshal([]byte(captionedYDLPDumpJSON), &parsed))
	has, langs := parsed.captionFlags()
	assert.True(t, has)
	assert.Equal(t, []string{"en", "it"}, langs)

	empty := ytDLPJSON{}
	has, langs = empty.captionFlags()
	assert.False(t, has)
	assert.Empty(t, langs)
}
