// Package monitor — extraction enqueue port and payloads.
package monitor

import (
	"context"

	channels "github.com/Marcuss-ops/PipelineGen/internal/capabilities/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// ActiveKeyPrefix is the canonical job.ActiveKey prefix for channel-sync extraction jobs.
const ActiveKeyPrefix = "channel_sync_"

// JobEnqueuer emits a youtube_clip.extract job for an analyzed video.
type JobEnqueuer interface {
	EnqueueExtract(ctx context.Context, req EnqueueExtractRequest) error
}

// ExtractionSegment is the monitor-package segment alias for ytdomain.Segment.
type ExtractionSegment = ytdomain.Segment

// ExtractionIntent is the canonical extraction-enqueue intent payload.
type ExtractionIntent struct {
	VideoID       string              `json:"video_id"`
	Title         string              `json:"title"`
	URL           string              `json:"url"`
	Group         string              `json:"group"`
	DriveFolderID string              `json:"drive_folder_id"`
	Segments      []ExtractionSegment `json:"segments"`
	Channel       channels.Channel    `json:"-"`
}

// EnqueueExtractRequest preserves the pre-Fase-8 caller name while resolving to ExtractionIntent.
type EnqueueExtractRequest = ExtractionIntent
