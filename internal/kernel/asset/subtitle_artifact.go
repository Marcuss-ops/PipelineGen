package asset

import (
	"context"
	"time"
)

type SubtitleFormat string

const (
	SubtitleFormatASS SubtitleFormat = "ass"
	SubtitleFormatSRT SubtitleFormat = "srt"
	SubtitleFormatVTT SubtitleFormat = "vtt"
)

type SubtitleArtifactStatus string

const (
	SubtitleStatusPending SubtitleArtifactStatus = "PENDING"
	SubtitleStatusReady   SubtitleArtifactStatus = "READY"
	SubtitleStatusFailed  SubtitleArtifactStatus = "FAILED"
	SubtitleStatusStale   SubtitleArtifactStatus = "STALE"
)

type SubtitleArtifact struct {
	ID               int64                  `json:"id"`
	AssetID          string                 `json:"asset_id"`
	TextTrackID      int64                  `json:"text_track_id"`
	LanguageCode     string                 `json:"language_code"`
	Format           SubtitleFormat         `json:"format"`
	LocalPath        string                 `json:"local_path"`
	DriveFileID      string                 `json:"drive_file_id"`
	DriveURL         string                 `json:"drive_url"`
	FileHash         string                 `json:"file_hash"`
	TextHash         string                 `json:"text_hash"`
	CuesHash         string                 `json:"cues_hash"`
	ClipContentHash  string                 `json:"clip_content_hash"`
	CueCount         int                    `json:"cue_count"`
	ClipDurationMs   int64                  `json:"clip_duration_ms"`
	LastCueEndMs     int64                  `json:"last_cue_end_ms"`
	StyleVersion     string                 `json:"style_version"`
	GeneratorVersion string                 `json:"generator_version"`
	Status           SubtitleArtifactStatus `json:"status"`
	IsCurrent        bool                   `json:"is_current"`
	ValidationError  string                 `json:"validation_error"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

type SubtitleArtifactRepository interface {
	Upsert(ctx context.Context, artifact *SubtitleArtifact) error
	FindCurrent(ctx context.Context, assetID string, languageCode string, format SubtitleFormat) (*SubtitleArtifact, error)
	ListByAsset(ctx context.Context, assetID string) ([]SubtitleArtifact, error)
}

func RequiresSubtitles(source string) bool {
	switch source {
	case "youtube", "youtube-manual", "manual", "upload", "clip_drive", "local":
		return true
	default:
		return false
	}
}
