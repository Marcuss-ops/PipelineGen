package adapters

import (
	"net/url"
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
		if kind == "PERSON" {
			// Keep portrait retrieval on a dedicated identity-focused query.
			// SearchIntents may include scene context (e.g. a program or book),
			// which correctly belongs to the separate contextual-image lane; a
			// bare name would let collages/news cards bind as portraits.
			name := strings.TrimSpace(entity.Value)
			if name != "" {
				queries = appendUniqueImageQueries(queries, []string{name + " portrait"})
			}
			continue
		}
		queries = appendUniqueImageQueries(queries, entity.SearchIntents)
	}
	return plausibleSceneImageQueries(queries)
}

// vidRushForbiddenProviders lists provider values that MUST be rejected
// regardless of provenance. This gate enforces the VidRush provider separation
var vidRushForbiddenProviders = map[string]bool{
	"youtube":             true,
	"generated_images":    true,
	"local_youtube_stock": true,
	"local_stock":         true,
}

// vidRushForbiddenURLPatterns lists URL substrings that disqualify a candidate
// even when the provider field is not directly "youtube".
var vidRushForbiddenURLPatterns = []string{
	"youtube-nocookie.com",
	"youtube.com",
	"youtu.be",
}

func validVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	if strings.TrimSpace(candidate.AssetID) == "" || strings.TrimSpace(candidate.Provider) == "" || candidate.Score < 0 {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	if provider == scriptpkg.VidRushProviderYouTube {
		return strings.TrimSpace(candidate.AssetID) != "" && strings.TrimSpace(candidate.SourceURL) != "" && candidate.SourceEndMs > candidate.SourceStartMs
	}
	if vidRushForbiddenProviders[provider] {
		return false
	}
	// Generated assets are accepted only through the lifecycle-aware
	// image.generate.google path. A legacy remote URL must never masquerade as
	// a generated, durable artifact.
	if provider == scriptpkg.VidRushProviderImageGeneration && candidate.IsLegacyCandidate() {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(candidate.RightsStatus), "rejected") {
		return false
	}
	sourceURL := strings.ToLower(strings.TrimSpace(candidate.SourceURL))
	if provider != scriptpkg.VidRushProviderYouTube {
		for _, pattern := range vidRushForbiddenURLPatterns {
			if strings.Contains(sourceURL, pattern) {
				return false
			}
		}
	}
	// Selection is an HTTP(S) boundary. A data:, file:, javascript: or
	// scheme-relative locator is never a provider image, even when the
	// provider field is correct.
	if source := strings.TrimSpace(candidate.SourceURL); source != "" && !isAbsoluteHTTPURL(source) {
		return false
	}
	if preview := strings.TrimSpace(candidate.PreviewURL); preview != "" && !isAbsoluteHTTPURL(preview) {
		return false
	}
	return isAbsoluteHTTPURL(candidate.SourceURL) || isAbsoluteHTTPURL(candidate.PreviewURL)
}

func isAbsoluteHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func readyVidRushCandidate(candidate scriptpkg.SegmentAssetCandidate) bool {
	return readyVidRushCandidateWithMode(candidate, false)
}

// readyVidRushCandidateWithMode is the single readiness predicate for every
// provider. allowURLFallback selects the explicit durable-vs-local mode used
// by verifiedImageCandidateWithoutDrive: a verified content address may bind
// its original URL when the durable Drive copy is unavailable. The normal
// binding path never accepts a URL-only candidate.
func readyVidRushCandidateWithMode(candidate scriptpkg.SegmentAssetCandidate, allowURLFallback bool) bool {
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	if provider == scriptpkg.VidRushProviderYouTube {
		return youtubeCandidateReady(candidate)
	}
	// Lifecycle-aware candidates are fail-closed. Legacy candidates remain
	// readable during the migration window, but only when a durable Drive
	// location proves they were actually persisted.
	if candidate.IsLegacyCandidate() {
		return strings.TrimSpace(candidate.DriveLink) != ""
	}
	if provider == scriptpkg.VidRushProviderInternetImages {
		return internetImageCandidateReady(candidate, allowURLFallback)
	}
	if allowURLFallback {
		return false
	}
	return candidate.ReadyForBinding() && strings.TrimSpace(candidate.ContentSHA256) != "" && strings.TrimSpace(candidate.DriveLink) != ""
}

// youtubeCandidateReady is the one YouTube readiness rule. The historical
// implementation carried four near-identical branches with subtly different
// case handling; one predicate keeps the contract testable.
func youtubeCandidateReady(candidate scriptpkg.SegmentAssetCandidate) bool {
	if strings.TrimSpace(candidate.AssetID) == "" || strings.TrimSpace(candidate.DriveLink) == "" {
		return false
	}
	rights := strings.TrimSpace(candidate.RightsStatus)
	if rights != "" && !strings.EqualFold(rights, "verified") {
		return false
	}
	if strings.EqualFold(candidate.AcquisitionStatus, scriptpkg.VidRushStatusAcquired) &&
		strings.EqualFold(candidate.VerificationStatus, scriptpkg.VidRushStatusVerified) &&
		strings.EqualFold(candidate.PersistenceStatus, scriptpkg.VidRushStatusPersisted) &&
		vidRushIndexReady(candidate.IndexStatus) {
		return true
	}
	// Legacy YouTube candidates can carry a ready local/Drive binding without
	// the newer lifecycle stamps. ReadyForBinding remains the authority for
	// that migration window, but a Drive copy is still mandatory.
	return candidate.ReadyForBinding() && strings.TrimSpace(candidate.DriveLink) != ""
}

// internetImageCandidateReady is the one internet-image readiness rule.
// unknown_allowed is sufficient for the technical retrieval contract; only an
// explicit rejection blocks binding. The durable path requires persistence,
// index projection and a Drive copy; the URL fallback requires the verified
// content address and the original locator instead.
func internetImageCandidateReady(candidate scriptpkg.SegmentAssetCandidate, allowURLFallback bool) bool {
	rights := strings.TrimSpace(candidate.RightsStatus)
	if strings.EqualFold(rights, "rejected") {
		return false
	}
	if rights != "" && !strings.EqualFold(rights, "verified") && !strings.EqualFold(rights, "unknown_allowed") {
		return false
	}
	if !strings.EqualFold(candidate.AcquisitionStatus, scriptpkg.VidRushStatusAcquired) ||
		!strings.EqualFold(candidate.VerificationStatus, scriptpkg.VidRushStatusVerified) ||
		strings.TrimSpace(candidate.ContentSHA256) == "" {
		return false
	}
	if allowURLFallback {
		return (strings.TrimSpace(candidate.SourceURL) != "" || strings.TrimSpace(candidate.PreviewURL) != "") &&
			strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), "accepted")
	}
	durable := strings.EqualFold(candidate.PersistenceStatus, scriptpkg.VidRushStatusPersisted) &&
		vidRushIndexReady(candidate.IndexStatus) &&
		strings.TrimSpace(candidate.DriveLink) != ""
	if durable {
		return true
	}
	// Older lanes can record a usable Drive copy with a non-canonical index
	// status (for example "queued"). The lifecycle predicate remains the
	// authority; the fallback only accepts a candidate that already proves it
	// is ready for binding and has both a content address and a Drive copy.
	return candidate.ReadyForBinding() &&
		strings.TrimSpace(candidate.ContentSHA256) != "" &&
		strings.TrimSpace(candidate.DriveLink) != ""
}

func vidRushIndexReady(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "indexed", "pending", "discovered", "indexing_skipped_no_indexer":
		return true
	default:
		return false
	}
}

func preserveSelectedVidRushImages(selected, valid []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	if len(selected) == 0 {
		return filterVidRushImages(valid)
	}
	validByIdentity := make(map[string]scriptpkg.SegmentAssetCandidate, len(valid))
	for _, candidate := range valid {
		if candidate.Provider == scriptpkg.VidRushProviderImageGeneration {
			continue
		}
		validByIdentity[selectedCandidateIdentity(candidate)] = candidate
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, candidate := range selected {
		key := selectedCandidateIdentity(candidate)
		if _, exists := seen[key]; exists {
			continue
		}
		if _, ok := validByIdentity[key]; ok {
			// The selected record is the stable asset identity. The validation
			// pass may return a refreshed projection of the same candidate, but
			// silently swapping the selected struct (asset id/Drive link) made
			// finalization observe a different image than selection chose.
			out = append(out, candidate)
			seen[key] = struct{}{}
		}
	}
	if len(out) > 0 {
		return out
	}
	if len(selected) > 0 {
		// A non-empty selection that did not survive validation is a real
		// failure, not permission to swap in a different asset. Replacing it
		// with the validator's projection made finalization silently bind an
		// image selection never chose.
		return nil
	}
	return filterVidRushImages(valid)
}

// selectedCandidateIdentity is the discovery identity used to preserve the
// selected asset across finalization. DriveLink is deliberately excluded: it
// is assigned by the persistence step and may change between selection and
// finalization, while AssetID and the source locators identify the image the
// selector actually chose.
func selectedCandidateIdentity(candidate scriptpkg.SegmentAssetCandidate) string {
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	for _, value := range []string{candidate.AssetID, candidate.SourceURL, candidate.PreviewURL, candidate.DriveLink} {
		if value = strings.TrimSpace(value); value != "" {
			return provider + "\x00" + strings.ToLower(value)
		}
	}
	return ""
}

func filterVidRushImages(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider != scriptpkg.VidRushProviderImageGeneration {
			out = append(out, candidate)
		}
	}
	return out
}

func filterVidRushGeneratedImages(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider == scriptpkg.VidRushProviderImageGeneration {
			out = append(out, candidate)
		}
	}
	return out
}

func candidateSetHash(candidates []scriptpkg.SegmentAssetCandidate) string {
	// The hash is the discovery identity of the set. Acquisition, verification,
	// persistence and index statuses mutate after discovery, so including them
	// made the same candidate set hash differently across lifecycle stages.
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, strings.Join([]string{
			candidate.AssetID, candidate.Provider, candidate.Query, candidate.Entity,
			candidate.SourceURL, candidate.PreviewURL,
		}, "\x00"))
	}
	return segmentCacheKey(parts...)
}
