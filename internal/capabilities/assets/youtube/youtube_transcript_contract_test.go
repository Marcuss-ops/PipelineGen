package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// stubSubtitleFetcher doubles the transcript source: records the video id
// and window it received, returns a canned bundle or error.
type stubSubtitleFetcher struct {
	bundle      *detail.ResolvedTextBundle
	err         error
	calls       int
	gotVideoID  string
	gotStart    int
	gotEnd      int
	gotSliceOut string
}

func (s *stubSubtitleFetcher) SliceSubtitles(_ context.Context, _ string, _, _ int, outputPath string) error {
	s.gotSliceOut = outputPath
	return nil
}

func (s *stubSubtitleFetcher) FetchSegmentSubtitles(_ context.Context, videoID string, startSec, endSec int) (*detail.ResolvedTextBundle, error) {
	s.calls++
	s.gotVideoID = videoID
	s.gotStart = startSec
	s.gotEnd = endSec
	return s.bundle, s.err
}

// invokeGetTranscript drives the handler with the given raw query string.
func invokeGetTranscript(t *testing.T, h *YouTubeClipHandler, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.GetTranscript(ctx)
	return rec
}

// TestGetTranscript_RejectsBadInput pins the 400 arms: missing url and a
// non-YouTube URL never reach the fetcher (a fetch attempt with an
// unresolvable id would be a wasted yt-dlp round-trip).
func TestGetTranscript_RejectsBadInput(t *testing.T) {
	fetcher := &stubSubtitleFetcher{bundle: &detail.ResolvedTextBundle{PlainText: "x"}}
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.transcript = fetcher

	rec := invokeGetTranscript(t, h, "/api/clips/transcript")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "url parameter is required")

	rec = invokeGetTranscript(t, h, "/api/clips/transcript?url=https%3A%2F%2Fexample.com%2Fnot-youtube")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not a recognizable YouTube URL")

	assert.Zero(t, fetcher.calls, "the fetcher must not be consulted for invalid input")
}

// TestGetTranscript_UnwiredPortFailsClosed pins the 503 arm: an
// unwired fetcher is Service Unavailable, never a silent 404 that would
// claim the video has no captions.
func TestGetTranscript_UnwiredPortFailsClosed(t *testing.T) {
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)

	rec := invokeGetTranscript(t, h, "/api/clips/transcript?url=https%3A%2F%2Fyoutu.be%2F506AyzC7d-k")

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "not wired")
}

// TestGetTranscript_NotFoundWhenNoCaptions pins the 404 arm: an empty
// bundle (no captions on any configured language, acquisition failed
// across all levels) is "no transcript", not a 200 with empty text.
func TestGetTranscript_NotFoundWhenNoCaptions(t *testing.T) {
	fetcher := &stubSubtitleFetcher{bundle: &detail.ResolvedTextBundle{}}
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.transcript = fetcher

	rec := invokeGetTranscript(t, h, "/api/clips/transcript?url=https%3A%2F%2Fyoutu.be%2F506AyzC7d-k")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "no transcript available")
}

// TestGetTranscript_FetchErrorSurfaces500 pins fail-closed error
// propagation: a fetch/parse failure is a 500, never a 404 that would
// conflate "broken" with "no captions exist".
func TestGetTranscript_FetchErrorSurfaces500(t *testing.T) {
	fetcher := &stubSubtitleFetcher{err: errors.New("yt-dlp exploded")}
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.transcript = fetcher

	rec := invokeGetTranscript(t, h, "/api/clips/transcript?url=https%3A%2F%2Fyoutu.be%2F506AyzC7d-k")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"ok":true`)
}

// TestGetTranscript_MapsBundleToResponse pins the success mapping: the
// canonical bundle fields (language, provenance, text, cues) project 1:1
// onto the wire shape, the extracted video id is echoed back, and the
// requested window reaches the fetcher verbatim (the cue slice contract).
func TestGetTranscript_MapsBundleToResponse(t *testing.T) {
	fetcher := &stubSubtitleFetcher{bundle: &detail.ResolvedTextBundle{
		LanguageCode:       "it",
		SourceLanguageCode: "it",
		PlainText:          "Ciao, sono Venanzio.\nE questo è il nostro canale.",
		SourceType:         detail.TextSourceYouTubeSubtitle,
		IsOriginal:         true,
		Provider:           "yt-dlp",
		Cues: []detail.TimedCue{
			{StartMs: 1000, EndMs: 2400, Text: "Ciao, sono Venanzio."},
			{StartMs: 2500, EndMs: 4000, Text: "E questo è il nostro canale."},
		},
	}}
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.transcript = fetcher

	rec := invokeGetTranscript(t, h,
		"/api/clips/transcript?url=https%3A%2F%2Fwww.youtube.com%2Fwatch%3Fv%3D506AyzC7d-k&start=60&end=180")

	require.Equal(t, http.StatusOK, rec.Code)

	var resp TranscriptResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	assert.True(t, resp.OK)
	assert.Equal(t, "506AyzC7d-k", resp.VideoID)
	assert.Equal(t, "it", resp.Language)
	assert.Equal(t, "it", resp.SourceLanguage)
	assert.Equal(t, "youtube_subtitle", resp.SourceType)
	assert.True(t, resp.IsOriginal)
	assert.Equal(t, "yt-dlp", resp.Provider)
	assert.Contains(t, resp.Text, "Venanzio")
	assert.Len(t, resp.Cues, 2)
	assert.Equal(t, int64(1000), resp.Cues[0].StartMs)
	assert.Equal(t, 2, resp.CueCount)
	assert.Equal(t, transcriptWindow{StartSec: 60, EndSec: 180}, resp.Window)

	// The port must receive the extracted id + the window verbatim —
	// that is the whole point of the params (cue slicing server-side).
	assert.Equal(t, "506AyzC7d-k", fetcher.gotVideoID)
	assert.Equal(t, 60, fetcher.gotStart)
	assert.Equal(t, 180, fetcher.gotEnd)
}

// TestGetTranscript_DefaultWindowIsZeroZero pins the whole-video
// default: absent start/end params forward 0/0, which the fetcher
// interprets as "no window filter".
func TestGetTranscript_DefaultWindowIsZeroZero(t *testing.T) {
	fetcher := &stubSubtitleFetcher{bundle: &detail.ResolvedTextBundle{PlainText: "full transcript"}}
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.transcript = fetcher

	rec := invokeGetTranscript(t, h, "/api/clips/transcript?url=https%3A%2F%2Fyoutu.be%2F506AyzC7d-k")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, fetcher.gotStart)
	assert.Equal(t, 0, fetcher.gotEnd)

	var resp TranscriptResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, transcriptWindow{StartSec: 0, EndSec: 0}, resp.Window)
}
