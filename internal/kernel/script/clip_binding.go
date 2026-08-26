package script

import (
)

// ClipBinding anchors a scene to a selected YouTube clip. The LLM
// outputs the clip_id; the application layer enriches title and
// Drive link from the resolved clip evidence.
type ClipBinding struct {
	// ClipID is the canonical asset ID of the selected clip.
	ClipID string `json:"clip_id"`

	// ClipTitle is the human-readable clip title, enriched by the
	// application layer from resolved clip evidence.
	ClipTitle string `json:"clip_title,omitempty"`

	// DriveLink is the Google Drive URL, enriched by the application
	// layer from resolved clip evidence.
	DriveLink string `json:"drive_link,omitempty"`

	// SubtitleLink is the canonical Google Drive URL of the ASS artifact
	// associated with this clip.
	SubtitleLink string `json:"subtitle_link,omitempty"`

	// SubtitleFileID is the Drive file ID of the associated ASS artifact.
	SubtitleFileID string `json:"subtitle_file_id,omitempty"`

	// StartMs is the optional clip start offset in milliseconds.
	// Together with EndMs it bounds the selected segment within
	// the underlying clip asset.
	StartMs int64 `json:"start_ms,omitempty"`

	// EndMs is the optional clip end offset in milliseconds.
	// Together with StartMs it bounds the selected segment within
	// the underlying clip asset.
	EndMs int64 `json:"end_ms,omitempty"`

	// DurationMs is the canonical binding-segment-duration
	// surface; "duration unknown" when zero. Populated by
	// the scene planner via scriptpkg.ClipDurationMs (PURE
	// canonical helper) with the canonical caller pattern's
	// scriptpkg.ClipDurationMsFromAssetID fallback. Whole-
	// clip duration is upstream binder's responsibility
	// (godlike/06 SSOT decomposition).
	DurationMs int64 `json:"duration_ms,omitempty"`

	// TotalDurationMs is the measured duration of the complete source asset.
	// It is separate from DurationMs, which is the selected binding segment.
	// Zero means unknown; callers must never substitute the scene duration.
	TotalDurationMs int64 `json:"total_duration_ms,omitempty"`
}
