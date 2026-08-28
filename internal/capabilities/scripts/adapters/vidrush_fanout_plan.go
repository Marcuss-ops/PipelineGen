package adapters

import (
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func buildVidRushFanoutPlan(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult, artlist ArtlistClipSearcher, images InternetImageSearcher, youtube scriptports.VidRushAssetProvider) vidRushFanoutPlan {
	profile := profileFromVidRushSegment(segment)
	artlistQueries := queriesOrProfile(segment.Insights.ArtlistQueries, profileArtlistQueries(profile))
	imageQueries := queriesOrProfile(segment.Insights.ImageQueries, profileImageQueries(profile))
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
		artlistEnabled: plan.MediaPlan.ProviderPolicy.Artlist.AsBool() && artlist != nil && len(artlistQueries) > 0,
		imagesEnabled:  plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && images != nil && len(imageQueries) > 0,
		youtubeEnabled: plan.MediaPlan.ProviderPolicy.YouTube.AsBool() && youtube != nil,
	}
}
