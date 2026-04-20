package clipsearch

import (
	"encoding/json"
	"errors"
)

var ErrYouTubeAlreadyDownloaded = errors.New("youtube candidate already downloaded")

type TranscriptSegment struct {
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
	Text     string  `json:"text"`
}

type SelectedMoment struct {
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
	Reason   string  `json:"reason"`
	Score    float64 `json:"score,omitempty"`
	Source   string  `json:"source,omitempty"`
}

type YouTubeClipMetadata struct {
	VideoID            string
	VideoURL           string
	Title              string
	Channel            string
	Uploader           string
	ViewCount          int64
	DurationSec        float64
	UploadDate         string
	Description        string
	SearchQuery        string
	Relevance          int
	Transcript         string
	TranscriptVTT      string
	TranscriptSegments []TranscriptSegment
	SelectedMoment     *SelectedMoment
}

func (m *YouTubeClipMetadata) toJSON() string {
	if m == nil {
		return ""
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return ""
	}
	return string(raw)
}
