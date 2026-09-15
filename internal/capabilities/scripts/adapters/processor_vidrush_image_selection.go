// Package adapters — processor_vidrush_image_selection.go: the exact-image
// selection helpers of the VidRush materialization processor
// (extracted 2026-09-12 from processor_vidrush_materialization.go to keep
// both halves under max_lines_per_file_strict=600, godlike/08).
package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const vidRushDefaultImagesPerScene = 2

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
	allowed := map[string]struct{}{"PERSON": {}}
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
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(canonicalID)), "person:") && strings.TrimSpace(name) != "" {
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

// vidRushImageGroup is the identity used by image-only selection: one durable
// image per entity/query, rather than several catalog rows for the same name.
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
	images := durableVidRushImages(candidates)
	imagesOnly := plan != nil &&
		providerEnabledForVidRush(plan, scriptpkg.VidRushProviderInternetImages) &&
		!providerEnabledForVidRush(plan, scriptpkg.VidRushProviderArtlist) &&
		!providerEnabledForVidRush(plan, scriptpkg.VidRushProviderYouTube) &&
		!providerEnabledForVidRush(plan, scriptpkg.VidRushProviderImageGeneration)
	if !imagesOnly {
		return images[:min(target, len(images))]
	}

	selected := make([]scriptpkg.SegmentAssetCandidate, 0, min(target, len(images)))
	seenGroups := make(map[string]int, target)
	seenAssets := make(map[string]struct{}, target)
	for _, candidate := range images {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		group := vidRushImageGroup(candidate)
		if _, exists := seenGroups[group]; exists {
			// Candidate order is discovery order. Semantic selection belongs to
			// MediaSampler and must not be reconstructed in this boundary.
			continue
		}
		assetID := strings.ToLower(strings.TrimSpace(candidate.AssetID))
		if _, exists := seenAssets[assetID]; exists {
			continue
		}
		seenGroups[group] = len(selected)
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
