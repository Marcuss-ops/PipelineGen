package scriptgeneration

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// finalJobIdempotencyKey stays stable when the same submitted request is
// retried under a new local run id. The remote Master uses this key to return
// the already-prepared render instead of starting a duplicate video job.
// Older/internal callers without a request key retain the run-id behavior.
func finalJobIdempotencyKey(requestKey, runID string) string {
	requestKey = strings.TrimSpace(requestKey)
	if requestKey == "" {
		return "creator-77-" + strings.TrimSpace(runID)
	}
	// 2026-09-28: the digest SSOT (godlike/06) owns the algorithm. This call
	// used to import crypto/sha256 directly, which the archcheck
	// digest_sha256_import_outside_ssot gate rejects; SHA256String returns the
	// same lowercase hex, so the first 32 characters are the exact 16 bytes the
	// previous hex.EncodeToString(sum[:16]) produced.
	return "creator-77-request-" + digest.SHA256String(requestKey)[:32]
}

// finalJobAttemptIdempotencyKey is stable while reconciling one PREPARE
// attempt, and distinct after Master reports a terminal lease.
func finalJobAttemptIdempotencyKey(requestKey, runID string, attempt int) string {
	if attempt < 1 {
		attempt = 1
	}
	return finalJobIdempotencyKey(requestKey, runID) + fmt.Sprintf("-attempt-%d", attempt)
}

// compositeVideoScene preserves source identity in the remote contract. Stock
// footage and caller-provided clips both carry silent video in the `stock`
// media slot, but `kind` determines which pipeline policy owns that video.
// Keep it explicit so stock can never inherit clip subtitle/transcript work.
func compositeVideoScene(id string, index int, kind string, asset map[string]any, durationMS int64, text string) map[string]any {
	ref := make(map[string]any, len(asset))
	for k, v := range asset {
		// Materialized local paths are only for the Creator-side compositor. The
		// worker fetches the certified source_uri over HTTPS.
		if k == "local_path" {
			continue
		}
		ref[k] = v
	}
	if driveID := finalJobStringValue(ref["drive_file_id"]); driveID != "" {
		ref["url"] = driveFileWebLink(driveID)
	}
	scene := map[string]any{"scene_id": id, "index": index, "kind": kind, "duration_seconds": float64(durationMS) / 1000, "stock": ref}
	// PREPARE requires non-empty text on every scene, but stock scene text is
	// rendered as subtitles. Stock footage is intentionally caption-free, so
	// discard any narration/title and satisfy the schema with an invisible
	// separator. Clip scenes retain their real caption when provided.
	if kind == "stock" || strings.TrimSpace(text) == "" {
		text = "\u200b"
	}
	scene["text"] = text
	return scene
}

// The remote renderer requires each scene to be at least 100 ms. Audio and
// source timing can leave a short final remainder (for example, 16 ms) after
// millisecond rounding. Borrow that remainder from preceding scenes while
// keeping every scene above the limit and preserving the total runtime.
func enforceFinalJobMinimumSceneDuration(scenes []map[string]any, minimumMS int64) error {
	if minimumMS <= 0 {
		return fmt.Errorf("final_job minimum scene duration must be positive")
	}
	for i, scene := range scenes {
		seconds, ok := scene["duration_seconds"].(float64)
		if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
			return fmt.Errorf("final_job scene %d has an invalid duration", i)
		}
		durationMS := int64(math.Round(seconds * 1000))
		if durationMS >= minimumMS {
			continue
		}
		deficit := minimumMS - durationMS
		for previous := i - 1; previous >= 0 && deficit > 0; previous-- {
			previousSeconds, valid := scenes[previous]["duration_seconds"].(float64)
			if !valid || math.IsNaN(previousSeconds) || math.IsInf(previousSeconds, 0) {
				return fmt.Errorf("final_job scene %d has an invalid duration", previous)
			}
			previousMS := int64(math.Round(previousSeconds * 1000))
			available := previousMS - minimumMS
			if available <= 0 {
				continue
			}
			take := min(available, deficit)
			scenes[previous]["duration_seconds"] = float64(previousMS-take) / 1000
			deficit -= take
		}
		if deficit > 0 {
			return fmt.Errorf("final_job scene %d is shorter than %dms with no preceding duration to borrow", i, minimumMS)
		}
		scene["duration_seconds"] = float64(minimumMS) / 1000
	}
	return nil
}

// finalJobSegmentWork is one independent fragment preparation: the validated
// source reference, its exact request, and its position in the compiled plan.
type finalJobSegmentWork struct {
	index   int
	source  map[string]any
	request FinalJobVideoSegmentRequest
}

// FinalJobSegmentParallelism is the optional parallelism a preparer declares. A
// preparer that does not implement it is driven one fragment at a time.
type FinalJobSegmentParallelism interface {
	Parallelism() int
}

func finalJobSegmentPrepareError(item finalJobSegmentWork, err error) error {
	return fmt.Errorf(
		"prepare final_job segment %q from asset %q role %q at %dus for %dus: %w",
		item.request.SegmentID,
		finalJobStringValue(item.source["asset_id"]),
		finalJobStringValue(item.source["role"]),
		item.request.SourceInUS,
		item.request.SourceDurationUS,
		err,
	)
}
