package adapters

import (
	"strings"
	"unicode/utf8"

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
	case mediadomain.ImageSearchModeAutomatic, mediadomain.ImageSearchModeExplicitThenFallback:
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
	if entityImageLaneRequested(perSceneImages, automaticImageSearch, manualImageExclusive) {
		// Entity-image searches use the canonical person identity. Adding the
		// scene's opening prose created queries like "Chiara Poggi Presenta ..."
		// and let collages/news cards bind as identity portraits. Variant
		// selection happens within the canonical entity pool downstream. This
		// fan-out can run before VisualNER has populated the segment; in that
		// case an empty identity list must stay empty and let the later image
		// stage search after extraction. Retaining the earlier contextual
		// fallback here turns the whole narration into an image query and admits
		// unrelated icons/photos into an entity-only lane.
		imageQueries = sceneScopedImageQueriesForTypes(segment, plan.MediaPlan.Extraction.EntityImages.EntityTypes)
	}
	switch {
	case imageSearchMode == mediadomain.ImageSearchModeDisabled:
		// Disabled means no provider query at all, even when NER or a caller
		// supplied one.
		imageQueries = nil
	case imageSearchMode == mediadomain.ImageSearchModeExplicitOnly,
		imageSearchMode == "" && len(manualImageQueries) > 0:
		// Manual searches are the caller's complete retrieval contract. No
		// inferred entity query, semantic ladder or scene-sentence fallback
		// may widen that scope: doing so retrieves unrelated people or
		// places under the authority of an explicit pin. The per-segment
		// budget does not truncate an explicit list either; the caller
		// authorized every entry.
		imageQueries = append([]string(nil), manualImageQueries...)
	default:
		// Automatic identity/context discovery and caller-authored context
		// searches are independent lanes. Only explicit_only grants the caller
		// authority to suppress entity-derived queries.
		imageQueries = appendUniqueImageQueries(imageQueries, manualImageQueries)
		if maxQueries := plan.MediaPlan.Extraction.MaxImageQueriesPerSegment; maxQueries > 0 && len(imageQueries) > maxQueries {
			imageQueries = imageQueries[:maxQueries]
		}
	}
	// Final identity-scope guard. The incremental fan-out can be planned while
	// semantic extraction is still in flight, and the automatic fallback above
	// can append the whole narration once it has several identity queries. An
	// entity-image surface must never inherit that contextual/narrative query:
	// search only identities already grounded on this segment, then let the
	// later image stage handle identities that become available after extraction.
	if automaticImageSearch && !manualImageExclusive &&
		(perSceneImages || plan.MediaPlan.Extraction.EntityImageSurfaceEnabled()) {
		imageQueries = sceneScopedImageQueriesForTypes(segment, plan.MediaPlan.Extraction.EntityImages.EntityTypes)
		imageQueries = appendUniqueImageQueries(imageQueries, manualImageQueries)
	}
	limit := 10
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		limit = plan.MediaPlan.Planner.CandidateLimit
	}
	if automaticImageSearch && !manualImageExclusive &&
		(perSceneImages || plan.MediaPlan.Extraction.EntityImageSurfaceEnabled()) {
		limit = internetImagePerQueryLimit(limit, len(imageQueries))
	}
	if limit > 50 {
		limit = 50
	}
	firstEntity := ""
	entities := segment.Insights.Entities
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
func entityImageLaneRequested(perSceneImages, automaticImageSearch, manualImageExclusive bool) bool {
	return perSceneImages && automaticImageSearch && !manualImageExclusive
}

func contextualImageLaneRequested(perSceneImages, automaticImageSearch, manualImageExclusive bool) bool {
	return automaticImageSearch && !manualImageExclusive && !perSceneImages
}

func plausibleSceneImageQueries(queries []string) []string {
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" || utf8.RuneCountInString(query) < 2 {
			continue
		}
		lower := strings.ToLower(query)
		if strings.HasPrefix(lower, "overlay ") || strings.HasPrefix(lower, "overlay:") {
			continue
		}
		out = appendUniqueImageQueries(out, []string{query})
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
		// An empty policy means the canonical imageable set, not PERSON-only:
		// GPE/ORG entities are legitimate entity cards and must be searched.
		for _, kind := range scriptpkg.AnnotationEntityKinds() {
			allowed[normalizeAnnotationType(kind)] = struct{}{}
		}
	}
	queries := make([]string, 0, len(segment.Insights.Entities))
	for _, entity := range segment.Insights.Entities {
		kind := normalizeAnnotationType(entity.Type)
		if _, ok := allowed[kind]; !ok {
			continue
		}
		queries = appendUniqueImageQueries(queries, entity.SearchIntents)
	}
	return plausibleSceneImageQueries(queries)
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
			name := canonicalImageEntityName(segment, entity.Value)
			if name == "" {
				name = entity.Value
			}
			add(name)
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
