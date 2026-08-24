// cmd/admin/text_tracks_backfill_checkpoint.go — on-disk resume
// state for the text-tracks-backfill CLI (Fase 5, July 2026).
// Extracted from text_tracks_backfill.go; no behavior change.
package backfill

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

// loadOrInitTextTracksCheckpoint loads a checkpoint from disk
// when --checkpoint is set, or returns a fresh one.
func loadOrInitTextTracksCheckpoint(deps textTracksBackfillDeps) (*textTracksCheckpoint, error) {
	if deps.Checkpoint == "" {
		return nil, nil
	}
	if deps.Resume || deps.RetryFailed {
		data, err := os.ReadFile(deps.Checkpoint)
		if err != nil {
			return nil, fmt.Errorf("--resume/--retry-failed: read checkpoint %q: %w", deps.Checkpoint, err)
		}
		var cp textTracksCheckpoint
		if err := json.Unmarshal(data, &cp); err != nil {
			return nil, fmt.Errorf("--resume/--retry-failed: parse checkpoint %q: %w", deps.Checkpoint, err)
		}
		if cp.JobID == "" {
			return nil, fmt.Errorf("checkpoint %q is missing job_id (corrupt?)", deps.Checkpoint)
		}
		return &cp, nil
	}
	return &textTracksCheckpoint{
		JobID:     fmt.Sprintf("text-tracks-backfill-%s", uuid.NewString()[:8]),
		Source:    deps.Source,
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}
