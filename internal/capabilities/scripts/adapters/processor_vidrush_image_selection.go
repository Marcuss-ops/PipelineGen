// Package adapters — processor_vidrush_image_selection.go: the exact-image
// selection helpers of the VidRush materialization processor
// (extracted 2026-09-12 from processor_vidrush_materialization.go to keep
// both halves under max_lines_per_file_strict=600, godlike/08).
package adapters

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const vidRushDefaultImagesPerScene = 2

// vidRushPerceptualDuplicateDistance is the maximum dHash Hamming distance at
// which two materialized images are treated as the same visual. 5 of 64 bits
// absorbs re-encoding, watermarking and small crops while keeping distinct
// photographs apart.
const vidRushPerceptualDuplicateDistance = 5

func vidRushImageTarget(plan *scriptpkg.ResolvedGenerationPlan) int {
	if plan == nil {
		return 0
	}
	if plan.ImagesPerScene > 0 {
		return plan.ImagesPerScene
	}
	if plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() || plan.MediaPlan.ProviderPolicy.ImageGeneration.AsBool() {
		return vidRushDefaultImagesPerScene
	}
	return 0
}

// vidRushImageTargetForSegment raises the generic scene-image target to the
// number of distinct imageable entities requested by the NLP surface. Entity
// overlays are an explicit per-entity contract: the scene default of two
// images must not silently truncate a 3-person (or 5-person) extraction.
func vidRushImageTargetForSegment(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) int {
	target := vidRushImageTarget(plan)
	if plan == nil || !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		return target
	}
	maxPerEntity := plan.MediaPlan.Extraction.EntityImages.MaxPerEntity
	if maxPerEntity <= 0 {
		maxPerEntity = 1
	}
	allowed := map[string]struct{}{"PERSON": {}, "LOGO": {}, "ORG": {}, "ORGANIZATION": {}}
	if len(plan.MediaPlan.Extraction.EntityImages.EntityTypes) > 0 {
		allowed = make(map[string]struct{}, len(plan.MediaPlan.Extraction.EntityImages.EntityTypes))
		for _, raw := range plan.MediaPlan.Extraction.EntityImages.EntityTypes {
			allowed[strings.ToUpper(strings.TrimSpace(raw))] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	for _, entity := range segment.Insights.Entities {
		kind := strings.ToUpper(strings.TrimSpace(entity.Type))
		if _, ok := allowed[kind]; !ok {
			continue
		}
		name := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(entity.Value)), " "))
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	if len(seen) == 0 {
		for name, canonicalID := range segment.Insights.ImageEntityCanonicalIDs {
			canonical := strings.ToLower(strings.TrimSpace(canonicalID))
			if (strings.HasPrefix(canonical, "person:") || strings.HasPrefix(canonical, "brand:") || strings.HasPrefix(canonical, "org:")) && strings.TrimSpace(name) != "" {
				seen[strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))] = struct{}{}
			}
		}
	}
	entityTarget := len(seen) * maxPerEntity
	if entityTarget > target {
		return entityTarget
	}
	return target
}

// dropPerceptualDuplicates removes candidates whose perceptual hash is within
// vidRushPerceptualDuplicateDistance of an earlier, already-kept candidate.
// Two providers frequently return the same syndicated photo under different
// URLs; the identity-keyed dedup upstream cannot see that because the bytes
// only exist after materialization. Candidates without a perceptual hash (for
// example catalog-hydrated or cache-replayed rows) pass through untouched, so
// the filter is fail-open.
func dropPerceptualDuplicates(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	kept := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		hash := strings.TrimSpace(candidate.PerceptualHash)
		if hash != "" {
			duplicate := false
			for _, seen := range kept {
				if distance, ok := digest.PerceptualHashDistance(hash, seen); ok && distance <= vidRushPerceptualDuplicateDistance {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			kept = append(kept, hash)
		}
		out = append(out, candidate)
	}
	return out
}

func durableVidRushImages(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider != scriptpkg.VidRushProviderInternetImages && candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
			continue
		}
		if readyVidRushCandidate(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

// vidRushImageGroup is the identity used by image-only selection. The entity
// image policy may permit multiple distinct images for one query; otherwise
// the default remains one image per entity/query.
func vidRushImageGroup(candidate scriptpkg.SegmentAssetCandidate) string {
	group := strings.ToLower(strings.TrimSpace(candidate.Query))
	if group == "" {
		group = strings.ToLower(strings.TrimSpace(candidate.Entity))
	}
	if group == "" {
		group = "asset:" + strings.ToLower(strings.TrimSpace(candidate.AssetID))
	}
	return group
}

// selectExactVidRushImages is the final selected-image projection. In an
// Images-only plan, internet_images is the complete provider allowlist and
// image_generation is deliberately excluded. Candidates are grouped by their
// entity (or query when the provider did not return an entity), keeping the
// highest-scored durable result from each group so the selected set contains
// at most one image per entity.
func selectExactVidRushImages(candidates []scriptpkg.SegmentAssetCandidate, target int, plan *scriptpkg.ResolvedGenerationPlan) []scriptpkg.SegmentAssetCandidate {
	if target <= 0 {
		return nil
	}
	images := dropPerceptualDuplicates(durableVidRushImages(candidates))
	imagesOnly := plan != nil &&
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderInternetImages) &&
		!providerEnabledForVidRush(plan, scriptpkg.VidRushProviderArtlist) &&
		!providerEnabledForVidRush(plan, scriptpkg.VidRushProviderYouTube) &&
		!providerEnabledForVidRush(plan, scriptpkg.VidRushProviderImageGeneration)
	if !imagesOnly {
		return images[:min(target, len(images))]
	}

	selected := make([]scriptpkg.SegmentAssetCandidate, 0, min(target, len(images)))
	perGroupLimit := 1
	if plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() && plan.MediaPlan.Extraction.EntityImages.MaxPerEntity > 1 {
		perGroupLimit = plan.MediaPlan.Extraction.EntityImages.MaxPerEntity
	}
	groupCounts := make(map[string]int, target)
	seenAssets := make(map[string]struct{}, target)
	for _, candidate := range images {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		group := vidRushImageGroup(candidate)
		if groupCounts[group] >= perGroupLimit {
			// Candidate order is discovery order. Semantic selection belongs to
			// MediaSampler and must not be reconstructed in this boundary.
			continue
		}
		assetID := strings.ToLower(strings.TrimSpace(candidate.AssetID))
		if _, exists := seenAssets[assetID]; exists {
			continue
		}
		groupCounts[group]++
		seenAssets[assetID] = struct{}{}
		selected = append(selected, candidate)
	}
	if len(selected) > target {
		selected = selected[:target]
	}
	return selected
}

func prioritizeExactVidRushImageCandidates(candidates []scriptpkg.SegmentAssetCandidate, target int, plan *scriptpkg.ResolvedGenerationPlan) []scriptpkg.SegmentAssetCandidate {
	if target <= 0 || plan == nil ||
		!providerEnabledForVidRush(plan, scriptpkg.VidRushProviderInternetImages) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderArtlist) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderYouTube) ||
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderImageGeneration) {
		return candidates
	}
	groups := make([][]scriptpkg.SegmentAssetCandidate, 0, target)
	groupIndex := make(map[string]int, target)
	others := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			others = append(others, candidate)
			continue
		}
		group := vidRushImageGroup(candidate)
		index, exists := groupIndex[group]
		if !exists {
			index = len(groups)
			groupIndex[group] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], candidate)
	}
	ordered := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for round := 0; ; round++ {
		added := false
		for _, group := range groups {
			if round >= len(group) {
				continue
			}
			ordered = append(ordered, group[round])
			added = true
		}
		if !added {
			break
		}
	}
	return append(ordered, others...)
}
