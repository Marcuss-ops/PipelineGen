package adapters

import (
	"strconv"
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
	manualImageQueries := ResolveManualSegmentQueries(plan, scriptpkg.CanonicalSegment{ID: segment.SegmentID}, scriptpkg.VidRushProviderInternetImages, mediadomain.SlotSecondaryImage)
	perSceneImages := plan.MediaPlan.Extraction.EntityImages.PerScene()
	imageSearchMode := plan.MediaPlan.Extraction.ImageSearchMode
	if !mediadomain.IsValidImageSearchMode(imageSearchMode) {
		// Unknown modes fail closed to the historical policy rather than
		// silently widening an explicit pin.
		imageSearchMode = ""
	}
	automaticImageSearch := imageSearchMode != mediadomain.ImageSearchModeDisabled &&
		imageSearchMode != mediadomain.ImageSearchModeExplicitOnly
	manualImageExclusive := len(manualImageQueries) > 0
	switch imageSearchMode {
	case mediadomain.ImageSearchModeExplicitOnly:
		manualImageExclusive = true
	case mediadomain.ImageSearchModeAutomatic:
		manualImageExclusive = false
	case mediadomain.ImageSearchModeDisabled:
		manualImageExclusive = false
	}
	imageSidecarRequested := false
	if plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && imageSearchMode != mediadomain.ImageSearchModeDisabled {
		switch imageSearchMode {
		case mediadomain.ImageSearchModeExplicitOnly:
			imageSidecarRequested = len(manualImageQueries) > 0
		case mediadomain.ImageSearchModeAutomatic:
			imageSidecarRequested = perSceneImages || len(segment.Insights.ImageQueries) > 0
		default:
			imageSidecarRequested = len(manualImageQueries) > 0 || perSceneImages || len(segment.Insights.ImageQueries) > 0
		}
	}
	contentType := "video"
	if imageSidecarRequested {
		contentType = "image"
	}
	decision := buildSegmentProviderDecision(plan, segment, contentType)
	if hasCanonicalSourceForSegment(plan, segment.SegmentID) && !imageSidecarRequested {
		// A caller-supplied canonical asset is complete at the source
		// boundary when no still-image sidecar was requested. A stock video
		// does not satisfy an explicit image/identity search request.
		return vidRushFanoutPlan{segmentID: segment.SegmentID, textHash: segment.TextHash, text: segment.Text, title: plan.Title, perQueryLimit: 0}
	}
	imageQueries := []string(nil)
	if automaticImageSearch {
		imageQueries = plausibleSceneImageQueries(segment.Insights.ImageQueries)
	}
	// Explicit searches authored in the media plan define this segment's image
	// scope. Do not mix them with inferred entities or broad semantic queries:
	// their candidates can belong to a different person or story.
	// Explicit media-plan searches are the caller's retrieval intent. Apply
	// them at fanout planning too, but keep them alongside identity-scoped
	// searches. A courthouse/town query cannot retrieve a verified portrait,
	// and replacing the identity queries here silently removed every entity
	// image whenever a caller also supplied contextual image searches.
	if perSceneImages && automaticImageSearch && !manualImageExclusive {
		// Entity-image searches use the canonical person identity. Adding the
		// scene's opening prose created queries like "Chiara Poggi Presenta ..."
		// and let collages/news cards bind as identity portraits. Variant
		// selection happens within the canonical entity pool downstream. Caller
		// supplied contextual searches are appended below and remain distinct.
		if queries := sceneScopedImageQueriesForTypes(segment, plan.MediaPlan.Extraction.EntityImages.EntityTypes); len(queries) > 0 {
			imageQueries = queries
		}
	}
	// Entity-image mode is an explicit narrow surface: preserve only the
	// entity queries emitted by VisualNER. The broad semantic profile ladder
	// is useful for generic scene imagery, but must not leak into an
	// entity-only run because it bypasses entity cache identity and creates
	// dozens of unrelated provider downloads.
	if perSceneImages && automaticImageSearch && !manualImageExclusive {
		// The source-grounded scene query above is the complete retrieval scope;
		// do not collapse it back to an entity-only canonical query. Entity
		// searches still yield one image per identity, so an explicit scene
		// image budget larger than the provider default would silently stop at
		// one frame for a scene with a single person/place. The generic semantic
		// ladder fills the remaining frames AFTER the identity queries, which
		// stay first because they feed the entity cards.
		budget := plan.MediaPlan.Planner.MaxImagesPerScene
		if budget <= vidRushDefaultImagesPerScene {
			budget = plan.ImagesPerScene
		}
		if budget > vidRushDefaultImagesPerScene {
			imageQueries = appendUniqueImageQueries(imageQueries, plausibleSceneImageQueries(scriptpkg.QueriesForImages(profile, budget)))
		}
	} else if automaticImageSearch && !manualImageExclusive && !perSceneImages && !plan.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		for _, query := range plausibleSceneImageQueries(scriptpkg.QueriesForImages(profile, 7)) {
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
	} else if automaticImageSearch && !manualImageExclusive && !perSceneImages {
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
	switch {
	case imageSearchMode == mediadomain.ImageSearchModeDisabled:
		// Disabled means no provider query at all, even when NER or a caller
		// supplied one.
		imageQueries = nil
	case imageSearchMode == mediadomain.ImageSearchModeExplicitOnly,
		imageSearchMode == mediadomain.ImageSearchModeExplicitThenFallback && len(manualImageQueries) > 0,
		imageSearchMode == "" && len(manualImageQueries) > 0:
		// Manual searches are the caller's complete retrieval contract. No
		// inferred entity query, semantic ladder or scene-sentence fallback
		// may widen that scope: doing so retrieves unrelated people or
		// places under the authority of an explicit pin. The per-segment
		// budget does not truncate an explicit list either; the caller
		// authorized every entry.
		imageQueries = append([]string(nil), manualImageQueries...)
	default:
		if imageSearchMode != mediadomain.ImageSearchModeAutomatic {
			imageQueries = appendUniqueImageQueries(imageQueries, manualImageQueries)
		}
		// A complete source sentence is a final provider fallback for scenes
		// whose extracted entity terms are too generic (for example "wide
		// pan"). It is still source-grounded and bounded by the same provider
		// result limit. It is never appended to an explicit-only search.
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
		if maxQueries := plan.MediaPlan.Extraction.MaxImageQueriesPerSegment; maxQueries > 0 && len(imageQueries) > maxQueries {
			imageQueries = imageQueries[:maxQueries]
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

// plausibleSceneImageQueries drops provider queries that cannot describe a
// picture: a one-character token (an article such as the Italian "I") and
// overlay metadata echoed back by the LLM ("Overlay 25 100 1 2"). Both were
// observed retrieving unrelated stock photos that consumed the scene budget.
func plausibleSceneImageQueries(queries []string) []string {
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		query = strings.Join(strings.Fields(strings.TrimSpace(query)), " ")
		if query == "" || len([]rune(query)) < 3 {
			continue
		}
		fields := strings.Fields(query)
		if len(fields) > 1 && strings.EqualFold(fields[0], "overlay") {
			numeric := true
			for _, field := range fields[1:] {
				if _, err := strconv.Atoi(field); err != nil {
					numeric = false
					break
				}
			}
			if numeric {
				continue
			}
		}
		out = append(out, query)
	}
	return out
}

func appendUniqueImageQueries(existing, extra []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(extra))
	for _, query := range existing {
		if key := strings.ToLower(strings.TrimSpace(query)); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, query := range extra {
		query = strings.TrimSpace(query)
		key := strings.ToLower(query)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, query)
	}
	return existing
}

func sceneScopedImageQueriesForTypes(segment scriptpkg.VidRushSegmentResult, entityTypes []string) []string {
	allowed := make(map[string]struct{}, len(entityTypes))
	for _, kind := range entityTypes {
		allowed[normalizeAnnotationType(kind)] = struct{}{}
	}
	if len(allowed) == 0 {
		allowed["PERSON"] = struct{}{}
	}
	queries := make([]string, 0, len(segment.Insights.Entities))
	for _, entity := range segment.Insights.Entities {
		kind := normalizeAnnotationType(entity.Type)
		if _, ok := allowed[kind]; !ok {
			continue
		}
		queries = appendUniqueImageQueries(queries, []string{entity.Value})
	}
	return queries
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
