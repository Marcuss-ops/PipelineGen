package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type stubAssetTranscriptSource struct {
	bundle  *detail.ResolvedTextBundle
	found   bool
	err     error
	calls   int
	gotID   string
	gotLang string
	gotFrom int
	gotTo   int
}

func (s *stubAssetTranscriptSource) FetchAssetTranscript(_ context.Context, assetID, language string, startSec, endSec int) (*detail.ResolvedTextBundle, bool, error) {
	s.calls++
	s.gotID = assetID
	s.gotLang = language
	s.gotFrom = startSec
	s.gotTo = endSec
	return s.bundle, s.found, s.err
}

func TestGetTranscript_AssetIDMapsBundle(t *testing.T) {
	stub := &stubAssetTranscriptSource{
		found: true,
		bundle: &detail.ResolvedTextBundle{
			LanguageCode:       "it",
			SourceLanguageCode: "it",
			PlainText:          "Ciao dal clip",
			Cues:               []detail.TimedCue{{StartMs: 1000, EndMs: 2000, Text: "Ciao dal clip"}},
			SourceType:         detail.TextSourceWhisper,
			IsOriginal:         true,
			Provider:           "whisper",
		},
	}
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.assetTranscript = stub

	rec := invokeGetTranscript(t, h, "/api/clips/transcript?asset_id=cliprender_1&language=it&start=1&end=2")
	require.Equal(t, http.StatusOK, rec.Code)

	var resp TranscriptResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.OK)
	assert.Equal(t, "cliprender_1", resp.AssetID)
	assert.Empty(t, resp.VideoID)
	assert.Equal(t, "it", resp.Language)
	assert.Equal(t, "Ciao dal clip", resp.Text)
	assert.Equal(t, 1, resp.CueCount)
	assert.Equal(t, transcriptWindow{StartSec: 1, EndSec: 2}, resp.Window)
	assert.Equal(t, "cliprender_1", stub.gotID)
	assert.Equal(t, "it", stub.gotLang)
	assert.Equal(t, 1, stub.gotFrom)
	assert.Equal(t, 2, stub.gotTo)
}

func TestGetTranscript_AssetIDUnwiredFailsClosed(t *testing.T) {
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)

	rec := invokeGetTranscript(t, h, "/api/clips/transcript?asset_id=cliprender_1")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "asset transcript source not wired")
}

func TestGetTranscript_AssetIDNotFound(t *testing.T) {
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.assetTranscript = &stubAssetTranscriptSource{found: false}

	rec := invokeGetTranscript(t, h, "/api/clips/transcript?asset_id=cliprender_1")
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "no transcript available for this asset")
}

func TestGetTranscript_AssetIDErrorSurfaces500(t *testing.T) {
	h := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)
	h.assetTranscript = &stubAssetTranscriptSource{err: errors.New("ASR unavailable")}

	rec := invokeGetTranscript(t, h, "/api/clips/transcript?asset_id=cliprender_1")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"ok":true`)
}
