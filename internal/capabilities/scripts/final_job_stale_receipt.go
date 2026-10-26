package scriptgeneration

import (
	"errors"
	"strings"

	"go.uber.org/zap"
)

// ErrFinalJobStaleReceipt marks a remote final-job identity the Master will
// never accept another FINALIZE for: a canceled preparation, or a lease held
// elsewhere (`job_already_leased`). The identity lives on the Master, so
// clearing the local receipt does not make it reusable. The receipt carrying
// it is archived as TERMINAL (see settleFinalJob), and the next attempt rotates
// the idempotency key (see finalJobAttemptIdempotencyKey) to prepare a fresh
// remote job instead of replaying the dead one.
var ErrFinalJobStaleReceipt = errors.New("remote final job identity is stale and must be replaced")

// auditFinalJobArtifactReuse makes the retry path's artifact reuse auditable.
// A final-job-only retry must carry the certified script, audio and overlay
// artifacts earlier attempts produced instead of regenerating them; these are
// the exact counts the handoff reuses, so an operator can tell a reuse from a
// regeneration (and a stuck run from a slow one) from the run log alone.
func (r *Runner) auditFinalJobArtifactReuse(runID string, result *GenerateResult) {
	if r == nil || r.log == nil || result == nil {
		return
	}
	scenes := len(result.Scenes)
	localizedRenders := len(result.LocalizedRenders)
	overlayItems, overlayRenders := 0, 0
	if result.OverlayPlan != nil {
		overlayItems = len(result.OverlayPlan.Items)
	}
	if result.OverlayRender != nil {
		overlayRenders = len(result.OverlayRender.Items) + len(result.OverlayRender.Batches)
	}
	finalAudioMS := int64(0)
	finalAudioID := ""
	if result.FinalAudio != nil {
		finalAudioMS = result.FinalAudio.DurationMS
		finalAudioID = strings.TrimSpace(result.FinalAudio.AssetID)
	}
	r.log.Info("final_job artifact reuse",
		zap.String("run_id", runID),
		zap.Int("scenes", scenes),
		zap.String("final_audio_asset_id", finalAudioID),
		zap.Int64("final_audio_ms", finalAudioMS),
		zap.Int("overlay_items", overlayItems),
		zap.Int("overlay_renders", overlayRenders),
		zap.Int("localized_renders", localizedRenders))
}
