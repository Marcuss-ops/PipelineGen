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
	// Entity-image mode is an explicit narrow surface: preserve only the
	// entity queries emitted by VisualNER. The broad semantic profile ladder
	// is useful for generic scene imagery, but must not leak into an
	// entity-only run because it bypasses entity cache identity and creates
	// dozens of unrelated provider downloads.
	if !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
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
	} else {
		// The image-search resolver also emits visual-concept queries. In an
		// entity-image run those concepts are outside the requested contract:
		// retain only queries that resolve to a PERSON extracted from this
		// segment, so generic imagery cannot consume the bounded materialization
		// budget before the requested person has a durable image.
		personQueries := make(map[string]struct{})
		for _, entity := range segment.Insights.Entities {
			if normalizeAnnotationType(entity.Type) != "PERSON" {
				continue
			}
			name := normalizeEntityMatch(trimEnglishPossessive(entity.Value))
			if name != "" {
				personQueries[name] = struct{}{}
			}
		}
		for query, canonicalID := range segment.Insights.ImageEntityCanonicalIDs {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(canonicalID)), "person:") {
				if name := normalizeEntityMatch(trimEnglishPossessive(query)); name != "" {
					personQueries[name] = struct{}{}
				}
			}
		}
		filtered := make([]string, 0, len(imageQueries))
		seenPersonQueries := make(map[string]struct{})
		for _, query := range imageQueries {
			if _, ok := personQueries[normalizeEntityMatch(trimEnglishPossessive(query))]; ok {
				filtered = append(filtered, query)
				seenPersonQueries[strings.ToLower(strings.TrimSpace(query))] = struct{}{}
			}
		}
		// Preserve distinct source surfaces (straight and curly apostrophe,
		// for example) because semantic certification counts extracted PERSON
		// surfaces and expects one image query per surface.
		for _, entity := range segment.Insights.Entities {
			if normalizeAnnotationType(entity.Type) != "PERSON" {
				continue
			}
			query := strings.TrimSpace(entity.Value)
			if query == "" {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(query))
			if _, ok := seenPersonQueries[key]; ok {
				continue
			}
			if _, ok := personQueries[normalizeEntityMatch(trimEnglishPossessive(query))]; ok {
				filtered = append(filtered, query)
				seenPersonQueries[key] = struct{}{}
			}
		}
		if len(filtered) > 0 {
			imageQueries = filtered
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

func trimEnglishPossessive(value string) string {
	value = strings.TrimSpace(value)
	for _, suffix := range []string{"'s", "’s"} {
		if len(value) > len(suffix) && strings.EqualFold(value[len(value)-len(suffix):], suffix) {
			return strings.TrimSpace(value[:len(value)-len(suffix)])
		}
	}
	return value
}
