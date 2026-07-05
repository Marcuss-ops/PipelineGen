// Package clips (bulk_upload_result) — Step "result" of the
// per-clip pipeline. Builds the final JSON result shape returned
// to the broker once all goroutines have completed.
//
// P1.7 (July 2026): extracted from
// internal/application/clips/bulk_upload_worker.go as part of the
// 7-file worker-pipeline split.
//
// The result map is consumed by:
//   - the broker's job-outcome renderer (the JSON becomes part of
//     the canonical job.result envelope),
//   - downstream reconciliation poller screens,
//   - operator dashboards pinning "bulk_upload_youtube_clips"
//     progress.
//
// Schema (preserved verbatim from pre-split code):
//   - total         — int; len(candidates)
//   - uploaded      — int64; count of successful .mp4 publishes
//   - indexed       — int64; count of successful ClipIndexer.IndexClip
//   - qdrant_pushed — int64; count of qdrant pushes (latent
//     unused — preserved for back-compat)
//   - skipped       — int64; count of skipped clips (latent
//     unused — preserved for back-compat)
//   - failed        — int64; count of failed clips
//   - local_folder  — string; payload.LocalFolder (audit echo)
//   - drive_folder  — string; payload.DriveFolderID (audit echo)
//   - failures      — []string (≤50 entries); cap-limited
//     per-clip failure messages
//
// No new abstractions — top-level helper function.
package clips

import (
	"sync/atomic"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// finalizeJobResult builds the canonical result envelope returned
// to the broker by HandleJob. Counters are loaded atomically from
// the per-job atomic counters (mutated by goroutines in the
// HandleJob fan-out). The function is pure — no side effects, no
// logging (caller logs `result` once for the audit trail).
func finalizeJobResult(
	total int,
	uploaded, indexed, pushed, skipped, failed *atomic.Int64,
	failedDetails []string,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
) map[string]any {
	result := map[string]any{
		"total":         total,
		"uploaded":      uploaded.Load(),
		"indexed":       indexed.Load(),
		"qdrant_pushed": pushed.Load(),
		"skipped":       skipped.Load(),
		"failed":        failed.Load(),
		"local_folder":  payload.LocalFolder,
		"drive_folder":  payload.DriveFolderID,
	}
	if len(failedDetails) > 0 {
		result["failures"] = failedDetails
	}
	return result
}
