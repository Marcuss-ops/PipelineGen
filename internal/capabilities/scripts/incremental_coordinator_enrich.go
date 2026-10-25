// Package scriptgeneration — incremental_coordinator_enrich.go: the per-scene
// enrichment STAGE CHAIN driven by the incremental coordinator, extracted from
// incremental_coordinator.go to keep that file under
// max_lines_per_file_strict=600 (godlike/08) while giving the §7.3 per-scene
// trace its own home.
//
// The chain is: extraction → canonical identity stamping → research (the
// semantic/NLP work that reads the committed text and produces insights), then
// provider fan-out → acquire/verify/finalize (the asset work that turns those
// insights into certified media). The two blocks are recorded as the scene's
// nlp and asset stages so the critical path can chain nlp → asset exactly as
// the kernel DAG declares.
package scriptgeneration

import (
	"context"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// enrichAndMaterialize runs the full per-scene enrichment chain and records
// the nlp and asset stage lifecycles on the run bound to ctx.
//
// §7.3 trace:
//   - nlp   — readyAt is the committed scene's text-ready instant; the stage
//     covers extraction, identity stamping and research.
//   - asset — chains to the completed analysis (its readyAt) and covers the
//     provider fan-out plus the acquire/verify/finalize stage.
//
// Each entry records the real span the scene waited for that stage, including
// the stage's own bounded-pool waits, and carries the stage error (if any) so
// a failed scene is visible in the trace instead of silently absent.
func (c *VidRushIncrementalCoordinator) enrichAndMaterialize(ctx context.Context, event SceneCommitted, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	// Stage 1 — entity extraction. Single-slot by default (local Ollama),
	// and low-priority against the generation gate so scene generation is
	// never starved when both share the same model.
	nlpStartedAt := time.Now().UTC()
	result, err := c.enrichSegment(ctx, scene)
	if err == nil {
		// Stage 1b — canonical identity stamping: the Image Search Intent
		// resolver's canonical_entity_id choices travel with the segment
		// (never re-derived downstream by the annotation projection).
		result, err = c.stampImageSearchDecision(ctx, result, scene.Text, event.Language)
	}
	if err == nil {
		result, err = c.researchSegment(ctx, result)
	}
	nlpCompletedAt := time.Now().UTC()
	recordSceneStage(ctx, event.SceneID, kernobs.SceneStageNLP, kernobs.SceneStageTimes{
		ReadyAt: event.ReadyAt, StartedAt: nlpStartedAt, CompletedAt: nlpCompletedAt,
	}, "", err)
	if err != nil {
		return result, err
	}

	// Stage 2 — provider fan-out, then acquire/verify/finalize. This is the
	// asset stage: it cannot begin before the analysis that produced the
	// queries is complete.
	assetStartedAt := time.Now().UTC()
	result, err = c.searchProviders(ctx, result)
	if err == nil {
		result, err = c.materializeSegment(ctx, result)
	}
	recordSceneStage(ctx, event.SceneID, kernobs.SceneStageAsset, kernobs.SceneStageTimes{
		ReadyAt: nlpCompletedAt, StartedAt: assetStartedAt, CompletedAt: time.Now().UTC(),
	}, "", err)
	return result, err
}
