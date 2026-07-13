// Package clips (bulk_upload_result) — builds the canonical bulk-upload
// result map returned to the job broker.
//
// Schema invariants:
//   - total / uploaded / committed / failed — atomic counters
//   - local_folder / drive_folder — payload echoes
//   - failures []string (capped at 50)
//   - committed ≤ uploaded by design (publish-first ordering, single
//     EnqueueAndIndex tx drives the gap when an upload isn't followed
//     by a successful commit)
package clips

import (
	"sync/atomic"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

func finalizeJobResult(
	total int,
	uploaded, committed, failed *atomic.Int64,
	failedDetails []string,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
) map[string]any {
	result := map[string]any{
		"total":        total,
		"uploaded":     uploaded.Load(),
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
