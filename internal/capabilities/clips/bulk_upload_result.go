// Package clips (bulk_upload_result) — builds the canonical bulk-upload
// result map returned to the job broker.
//
// Schema invariants:
//   - total / committed / failed — atomic counters
//   - local_folder / drive_folder — payload echoes
//   - failures []string (capped at 50)
//   - total == committed + failed upon job completion
package clips

import (
	"sync/atomic"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
)

func finalizeJobResult(
	total int,
	committed, failed *atomic.Int64,
	failedDetails []string,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
) map[string]any {
	result := map[string]any{
		"total":        total,
		"committed":    committed.Load(),
		"failed":       failed.Load(),
		"local_folder": payload.LocalFolder,
		"drive_folder": payload.DriveFolderID,
	}
	if len(failedDetails) > 0 {
		result["failures"] = failedDetails
	}
	return result
}
