package youtube

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// transcriptWindow echoes the requested [start, end] window back to the
// caller (0/0 = whole asset/video; the values ride the cue slice semantics of
// the source fetcher).
type transcriptWindow struct {
	StartSec int `json:"start_sec"`
	EndSec   int `json:"end_sec"`
}

// TranscriptResponse is the wire shape of GET /api/clips/transcript.
// It is a pure projection of detail.ResolvedTextBundle — no acquisition
// logic lives at the transport layer.
//
// Exactly one of VideoID (url=...) or AssetID (asset_id=...) is set. The
// asset path is the non-YouTube fallback: READY text track first, streaming
// Whisper ASR when no track exists.
type TranscriptResponse struct {
	OK             bool              `json:"ok"`
	VideoID        string            `json:"video_id,omitempty"`
	AssetID        string            `json:"asset_id,omitempty"`
	Language       string            `json:"language"`
	SourceLanguage string            `json:"source_language,omitempty"`
	SourceType     string            `json:"source_type"`
	IsOriginal     bool              `json:"is_original"`
	Provider       string            `json:"provider,omitempty"`
	Text           string            `json:"text"`
	Cues           []detail.TimedCue `json:"cues"`
	CueCount       int               `json:"cue_count"`
	Window         transcriptWindow  `json:"window"`
}

// GetTranscript answers GET /api/clips/transcript.
//
//   - ?url=https://youtube...&start=&end= — the YouTube transcript-as-a-service
//     surface (yt-dlp --skip-download + canonical VTT parser).
//   - ?asset_id=cliprender_...&language=&start=&end= — a canonical media asset:
//     reuse a READY text track, otherwise materialize the asset and run the
//     same streaming Whisper resolver as clip.render.
//
// Status contract: 400 malformed/missing source, 503 port not wired
// (fail-closed), 404 no usable transcript, 500 acquisition/materialization/ASR
// failure. A silent 200 with empty text is never returned.
func (h *YouTubeClipHandler) GetTranscript(c *gin.Context) {
	startSec, endSec := transcriptWindowFromQuery(c)

	assetID := strings.TrimSpace(c.Query("asset_id"))
	if assetID != "" {
		h.getAssetTranscript(c, assetID, startSec, endSec)
		return
	}

	videoURL := strings.TrimSpace(c.Query("url"))
	if videoURL == "" {
		apiutil.BadRequest(c, "url or asset_id parameter is required")
		return
	}
	videoID, err := urlutil.ExtractVideoID(videoURL)
	if err != nil || videoID == "" {
		apiutil.BadRequest(c, "url is not a recognizable YouTube URL")
		return
	}

	if h.transcript == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "transcript source not wired (subtitle fetcher unavailable)")
		return
	}

	bundle, err := h.transcript.FetchSegmentSubtitles(c.Request.Context(), videoID, startSec, endSec)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	if bundle == nil || bundle.IsEmpty() {
		apiutil.NotFound(c, "no transcript available for this video (no captions on the configured languages)")
		return
	}

	apiutil.OK(c, newTranscriptResponse("", videoID, bundle, startSec, endSec))
}

// getAssetTranscript is the non-YouTube path: it never pretends a non-YouTube
// asset is a YouTube video, and it fails closed when the asset resolver is not
// wired. Language is optional; when omitted the resolver prefers the original
// READY track and otherwise lets Whisper detect it.
func (h *YouTubeClipHandler) getAssetTranscript(c *gin.Context, assetID string, startSec, endSec int) {
	if h.assetTranscript == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "asset transcript source not wired (text track/ASR unavailable)")
		return
	}
	language := strings.TrimSpace(c.Query("language"))
	bundle, found, err := h.assetTranscript.FetchAssetTranscript(c.Request.Context(), assetID, language, startSec, endSec)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	if !found || bundle == nil || bundle.IsEmpty() {
		apiutil.NotFound(c, "no transcript available for this asset (no READY text track and ASR found no speech)")
		return
	}
	apiutil.OK(c, newTranscriptResponse(assetID, "", bundle, startSec, endSec))
}

func transcriptWindowFromQuery(c *gin.Context) (int, int) {
	startSec, _ := strconv.Atoi(c.Query("start"))
	endSec, _ := strconv.Atoi(c.Query("end"))
	if startSec < 0 {
		startSec = 0
	}
	if endSec < 0 {
		endSec = 0
	}
	return startSec, endSec
}

func newTranscriptResponse(assetID, videoID string, bundle *detail.ResolvedTextBundle, startSec, endSec int) TranscriptResponse {
	return TranscriptResponse{
		OK:             true,
		VideoID:        videoID,
		AssetID:        assetID,
		Language:       bundle.LanguageCode,
		SourceLanguage: bundle.SourceLanguageCode,
		SourceType:     string(bundle.SourceType),
		IsOriginal:     bundle.IsOriginal,
		Provider:       bundle.Provider,
		Text:           bundle.PlainText,
		Cues:           bundle.Cues,
		CueCount:       len(bundle.Cues),
		Window:         transcriptWindow{StartSec: startSec, EndSec: endSec},
	}
}
