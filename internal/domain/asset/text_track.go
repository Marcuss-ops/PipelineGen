package asset

// text_track.go defines the domain types for the asset_text_tracks table.
// Each TextTrack is a localized, versioned text resource (transcript,
// description, summary, title, or keywords) attached to a media asset.
//
// Design: one row per (asset_id, language_code, text_kind). Translations
// and re-transcriptions upsert into the same row. The text_hash field
// enables deterministic change detection for source_version computation.

import "time"

// TextTrackKind classifies the type of text content stored in a track.
type TextTrackKind string

const (
	TextTrackTranscript  TextTrackKind = "transcript"
	TextTrackDescription TextTrackKind = "description"
	TextTrackSummary     TextTrackKind = "summary"
	TextTrackTitle       TextTrackKind = "title"
	TextTrackKeywords    TextTrackKind = "keywords"
)

// TextTrackSource records the provenance of a text track — how the text
// was obtained. This drives the resolver's priority chain.
type TextTrackSource string

const (
	TextSourceProvided        TextTrackSource = "provided"
	TextSourceYouTubeSubtitle TextTrackSource = "youtube_subtitle"
	TextSourceWhisper         TextTrackSource = "whisper"
	TextSourceTranslation     TextTrackSource = "translation"
	TextSourceManual          TextTrackSource = "manual"
)

// TextTrackStatus is the tri-state lifecycle of a text track.
// READY  = text content is available and usable.
// PENDING = transcription/translation has been requested but not completed.
// FAILED = transcription/translation failed; may be retried.
type TextTrackStatus string

const (
	TextTrackReady   TextTrackStatus = "READY"
	TextTrackPending TextTrackStatus = "PENDING"
	TextTrackFailed  TextTrackStatus = "FAILED"
)

// TextTrack is the domain model for a single localized text resource
// attached to a media asset. It maps 1:1 to a row in asset_text_tracks.
type TextTrack struct {
	ID                 int64           `json:"id"`
	AssetID            string          `json:"asset_id"`
	LanguageCode       string          `json:"language_code"`
	TextKind           TextTrackKind   `json:"text_kind"`
	TextContent        string          `json:"text_content"`
	SourceType         TextTrackSource `json:"source_type"`
	SourceLanguageCode string          `json:"source_language_code"`
	IsOriginal         bool            `json:"is_original"`
	Provider           string          `json:"provider"`
	ModelName          string          `json:"model_name"`
	ModelVersion       string          `json:"model_version"`
	TextHash           string          `json:"text_hash"`
	SourceVersion      string          `json:"source_version"`
	Confidence         *float64        `json:"confidence,omitempty"`
	Status             TextTrackStatus `json:"status"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}
