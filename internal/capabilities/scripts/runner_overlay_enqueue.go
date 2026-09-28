// Package scriptgeneration — runner_overlay_enqueue.go owns the overlay.prepare
// enqueue seam: deciding which OverlayIntents actually carry asset bytes worth
// prefetching and submitting the fire-and-forget prepare job for the run.
// Split out of runner.go per the max_lines_per_file_strict gate.
package scriptgeneration

import (
	"context"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// enqueueOverlayPrepare builds the overlay.prepare job for the run's
// pre-timing OverlayIntents and submits it (fire-and-forget). The plan id is
// the run id — the same idempotency key the overlay plan uses — so a retry
// never double-prepares.
func (r *Runner) enqueueOverlayPrepare(ctx context.Context, runID string, req GenerateRequest, intents []capabilityoverlay.OverlayIntent) error {
	if r.overlayPrepareEnqueuer == nil || len(intents) == 0 {
		return nil
	}
	if !overlayPrepareNeedsAssetPrefetch(intents) {
		// Text/phrase-only intents have no asset bytes for RenderingGen to
		// resolve or prefetch. The final timing-frozen compile resolves their
		// templates directly, so an overlay.prepare queue round-trip would only
		// add lease/IPC latency to the run.
		return nil
	}
	canvas := r.overlayCanvas.withDefaults()
	// overlay.prepare is a business stage boundary: the enqueue itself is
	// measured on the canonical Run clock so the run's critical path shows
	// the prepare submit wall time (which runs in parallel with TTS) instead
	// of hiding it inside the generate phase.
	if _, err := kernobs.MeasureStageReport(ctx, StageOverlayPrepare, func(stageCtx context.Context) error {
		return r.overlayPrepareEnqueuer.EnqueuePrepare(stageCtx, capabilityoverlay.PrepareRequest{
			SchemaVersion: capabilityoverlay.SchemaVersionPrepare,
			PlanID:        runID,
			VideoID:       runID,
			ProjectID:     strings.TrimSpace(req.Project),
			Width:         canvas.Width,
			Height:        canvas.Height,
			FPSNum:        canvas.FPSNum,
			FPSDen:        canvas.FPSDen,
			Intents:       intents,
		})
	}); err != nil {
		return err
	}
	return nil
}

func overlayPrepareNeedsAssetPrefetch(intents []capabilityoverlay.OverlayIntent) bool {
	for _, intent := range intents {
		if len(intent.AssetRefs) > 0 || len(intent.Payload.AssetRefs) > 0 {
			return true
		}
	}
	return false
}
