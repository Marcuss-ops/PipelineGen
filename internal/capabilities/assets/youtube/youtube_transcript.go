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
// caller (0/0 = whole video; the values ride the cue slice semantics of
// FetchSegmentSubtitles).
type transcriptWindow struct {
	StartSec int `json:"start_sec"`
	EndSec   int `json:"end_sec"`
}

// TranscriptResponse is the wire shape of GET /api/clips/transcript.
// It is a pure projection of detail.ResolvedTextBundle — no acquisition
// logic lives at the transport layer (the fetcher + canonical VTT parser
// own that; see Service.Subtitles).
type TranscriptResponse struct {
	OK             bool              `json:"ok"`
	VideoID        string            `json:"video_id"`
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

// GetTranscript answers GET /api/clips/transcript?url=...&start=&end= —
// the transcript-as-a-service surface for autonomous agents: readable
// text plus per-cue timings for a YouTube URL WITHOUT downloading the
// video or running Whisper. The work stays where it already was —
// SubtitleFetcherAdapter (yt-dlp --write-subs/--write-auto-subs
// --skip-download) + ParseVTTFile + the acquisition chain's language
// policy — this handler only re-packages the resolved bundle.
//
// Status contract: 400 malformed/non-YouTube url, 503 port not wired
// (fail-closed), 404 no usable captions on the configured languages,
// 500 fetch/parse failure.
func (h *YouTubeClipHandler) GetTranscript(c *gin.Context) {
	videoURL := strings.TrimSpace(c.Query("url"))
	if videoURL == "" {
		apiutil.BadRequest(c, "url parameter is required")
		return
	}
	videoID, err := urlutil.ExtractVideoID(videoURL)
	if err != nil || videoID == "" {
		apiutil.BadRequest(c, "url is not a recognizable YouTube URL")
		return
	}

	startSec, _ := strconv.Atoi(c.Query("start"))
	endSec, _ := strconv.Atoi(c.Query("end"))
	if startSec < 0 {
		startSec = 0
	}
	if endSec < 0 {
		endSec = 0
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
	if bundle.IsEmpty() {
		apiutil.NotFound(c, "no transcript available for this video (no captions on the configured languages)")
		return
	}

	apiutil.OK(c, TranscriptResponse{
		OK:             true,
		VideoID:        videoID,
		Language:       bundle.LanguageCode,
		SourceLanguage: bundle.SourceLanguageCode,
		SourceType:     string(bundle.SourceType),
		IsOriginal:     bundle.IsOriginal,
		Provider:       bundle.Provider,
		Text:           bundle.PlainText,
		Cues:           bundle.Cues,
		CueCount:       len(bundle.Cues),
		Window:         transcriptWindow{StartSec: startSec, EndSec: endSec},
	})
}
