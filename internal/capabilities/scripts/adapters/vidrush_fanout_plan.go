package adapters

import (
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func hasCanonicalSourceForSegment(plan *scriptpkg.ResolvedGenerationPlan, segmentID string) bool {
	if plan == nil {
		return false
	}
	for _, source := range plan.MediaPlan.Sources {
		if source.SegmentID == segmentID && strings.TrimSpace(source.AssetID) != "" {
			return true
		}
	}
	for _, assignment := range plan.MediaPlan.Assignments {
		if assignment.SegmentID == segmentID && assignment.Locked && strings.TrimSpace(assignment.Asset.AssetID) != "" {
			return true
		}
	}
	return false
}

func buildVidRushFanoutPlan(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult, artlist ArtlistClipSearcher, images InternetImageSearcher, youtube scriptports.VidRushAssetProvider) vidRushFanoutPlan {
	if segment.ExecutionMode.IsFixedMedia() {
		return vidRushFanoutPlan{segmentID: segment.SegmentID, textHash: segment.TextHash, text: segment.Text, title: plan.Title, perQueryLimit: 0}
	}
	profile := segment.CanonicalSemanticProfile()
	decision := buildSegmentProviderDecision(plan, segment, "video")
	if hasCanonicalSourceForSegment(plan, segment.SegmentID) {
		// A caller-supplied canonical asset is complete at the source
		// boundary; no external provider should be queried for this segment.
		return vidRushFanoutPlan{segmentID: segment.SegmentID, textHash: segment.TextHash, text: segment.Text, title: plan.Title, perQueryLimit: 0}
	}
	artlistQueries := scriptpkg.QueriesForArtlist(profile, 5)
	imageQueries := append([]string(nil), segment.Insights.ImageQueries...)
	// Keep source-anchored queries first, then add the canonical profile
	// ladder as a bounded discovery fallback. The materializer still selects
	// exactly the requested number of durable images, while rate-limited or
	// over-specific engines retain enough real candidates to complete fanout.
	for _, query := range scriptpkg.QueriesForImages(profile, 7) {
		duplicate := false
		for _, existing := range imageQueries {
			if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(query)) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			imageQueries = append(imageQueries, query)
		}
	}
	// A complete source sentence is a final provider fallback for scenes whose
	// extracted entity terms are too generic (for example "wide pan"). It is
	// still source-grounded and bounded by the same provider result limit.
	if source := strings.TrimSpace(segment.Text); source != "" && len(imageQueries) >= 3 {
		duplicate := false
		for _, existing := range imageQueries {
			if strings.EqualFold(strings.TrimSpace(existing), source) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			imageQueries = append(imageQueries, source)
		}
	}
	limit := 10
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		limit = plan.MediaPlan.Planner.CandidateLimit
	}
	if limit > 50 {
		limit = 50
	}
	firstEntity := ""
	entities := segment.Insights.Entities
	if len(entities) == 0 {
		entities = profile.Entities
	}
	if len(entities) > 0 {
		firstEntity = strings.TrimSpace(entities[0].Value)
	}
	return vidRushFanoutPlan{
		segmentID: segment.SegmentID, textHash: segment.TextHash, text: segment.Text,
		title: plan.Title, artlistIntentHash: segment.Insights.ArtlistIntentHash,
		artlistQueries: artlistQueries, imageQueries: imageQueries, firstEntity: firstEntity,
		youtubeSources: youtubeSourcesForSegment(plan, segment.SegmentID), perQueryLimit: limit,
		artlistEnabled: effectiveProviderEnabled(plan, decision, scriptpkg.VidRushProviderArtlist) && artlist != nil && len(artlistQueries) > 0,
		imagesEnabled:  effectiveProviderEnabled(plan, decision, scriptpkg.VidRushProviderInternetImages) && images != nil && len(imageQueries) > 0,
		youtubeEnabled: effectiveProviderEnabled(plan, decision, scriptpkg.VidRushProviderYouTube) && youtube != nil,
	}
}
