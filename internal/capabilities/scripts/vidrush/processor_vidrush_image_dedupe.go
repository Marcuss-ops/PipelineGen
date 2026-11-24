package vidrush

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

// dedupeMaterializedImagesAcrossSegments keeps a selected image identity unique
// across the run. Materialization selects each scene independently, so the same
// durable asset can win the top slot in two scenes. Removing it from the later
// scene alone would silently shrink that scene; instead, refill the slot from
// the later scene's already-materialized candidate pool. Entities whose pool is
// genuinely exhausted keep the smaller set (NO-FAKE-AVAILABILITY).
func dedupeMaterializedImagesAcrossSegments(plan *scriptpkg.ResolvedGenerationPlan, segments []scriptpkg.VidRushSegmentResult) {
	if len(segments) < 2 {
		return
	}
	seen := make(map[string]struct{}, len(segments)*4)
	for i := range segments {
		if _, stockBound := stockBindingForSegment(plan, nil, segments[i]); stockBound {
			// Candidate pools on stock-bound scenes are only a source for entity
			// overlays. They must never be promoted back onto the scene-media
			// surface by the refill pass below.
			clearStockBoundSceneImages(&segments[i])
			continue
		}
		target := vidRushImageTargetForSegment(plan, segments[i])
		if target <= 0 {
			target = len(segments[i].Assets.SecondaryImages)
		}
		kept := make([]scriptpkg.SegmentAssetCandidate, 0, target)
		for _, image := range segments[i].Assets.SecondaryImages {
			key := vidRushCandidateIdentity(image)
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			kept = append(kept, image)
		}
		if len(kept) < target {
			for _, candidate := range segments[i].Assets.Candidates {
				key := vidRushCandidateIdentity(candidate)
				if key == "" {
					continue
				}
				if _, exists := seen[key]; exists {
					continue
				}
				// Only a materialized candidate can replace a selected image:
				// the overlay needs the durable Drive/content address.
				if candidate.DriveLink == "" || len(candidate.ContentSHA256) != 64 {
					continue
				}
				seen[key] = struct{}{}
				kept = append(kept, candidate)
				if len(kept) >= target {
					break
				}
			}
		}
		segments[i].Assets.SecondaryImages = kept
		segments[i].Assets.GeneratedImages = filterVidRushGeneratedImages(kept)
	}
}
