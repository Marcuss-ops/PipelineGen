package finaljob

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// FinalJobIdempotencyKey stays stable when the same submitted request is
// retried under a new local run id. The remote Master uses this key to return
// the already-prepared render instead of starting a duplicate video job.
// Older/internal callers without a request key retain the run-id behavior.
func FinalJobIdempotencyKey(requestKey, runID string) string {
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

// FinalJobAttemptIdempotencyKey is stable while reconciling one PREPARE
// attempt, and distinct after Master reports a terminal lease or the payload
// itself changes.
//
// The Master hashes the PREPARE payload and answers a reused key with a
// different payload as HTTP 409 idempotency_key_reused (hash_mismatch). The
// attempt counter alone cannot separate those calls: a run that resumes after a
// restart rebuilds its plan from the local render, so a re-encoded overlay moves
// the compiled plan digest while the attempt number stays the same. That 409 was
// terminal — a render the Master was happy to accept became a failed job. The
// plan digest IS the payload identity, so a byte-identical resubmission still
// replays the existing render, and a drifted plan gets its own key instead of
// colliding with the previous one.
func FinalJobAttemptIdempotencyKey(requestKey, runID string, attempt int, planSHA256 string) string {
	if attempt < 1 {
		attempt = 1
	}
	key := FinalJobIdempotencyKey(requestKey, runID) + fmt.Sprintf("-attempt-%d", attempt)
	digest := strings.TrimSpace(planSHA256)
	if digest == "" {
		return key
	}
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return key + "-" + digest
}

// FinalJobRemoteScene is the typed scene envelope of the remote render
// request. It replaces the former map[string]any: every consumer of a scene
// now fails at compile time instead of at runtime on a misspelled key, and no
// JSON round-trip is ever needed to copy or pass one. The JSON tags preserve
// the historical wire keys exactly, so the serialized form of a scene is
// unchanged (pinned by TestCompositeVideoSceneJSONUnchanged).
//
// Stock is the opaque media descriptor the resolver produced (Drive ids, sha,
// duration, geometry); it deliberately stays a map because it is an external
// payload, not a contract field the Producer owns.
type FinalJobRemoteScene struct {
	SceneID         string         `json:"scene_id"`
	Index           int            `json:"index"`
	Kind            string         `json:"kind"`
	DurationSeconds float64        `json:"duration_seconds"`
	Stock           map[string]any `json:"stock"`
	Text            string         `json:"text"`
	// StockPoolSlot marks a scene the stockpool must materialize remotely: it
	// carries no bound file, so the preflight cannot demand a locator.
	StockPoolSlot bool `json:"stock_pool_slot,omitempty"`
}

// CompositeVideoScene preserves source identity in the remote contract. Stock
// footage and caller-provided clips both carry silent video in the `stock`
// media slot, but `kind` determines which pipeline policy owns that video.
// Keep it explicit so stock can never inherit clip subtitle/transcript work.
func CompositeVideoScene(id string, index int, kind string, asset map[string]any, durationMS int64, text string) FinalJobRemoteScene {
	ref := make(map[string]any, len(asset))
	for k, v := range asset {
		// Materialized local paths are only for the Creator-side compositor. The
		// worker fetches the certified source_uri over HTTPS.
		if k == "local_path" {
			continue
		}
		ref[k] = v
	}
	if driveID := FinalJobStringValue(ref["drive_file_id"]); driveID != "" {
		ref["url"] = driveFileWebLink(driveID)
	}
	// PREPARE requires non-empty text on every scene, but stock scene text is
	// rendered as subtitles. Stock footage is intentionally caption-free, so
	// discard any narration/title and satisfy the schema with an invisible
	// separator. Clip scenes retain their real caption when provided.
	if kind == "stock" || strings.TrimSpace(text) == "" {
		text = "\u200b"
	}
	return FinalJobRemoteScene{
		SceneID:         id,
		Index:           index,
		Kind:            kind,
		DurationSeconds: float64(durationMS) / 1000,
		Stock:           ref,
		Text:            text,
	}
}

// The remote renderer requires each scene to be at least 100 ms. Audio and
// source timing can leave a short final remainder (for example, 16 ms) after
// millisecond rounding. Borrow that remainder from preceding scenes while
// keeping every scene above the limit and preserving the total runtime.
func EnforceFinalJobMinimumSceneDuration(scenes []FinalJobRemoteScene, minimumMS int64) error {
	if minimumMS <= 0 {
		return fmt.Errorf("final_job minimum scene duration must be positive")
	}
	for i := range scenes {
		seconds := scenes[i].DurationSeconds
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
			return fmt.Errorf("final_job scene %d has an invalid duration", i)
		}
		durationMS := int64(math.Round(seconds * 1000))
		if durationMS >= minimumMS {
			continue
		}
		deficit := minimumMS - durationMS
		for previous := i - 1; previous >= 0 && deficit > 0; previous-- {
			previousSeconds := scenes[previous].DurationSeconds
			if math.IsNaN(previousSeconds) || math.IsInf(previousSeconds, 0) {
				return fmt.Errorf("final_job scene %d has an invalid duration", previous)
			}
			previousMS := int64(math.Round(previousSeconds * 1000))
			available := previousMS - minimumMS
			if available <= 0 {
				continue
			}
			take := min(available, deficit)
			scenes[previous].DurationSeconds = float64(previousMS-take) / 1000
			deficit -= take
		}
		if deficit > 0 {
			return fmt.Errorf("final_job scene %d is shorter than %dms with no preceding duration to borrow", i, minimumMS)
		}
		scenes[i].DurationSeconds = float64(minimumMS) / 1000
	}
	return nil
}

func FinalJobSceneDurationMS(scenes []FinalJobRemoteScene) int64 {
	var total int64
	for _, scene := range scenes {
		seconds := scene.DurationSeconds
		if !math.IsNaN(seconds) && !math.IsInf(seconds, 0) && seconds > 0 {
			total += int64(math.Round(seconds * 1000))
		}
	}
	return total
}

func TrimFinalJobSceneTail(scenes []FinalJobRemoteScene, surplusMS, minimumMS int64) error {
	if surplusMS <= 0 {
		return nil
	}
	for i := len(scenes) - 1; i >= 0 && surplusMS > 0; i-- {
		seconds := scenes[i].DurationSeconds
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return fmt.Errorf("final_job scene %d has an invalid duration while aligning audio", i)
		}
		durationMS := int64(math.Round(seconds * 1000))
		available := durationMS - minimumMS
		if available <= 0 {
			continue
		}
		trim := min(available, surplusMS)
		scenes[i].DurationSeconds = float64(durationMS-trim) / 1000
		surplusMS -= trim
	}
	if surplusMS > 0 {
		return fmt.Errorf("final_job cannot trim %dms to fit the certified audio without violating minimum scene duration", surplusMS)
	}
	return nil
}
