package adapters

import (
	"context"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// FinalizeVidRushBindingsWithCache is the canonical binding finalizer used by
// the generation use case. The in-memory map remains a fast L1 cache, while
// cache provides the durable L2 replay surface across process restarts.
// Cache failures are deliberately non-fatal: they must never turn a valid,
// already-persisted binding into a false failure or a false cache hit.
func FinalizeVidRushBindingsWithCache(ctx context.Context, segments []scriptpkg.VidRushSegmentResult, forceRefresh bool, cache scriptports.VidRushCachePort) []scriptpkg.VidRushSegmentResult {
	out := make([]scriptpkg.VidRushSegmentResult, 0, len(segments))
	segmentIndex := make(map[string]int, len(segments))
	boundAssetOwners := make(map[string]string)
	artlistContext, artlistContextErr := newArtlistIsolationContext(segments)
	for _, original := range segments {
		seg := CloneVidRushSegmentResult(original)
		normalizeVidRushSegmentAssets(&seg)
		if seg.ExecutionMode.IsFixedMedia() {
			// Fixed media is already authoritative. Do not filter, rank,
			// deduplicate or rewrite its existing binding surface.
			out = append(out, seg)
			continue
		}
		valid := make([]scriptpkg.SegmentAssetCandidate, 0, len(seg.Assets.Candidates))
		seen := make(map[string]struct{}, len(seg.Assets.Candidates))
		artlistSegmentValid := artlistContextErr == nil
		if artlistSegmentValid {
			artlistSegmentValid = validateArtlistQueriesForSegment(artlistContext, seg) == nil
		}
		for _, candidate := range seg.Assets.Candidates {
			if !validVidRushCandidate(candidate) || !readyVidRushCandidate(candidate) {
				continue
			}
			// Candidates are untrusted provider output, including replayed
			// durable-cache rows. Never rebind a candidate stamped for another
			// segment during finalization.
			if owner := strings.TrimSpace(candidate.SegmentID); owner != "" && owner != strings.TrimSpace(seg.SegmentID) {
				continue
			}
			if candidate.Position != seg.Position ||
				(strings.TrimSpace(candidate.TextHash) != "" && strings.TrimSpace(candidate.TextHash) != strings.TrimSpace(seg.TextHash)) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderArtlist) {
				if !artlistSegmentValid || validateArtlistCandidateForContext(artlistContext, candidate, seg) != nil {
					// A contaminated Artlist candidate is never eligible for
					// ranking or winner selection, even if it is otherwise
					// technically durable.
					continue
				}
			}
			key := strings.ToLower(strings.TrimSpace(candidate.AssetID))
			if owner, exists := boundAssetOwners[key]; exists && owner != seg.SegmentID {
				// The same provider asset may not be rebound to another
				// segment. Drop the later binding rather than leaking it.
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			boundAssetOwners[key] = seg.SegmentID
			valid = append(valid, candidate)
		}
		seg.Assets.Candidates = valid
		selectedImages := make([]scriptpkg.SegmentAssetCandidate, 0, len(seg.Assets.SecondaryImages))
		for _, image := range seg.Assets.SecondaryImages {
			if owner := strings.TrimSpace(image.SegmentID); owner != "" && owner != strings.TrimSpace(seg.SegmentID) {
				continue
			}
			if image.Position != seg.Position ||
				(strings.TrimSpace(image.TextHash) != "" && strings.TrimSpace(image.TextHash) != strings.TrimSpace(seg.TextHash)) {
				continue
			}
			selectedImages = append(selectedImages, image)
		}
		seg.Assets.SecondaryImages = preserveSelectedVidRushImages(selectedImages, valid)
		seg.Assets.GeneratedImages = filterVidRushGeneratedImages(seg.Assets.SecondaryImages)
		// Binding finalization validates and persists discovered candidates. It
		// deliberately does not choose a winner; MediaSampler owns selection.
		if len(seg.Assets.SecondaryImages) > 0 {
			// Image-only plans have no primary video by design. The durable,
			// rights-verified secondary image set is nevertheless the scene's
			// definitive VidRush binding and must be surfaced as such.
			seg.Assets.SelectionReason = "highest scored provenance-valid secondary images for image fallback"
		} else {
			seg.Assets.PrimaryVideo = nil
			seg.Assets.SelectionReason = "no provenance-valid candidate available"
		}
		seg.Assets.CandidateSetHash = candidateSetHash(valid)
		for i := range seg.Assets.Candidates {
			seg.Assets.Candidates[i].CandidateSetHash = seg.Assets.CandidateSetHash
		}
		bindingKey := segmentCacheKey("binding", seg.SegmentID, seg.TextHash, seg.Assets.CandidateSetHash, "vidrush-binding-v1")
		if len(valid) == 0 {
			seg.Cache.Binding = "BYPASSED"
		} else if !forceRefresh {
			_, l1Hit := cacheLoad(vidrushBindingCache, bindingKey)
			l2Hit, _ := loadVidRushPersistentJSON(ctx, cache, "binding", bindingKey, new(bool))
			if l1Hit || l2Hit {
				seg.Cache.Binding = "HIT_EXACT"
			} else {
				seg.Cache.Binding = "MISS"
				cacheStore(vidrushBindingCache, bindingKey, true)
				_ = storeVidRushPersistentJSON(ctx, cache, "binding", bindingKey, true)
			}
		} else {
			seg.Cache.Binding = "REFRESHED"
			cacheStore(vidrushBindingCache, bindingKey, true)
			_ = storeVidRushPersistentJSON(ctx, cache, "binding", bindingKey, true)
		}
		if existing, ok := segmentIndex[seg.SegmentID]; ok && strings.TrimSpace(seg.SegmentID) != "" {
			// Multiple provider processors may return the same segment delta.
			// Collapse it here so the durable response has one authoritative
			// segment and cannot expose duplicate keyword/provider bindings.
			out[existing] = mergeVidRushSegmentResult(out[existing], seg)
			continue
		}
		if strings.TrimSpace(seg.SegmentID) != "" {
			segmentIndex[seg.SegmentID] = len(out)
		}
		out = append(out, seg)
	}
	return out
}
