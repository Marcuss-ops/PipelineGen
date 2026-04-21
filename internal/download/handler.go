package download

import (
	"context"
	"encoding/json"
	"fmt"
	"velox/go-master/internal/queue"
)

// DownloadPayload represents the job payload for a download task.
type DownloadPayload struct {
	URL string `json:"url"`
}

// HandleDownloadJob processes a job from the queue.
// It satisfies queue.JobHandler.
func (d *Downloader) HandleDownloadJob(ctx context.Context, msg queue.Message) error {
	var payload DownloadPayload
	
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal download payload: %w", err)
	}
	
	if payload.URL == "" {
		return fmt.Errorf("empty URL in download payload")
	}
	
	_, err := d.Download(ctx, payload.URL)
	return err
}
