package adapters

import (
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
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

func buildVidRushFanoutPlan(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult, images InternetImageSearcher, youtube scriptports.VidRushAssetProvider) vidRushFanoutPlan {
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
	imageQueries := append([]string(nil), segment.Insights.ImageQueries...)
	// Explicit media-plan searches are the caller's retrieval intent. Apply
	// them at fanout planning too, so certification and the later materializer
	// search the same per-scene image subjects.
	manualImageQueries := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: segment.SegmentID}, scriptpkg.VidRushProviderInternetImages, mediadomain.SlotSecondaryImage)
	if len(manualImageQueries) > 0 {
		imageQueries = manualImageQueries
	}
	perSceneImages := plan.MediaPlan.Extraction.EntityImages.PerScene()
	if len(manualImageQueries) > 0 {
		// The explicit image queries above are already scene scoped.
	} else if perSceneImages {
		// Entity-image searches use the canonical person identity. Adding the
		// scene's opening prose created queries like "Chiara Poggi Presenta ..."
		// and let collages/news cards bind as identity portraits. Variant
		// selection happens within the canonical entity pool downstream.
		if queries := sceneScopedImageQueries(segment); len(queries) > 0 {
			imageQueries = queries
		}
	}
	// Entity-image mode is an explicit narrow surface: preserve only the
	// entity queries emitted by VisualNER. The broad semantic profile ladder
	// is useful for generic scene imagery, but must not leak into an
	// entity-only run because it bypasses entity cache identity and creates
	// dozens of unrelated provider downloads.
	if perSceneImages {
		// The source-grounded scene query above is the complete retrieval scope;
		// do not collapse it back to an entity-only canonical query.
	} else if !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
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
		// Identity-image mode only materializes explicit person/brand/org
		// queries; numeric/value overlays and generic concepts cannot consume
		// the verified image budget.
		identityQueries := make(map[string]struct{})
		for _, entity := range segment.Insights.Entities {
			kind := normalizeAnnotationType(entity.Type)
			if kind != "PERSON" && kind != "LOGO" && kind != "ORG" {
				continue
			}
			name := normalizeEntityMatch(trimEnglishPossessive(entity.Value))
			if name != "" {
				identityQueries[name] = struct{}{}
			}
		}
		for query, canonicalID := range segment.Insights.ImageEntityCanonicalIDs {
			canonical := strings.ToLower(strings.TrimSpace(canonicalID))
			if strings.HasPrefix(canonical, "person:") || strings.HasPrefix(canonical, "brand:") || strings.HasPrefix(canonical, "org:") {
				if name := normalizeEntityMatch(trimEnglishPossessive(query)); name != "" {
					identityQueries[name] = struct{}{}
				}
			}
		}
		filtered := make([]string, 0, len(imageQueries))
		seenQueries := make(map[string]struct{})
		for _, query := range imageQueries {
			key := normalizeEntityMatch(trimEnglishPossessive(query))
			if _, ok := identityQueries[key]; ok {
				filtered = append(filtered, query)
				seenQueries[strings.ToLower(strings.TrimSpace(query))] = struct{}{}
			}
		}
		for _, entity := range segment.Insights.Entities {
			kind := normalizeAnnotationType(entity.Type)
			if kind != "PERSON" && kind != "LOGO" && kind != "ORG" {
				continue
			}
			query := strings.TrimSpace(entity.Value)
			key := strings.ToLower(query)
			if query == "" {
				continue
			}
			if _, ok := seenQueries[key]; ok {
				continue
			}
			if _, ok := identityQueries[normalizeEntityMatch(trimEnglishPossessive(query))]; ok {
				filtered = append(filtered, query)
				seenQueries[key] = struct{}{}
			}
		}
		imageQueries = filtered
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
		title: plan.Title, imageQueries: imageQueries, firstEntity: firstEntity,
		youtubeSources: youtubeSourcesForSegment(plan, segment.SegmentID), perQueryLimit: limit,
		imagesEnabled:  effectiveProviderEnabled(plan, decision, scriptpkg.VidRushProviderInternetImages) && images != nil && len(imageQueries) > 0,
		youtubeEnabled: effectiveProviderEnabled(plan, decision, scriptpkg.VidRushProviderYouTube) && youtube != nil,
	}
}

// sceneScopedImageQueries retains every imageable person in the scene. The
// singular helper remains for callers that need a lead identity, while the
// media fanout must search each distinct person so a two-person scene cannot
// silently lose its second subject.
func sceneScopedImageQueries(segment scriptpkg.VidRushSegmentResult) []string {
	queries := make([]string, 0, len(segment.Insights.Entities)+len(segment.Insights.ImageEntityCanonicalIDs))
	seen := make(map[string]struct{}, cap(queries))
	add := func(raw string) {
		name := strings.TrimSpace(raw)
		key := normalizeEntityMatch(name)
		if name == "" || key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		queries = append(queries, name)
	}
	for _, entity := range segment.Insights.Entities {
		if normalizeAnnotationType(entity.Type) == "PERSON" {
			add(entity.Value)
		}
	}
	if len(queries) == 0 {
		for surface, canonicalID := range segment.Insights.ImageEntityCanonicalIDs {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(canonicalID)), "person:") {
				add(surface)
			}
		}
	}
	return queries
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
