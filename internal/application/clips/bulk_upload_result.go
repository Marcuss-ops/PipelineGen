// Package clips (bulk_upload_result) — Step "result" of the
// per-clip pipeline. Builds the final JSON result shape returned
// to the broker once all goroutines have completed.
//
// P1.7 (July 2026): extracted from
// internal/application/clips/bulk_upload_worker.go as part of the
// 7-file worker-pipeline split.
//
// PR-13 (July 2026): the result map was slimmed to the canonical
// 3 counters (uploaded / committed / failed) + audit echoes
// (local_folder / drive_folder) + the failures cap. Retired the
// pre-PR-13 always-zero fields (indexed / qdrant_pushed / skipped).
//
// Schema:
//
//   - total         — int; len(candidates)
//   - uploaded      — int64; count of successful .mp4 publishes
//   - committed     — int64; count of asset+outbox tx commits
//     (≤ uploaded by design; canonical QDRANT-002
//     ordering: publish → register → commit)
//   - failed        — int64; count of failed clips
//   - local_folder  — string; payload.LocalFolder (audit echo)
//   - drive_folder  — string; payload.DriveFolderID (audit echo)
//   - failures      — []string (≤50 entries); cap-limited
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
