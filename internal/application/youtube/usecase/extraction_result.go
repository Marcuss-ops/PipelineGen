// Package usecase — extraction_result.go: pure aggregator + success
// classifier for an extraction fan-out run.
//
// PR-GODOBJ-1 (July 2026): Aggregoators extracted from extraction_service.go
// per godlike/06 SSOT (one canonical owner per fact: stats + classification
// live ONLY here). Side-effect free so they can be unit-tested without
// the full ExtractionService fixture.
package usecase

import (
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
)

// classifyExtractionRun is the canonical success/failure classifier
// for an extraction run. PR-C YouTube Cutover Commit 2/6 (Correttezza
// #7) corrected the success criterion: a 100% cache-hit re-run now
// classifies as success (the legacy form required processed > 0 which
// flagged all-skipped re-runs as failure).
//
// Returns true when the run is successful:
//   - Vacuously true when no segments were requested (Requested == 0).
//   - True when no segment failed AND (processed + skipped) == requested.
//
// Returns false when any segment failed (Failed > 0) OR the
// processed+skipped+failed accounting does not sum to requested
// (defensive: a counter that drifts is itself a fail-closed signal).
func classifyExtractionRun(stats *youtubetypes.ExtractStats) bool {
	if stats == nil {
		return false
	}
	if stats.Requested == 0 {
		return true
	}
	if stats.Failed > 0 {
		return false
	}
	return stats.Processed+stats.Skipped == stats.Requested
}

// aggregateFanOutStats walks the slice of per-segment items and stamps
// the canonical ExtractStats counters. The skipped counter is
// defensively recomputed as `len(items) - processed - failed` so a
// counter drift never produces a wrong sum (godlike/07 no fake-availability).
func aggregateFanOutStats(items []youtubetypes.ExtractItem) youtubetypes.ExtractStats {
	stats := youtubetypes.ExtractStats{Requested: len(items)}
	for _, item := range items {
		switch item.Status {
		case "processed", "processed_but_index_blocked":
			stats.Processed++
		case "skipped":
			stats.Skipped++
		case "failed":
			stats.Failed++
		default:
			// Unknown or empty status must fail closed. The fan-out
			// should surface the offending item as failed rather than
			// inventing a cache-hit skip.
			stats.Failed++
		}
	}
	return stats
}

// buildInitialResponse constructs the response envelope used by both
// the canonical fan-out path and the empty-segment short-circuit in
// Extract(). Items is pre-allocated to len(segments) so the
// append-in-extractFanOut path is O(1) amortized.
func buildInitialResponse(req *youtubetypes.ExtractRequest, segments []youtubetypes.Segment, videoID, driveFolderID, driveFolderPath string) *youtubetypes.ExtractResponse {
	return &youtubetypes.ExtractResponse{
		OK:              true,
		SourceURL:       req.URL,
		VideoID:         videoID,
		DriveFolderID:   driveFolderID,
		DriveFolderPath: driveFolderPath,
		Stats: &youtubetypes.ExtractStats{
			Requested: len(segments),
		},
		Items: make([]youtubetypes.ExtractItem, 0, len(segments)),
	}
}
