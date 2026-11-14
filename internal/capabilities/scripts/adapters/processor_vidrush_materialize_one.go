package adapters

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// materializeOne materializes one enriched segment: it acquires, verifies and
// finalizes every candidate through the shared provider registry and common
// finalizer, applies the generation fallback, and selects the primary video.
// It is shared by the batch Process path and the single-segment Materialize
// port so the materialization stage is implemented exactly once.
func (p *VidRushMaterializationProcessor) materializeOne(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (vidRushMaterializedSegment, error) {
	updated := CloneVidRushSegmentResult(segment)
	if segment.ExecutionMode.IsFixedMedia() {
		// Fixed media is already authoritative and must not be acquired,
		// verified, persisted, ranked or replaced by this processor.
		updated.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
		return vidRushMaterializedSegment{result: updated}, nil
	}
	run := &vidRushMaterializationRun{
		processor:          p,
		plan:               plan,
		segment:            segment,
		perSceneImageScope: plan != nil && plan.MediaPlan.Extraction.EntityImages.PerScene(),
	}

	// Search candidates must be acquired and verified before deciding how
	// many generated images are actually missing. This keeps generation a
	// true fallback instead of a parallel source that duplicates valid web
	// assets.
	imageTarget := vidRushImageTargetForSegment(plan, segment)
	materializationImageTarget := imageTarget
	if plan != nil && plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() && imageTarget > 0 {
		// Materialize the current demand plus one alternate for later scenes.
		// Discovery keeps its larger candidate pool; downloading a five-image
		// library synchronously on every run delayed the current video.
		materializationImageTarget = imageTarget * entityImageCatalogMaterializationCopies
	} else if run.perSceneImageScope && imageTarget > 0 {
		// Keep one extra verified per-scene candidate as a uniqueness fallback.
		// Independent searches can return the same syndicated photo in two
		// scenes; the overlay planner chooses the next materialized image by
		// content hash so the selected images are genuinely distinct.
		materializationImageTarget++
	}
	discoveredCandidates := prioritizeExactVidRushImageCandidates(updated.Assets.Candidates, imageTarget, plan)
	// Same surface decision as the target multiplication above: a payload that
	// asks for persons/entities through `include` (the r20minuti shape) has no
	// legacy entity_images toggle, so reading the toggle here skipped the
	// durable pool while the target was still multiplied for it. The pool must
	// follow the surface, not the deprecated spelling.
	if p.catalog != nil && plan != nil && plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		// Attach the durable per-person pool before acquisition so
		// verified-but-never-downloaded catalog rows get a real
		// Acquire → Verify → Finalize attempt in-run instead of arriving
		// after materialization, when they can no longer be fetched.
		if pooled := durableEntityImagePoolForSegment(ctx, p.catalog, updated); len(pooled) > 0 {
			discoveredCandidates = AppendProviderCandidatesUnique(discoveredCandidates, pooled)
		}
	}
	var materializeErr error
	updated.Assets.Candidates, materializeErr = run.materialize(ctx, discoveredCandidates, materializationImageTarget)
	if materializeErr != nil {
		return vidRushMaterializedSegment{}, materializeErr
	}
	generationCandidates, generationState := p.planGenerationFallback(plan, updated)
	updated.Cache.ImageGeneration = generationState
	if len(generationCandidates) > 0 {
		// Only the newly planned fallback candidates need a second
		// acquisition pass. Replaying the already attempted web candidates
		// here would duplicate downloads after a generation fallback.
		generated, materializeErr := run.materialize(ctx, generationCandidates, len(generationCandidates))
		if materializeErr != nil {
			return vidRushMaterializedSegment{}, materializeErr
		}
		updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, generated)
	}
	materialized := updated.Assets.Candidates
	updated.Assets.SecondaryImages = selectExactVidRushImages(materialized, imageTarget, plan, updated)
	logVidRushSelectedImages(p.log, segment.SegmentID, updated.Assets.SecondaryImages)
	updated.Assets.GeneratedImages = filterVidRushGeneratedImages(updated.Assets.SecondaryImages)
	if imageTarget > 0 && len(updated.Assets.SecondaryImages) != imageTarget {
		run.warnings = append(run.warnings, fmt.Sprintf(
			"FAILED_REQUIRED_IMAGE_COUNT: required=%d verified=%d segment=%s",
			imageTarget, len(updated.Assets.SecondaryImages), segment.SegmentID,
		))
	}
	profile := updated.CanonicalSemanticProfile()
	if p.sampler != nil {
		updated.Assets.PrimaryVideo = p.selectPrimaryWithMediaSampler(ctx, materialized, profile)
	} else {
		// Selection is fail-closed when the canonical sampler is absent. The
		// legacy VidRush ranker is not a production fallback; wiring must
		// provide MediaSampler before a primary can be bound.
		updated.Assets.PrimaryVideo = nil
	}
	updated.Assets.CandidateSetHash = candidateSetHash(materialized)
	// Numeric new-upload counter: 0 when the catalog Drive materialization is
	// reused, 1 per freshly finalized internet_images candidate otherwise.
	updated.Cache.InternetImagesNewUploads = run.newInternetImageUploads
	return vidRushMaterializedSegment{result: updated, warnings: run.warnings}, nil
}
