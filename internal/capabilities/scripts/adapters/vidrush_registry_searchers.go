package adapters

import (
	"context"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// this file sat past the godlike/08 strict line cap with the gate inline.

// VidRushRegistryMediaResolver is the single provider-registry discovery
// adapter. It implements both discovery ports consumed by the fan-out; it
// never acquires, scores, ranks or selects a winner.
type VidRushRegistryMediaResolver struct {
	Registry *VidRushAssetProviderRegistry
}

const vidRushMaxParallelImageSearches = 15

// One process-wide limiter bounds requests across all concurrently enriched
// scenes and all portrait-query variants; per-scene limits cannot multiply
// provider traffic without bound.
var vidRushImageSearchSlots = make(chan struct{}, vidRushMaxParallelImageSearches)

// Bound whole-query workers too: a query may expand into several provider
// variants, while all actual provider calls remain under the shared search cap.
var vidRushImageQuerySlots = make(chan struct{}, vidRushMaxParallelImageSearches)

func (s *VidRushRegistryMediaResolver) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if s == nil || s.Registry == nil {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	// Search only the query produced for this segment/entity. Expanding it here
	// made the result depend on unrelated case-specific hardcoding and caused
	// cross-story images to enter otherwise correctly scoped searches.
	queries := []string{req.Query}
	resultSets := make([][]scriptpkg.SegmentAssetCandidate, len(queries))
	queryErrors := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, query := range queries {
		i, query := i, query
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case vidRushImageSearchSlots <- struct{}{}:
			case <-ctx.Done():
				queryErrors[i] = ctx.Err()
				return
			}
			defer func() { <-vidRushImageSearchSlots }()
			found, err := s.Registry.Search(ctx, scriptpkg.VidRushProviderInternetImages, scriptports.VidRushSearchRequest{
				SegmentID: req.SegmentID, Position: req.Position, TextHash: req.TextHash, Text: query,
				Query: query, Entity: req.Entity, QueryClass: req.QueryClass, Language: req.Language,
				ForceRefresh: req.ForceRefresh, Limit: req.Limit,
			})
			resultSets[i], queryErrors[i] = found, err
		}()
	}
	wg.Wait()
	var lastErr error
	for _, err := range queryErrors {
		if err != nil {
			lastErr = err
		}
	}
	limit := req.Limit
	if len(queries) > 1 && limit < 5 {
		// The per-scene fanout limit can be lower than the explicit per-entity
		// portrait request. Keep enough distinct candidates to satisfy five
		// variants after merging the bounded contextual searches.
		limit = 5
	}
	// Round-robin by depth, deduped once: appending through
	// AppendProviderCandidatesUnique per single candidate cloned the whole
	// result and rebuilt its identity map on every item, which is O(N²) in the
	// number of candidates.
	var candidates []scriptpkg.SegmentAssetCandidate
	seen := make(map[string]struct{})
	for depth := 0; ; depth++ {
		addedAtDepth := false
		for _, results := range resultSets {
			if depth >= len(results) {
				continue
			}
			addedAtDepth = true
			candidate := results[depth]
			key := vidRushCandidateIdentity(candidate)
			if key == "" {
				continue
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate)
			if limit > 0 && len(candidates) >= limit {
				return candidates[:limit], nil
			}
		}
		if !addedAtDepth {
			break
		}
	}
	if len(candidates) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return candidates, nil
}

// VidRushProviderFanout resolves a single enriched segment's visual providers
// in parallel through the shared searcher ports (which dispatch the canonical
// provider registry). It owns only the concurrency and the candidate merge; the
// provider-specific search, rate limiting and retry stay in the searchers, so
// no provider orchestration is duplicated for the incremental path.
// one segment and merges the winning candidates back into an immutable result.
// Required providers fail closed; best-effort providers never turn an
// unavailable backend into a silent successful empty result (the searchers
// already return typed errors, and their absence is reflected in the result).
func (f *VidRushProviderFanout) ResolveProviders(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	updated := CloneVidRushSegmentResult(segment)
	if plan == nil {
		return updated, nil
	}
	if segment.ExecutionMode.IsFixedMedia() {
		// Fixed media is authoritative. Preserve its existing binding and do
		// not invoke any provider, catalog, query builder or ranker.
		updated.ExecutionMode = scriptpkg.SceneExecutionFixedMedia
		updated.Cache.InternetImages = "BYPASSED"
		updated.Cache.YouTube = "BYPASSED"
		return updated, nil
	}
	_, stockBound := stockBindingForSegment(plan, nil, segment)
	if stockBound {
		// A stock binding owns the scene's video. The explicit entity-image
		// policy may still fetch verified portrait overlays; keep every video
		// provider disabled on this sidecar path.
		if !stockBoundEntityImageMaterializationEnabled(plan) {
			updated.Cache.InternetImages = "BYPASSED"
			updated.Cache.YouTube = "BYPASSED"
			updated.Cache.Binding = "STOCK_BOUND"
			return updated, nil
		}
		imagePlan := *plan
		imagePlan.MediaPlan = plan.MediaPlan.Clone()
		imagePlan.MediaPlan.ProviderPolicy.YouTube = mediadomain.MediaToggleDisabled
		plan = &imagePlan
		updated.Cache.YouTube = "BYPASSED"
		updated.Cache.Binding = "STOCK_BOUND"
	}
	fanoutPlan := buildVidRushFanoutPlan(plan, updated, f.images, f.youtube)
	// Pinned-media fast path (opt-in, see media_manifest_shim.go): a pinned
	// segment is discovery-complete by construction, so it must return before
	// ANY provider is scheduled (YouTube included), not merely before image
	// search. Pins carry the durable verdicts the scorer would recompute.
	//
	// The pin is a CONTEXTUAL verdict, not an entity one. When a stock-bound
	// scene explicitly requests per-scene entity images, preserve the pinned
	// candidate and still run the identity search; returning here left every
	// PERSON overlay with SEARCH_EMPTY while the manifest pin satisfied the
	// scene image.
	pinned := f.pinnedCandidates(plan, updated)
	if stockBound {
		// Scene-background pins compete with the direct stock binding and must
		// not enter the stock scene's media surface. Only explicitly typed
		// entity-image pins may continue as annotation candidates.
		pinned = f.pinnedEntityOverlayCandidates(plan, updated)
	}
	if len(pinned) > 0 {
		updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, pinned)
		if !stockBound {
			updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, pinned)
		}
		if !stockBoundEntityImageMaterializationEnabled(plan) || !fanoutPlan.imagesEnabled {
			updated.Cache.InternetImages = MediaManifestCacheState
			updated.Cache.InternetImagesProviderSearches = 0
			return updated, nil
		}
	}

	// Provider work is represented by a small outcome value and merged only
	// by the caller, keeping concurrent providers away from shared state.

	profile := updated.CanonicalSemanticProfile()
	imagesEnabled := fanoutPlan.imagesEnabled
	youtubeEnabled := fanoutPlan.youtubeEnabled
	outcomes := make(chan vidRushProviderOutcome, 3)
	var wg sync.WaitGroup

	// Providers read a private snapshot; only the collector mutates updated.
	segmentID := fanoutPlan.segmentID
	youtubeSources := fanoutPlan.youtubeSources
	workerInput := CloneVidRushSegmentResult(updated)

	if youtubeEnabled && len(youtubeSources) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.resolveYouTubeCandidates(ctx, plan, fanoutPlan, workerInput, outcomes)
		}()
	}

	if imagesEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.resolveInternetImageCandidates(ctx, plan, fanoutPlan, workerInput, outcomes)
		}()
	}

	go func() { wg.Wait(); close(outcomes) }()

	for outcome := range outcomes {
		if err := mergeVidRushProviderOutcome(&updated, outcome, plan, profile, segmentID); err != nil {
			return updated, err
		}
	}
	return updated, nil
}

func (f *VidRushProviderFanout) resolveYouTubeCandidates(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, outcomes chan<- vidRushProviderOutcome) {
	profile := workerInput.CanonicalSemanticProfile()
	segmentDurationMs, _ := segmentDurationBudgetMs(workerInput, plan)
	candidates, err := f.youtube.Search(ctx, scriptports.VidRushSearchRequest{
		SegmentID: fanoutPlan.segmentID, Position: workerInput.Position, SceneID: plan.Title, TextHash: fanoutPlan.textHash, Text: workerInput.Text,
		Query: youtubeQuery(workerInput), Limit: 3,
		TargetDurationMs:    segmentDurationMs,
		SceneDurationMs:     segmentSceneDurationMs(workerInput),
		EstimatedDurationMs: estimatedSegmentDurationMs(workerInput, plan),
		SemanticProfile:     &profile,
		Sources:             fanoutPlan.youtubeSources,
	})
	if err != nil {
		outcomes <- vidRushProviderOutcome{provider: scriptpkg.VidRushProviderYouTube, err: err}
		return
	}
	outcomes <- vidRushProviderOutcome{provider: scriptpkg.VidRushProviderYouTube, candidates: candidates}
}

func (f *VidRushProviderFanout) verifySegmentImageCandidates(ctx context.Context, segment scriptpkg.VidRushSegmentResult, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	personCandidates := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	entityCandidates := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	contextCandidates := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		_, personQuery, err := personCatalogIdentityForSegmentQuery(segment, candidate.Entity)
		if err != nil {
			return nil, err
		}
		if personQuery {
			personCandidates = append(personCandidates, candidate)
			continue
		}
		class := searchClassForEntityQuery(segment.Insights.Entities, candidate.Entity)
		if class == "" {
			class = searchClassForEntityQuery(segment.Insights.Entities, candidate.Query)
		}
		if class != "" {
			entityCandidates = append(entityCandidates, candidate)
			continue
		}
		_, personQuery, err = personCatalogIdentityForSegmentQuery(segment, candidate.Query)
		if err != nil {
			return nil, err
		}
		if personQuery {
			personCandidates = append(personCandidates, candidate)
		} else {
			contextCandidates = append(contextCandidates, candidate)
		}
	}
	verifiedPeople, err := f.verifyPersonImageCandidatesForSegment(ctx, segment, personCandidates)
	if err != nil {
		return nil, err
	}
	verifiedEntities, err := f.verifyTypedEntityImageCandidates(ctx, segment, entityCandidates)
	if err != nil {
		return nil, err
	}
	verifiedContext := filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(contextCandidates, "")))
	return AppendProviderCandidatesUnique(append(verifiedPeople, verifiedEntities...), verifiedContext), nil
}

// Non-person entity images need the same pixel-level semantic boundary as
// portraits. Previously only PERSON queries reached the scorer, while entity
// binding required SemanticStatus=accepted for every type; organizations,
// places and products therefore could never bind their discovered images.
func (f *VidRushProviderFanout) verifyTypedEntityImageCandidates(ctx context.Context, segment scriptpkg.VidRushSegmentResult, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if len(candidates) == 0 || f == nil || f.semanticScorer == nil {
		return candidates, nil
	}
	// Keep the source scene separate from the provider query. Retrieval needs
	// short high-recall identity queries; the scene is evidence for the visual
	// verifier and must not become a keyword constraint.
	context := strings.TrimSpace(segment.Text)
	if len([]rune(context)) > 1200 {
		context = string([]rune(context)[:1200])
	}
	byClass := make(map[string][]scriptpkg.SegmentAssetCandidate)
	for _, candidate := range candidates {
		candidate.EntityContext = context
		class := searchClassForEntityQuery(segment.Insights.Entities, candidate.Entity)
		if class == "" {
			class = searchClassForEntityQuery(segment.Insights.Entities, candidate.Query)
		}
		byClass[class] = append(byClass[class], candidate)
	}
	verified := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for class, batch := range byClass {
		scored, err := resolveEntityVisualCandidates(ctx, f.semanticScorer, class, context, batch)
		if err != nil {
			// Scoring failure leaves candidates pending and therefore cannot
			// satisfy the accepted-status binding contract.
			verified = append(verified, batch...)
			continue
		}
		if scored == nil {
			scored = batch
		}
		verified = append(verified, scored...)
	}
	return filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(verified, "entity image"))), nil
}

func (f *VidRushProviderFanout) verifyPersonImageCandidates(ctx context.Context, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	return f.verifyPersonImageCandidatesForSegment(ctx, scriptpkg.VidRushSegmentResult{}, candidates)
}

// verifyPersonImageCandidatesForSegment is the PERSON gate of the fan-out.
//
// Segment-aware is a correctness requirement, not a convenience: the segment's
// PERSON mentions and PersonAliasGraph are the only authority that says a
// candidate's Entity is a person. Without them, a contextual query promoted to
// candidate.Entity ("italian-newspaper-newsroom-journalists-typewriters-...")
// was canonicalized by CanonicalizePersonName and written into the durable
// PERSON catalog as its own entity pool. Candidates that are not PERSON under
// the segment classification are returned untouched for the typed-entity/stock
// paths instead of being scored or persisted as people.
func (f *VidRushProviderFanout) verifyPersonImageCandidatesForSegment(ctx context.Context, segment scriptpkg.VidRushSegmentResult, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if f == nil {
		return filterVerifiedPersonImageCandidates(candidates), nil
	}
	aliasGraph := segmentPersonAliasGraph(segment)
	personCandidates := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	others := make([]scriptpkg.SegmentAssetCandidate, 0)
	identities := make(map[string]entitycatalog.PersonIdentity)
	for _, candidate := range candidates {
		identity, ok, err := personCandidateIdentityForSegment(segment, aliasGraph, candidate)
		if err != nil {
			return nil, err
		}
		if !ok {
			others = append(others, candidate)
			continue
		}
		candidate.Entity = identity.CanonicalName
		candidate.EntityID = identity.CanonicalEntityID
		identities[identity.CanonicalEntityID] = identity
		personCandidates = append(personCandidates, candidate)
	}
	if len(personCandidates) == 0 {
		return others, nil
	}
	// Cached/provider candidates arrive PRE-verification: stamp the durable
	// catalog verdicts first so a warm reuse skips the download + VLM call
	// entirely (the scorer itself honors the same ReusablePersonImageVerdict
	// contract per candidate).
	hydratePersonImageVisualVerdicts(ctx, f.catalog, aliasGraph, personCandidates)
	if f.semanticScorer == nil {
		// No scorer, no verdict. Keep discovery candidates pending so the
		// downstream stage/materializer can decide; the catalog still refuses
		// to reuse them until a real pixel verdict exists.
		recordEntityImageVisualOutcomes(f.metrics, personCandidates, nil)
		return append(personCandidates, others...), nil
	}
	if allCandidatesHaveReusableVerdict(personCandidates) {
		// Every candidate carries a definitive pixel verdict already —
		// re-running the visual scorer is exactly the double download + VLM
		// call this fast path removes.
		recordEntityImageVisualOutcomes(f.metrics, personCandidates, nil)
		return append(filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(personCandidates, "person entity image"))), others...), nil
	}
	scored, err := resolveEntityVisualCandidates(ctx, f.semanticScorer, "person", "", personCandidates)
	if err != nil {
		verified := filterVerifiedPersonImageCandidates(personCandidates)
		recordEntityImageVisualOutcomes(f.metrics, verified, err)
		return append(verified, others...), err
	}
	if scored == nil {
		scored = personCandidates
	}
	// Persist per identity: the catalog write target comes from the segment
	// classification, never from whatever text a provider echoed back.
	for _, identity := range identities {
		group := filterPersonCandidatesByIdentity(scored, identity.CanonicalEntityID)
		if err := persistPersonImageVisualVerdicts(ctx, f.catalog, identity, aliasGraph, group); err != nil {
			recordEntityImageVisualOutcomes(f.metrics, scored, err)
			return scored, err
		}
	}
	recordEntityImageVisualOutcomes(f.metrics, scored, nil)
	return append(filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(scored, "person entity image"))), others...), nil
}

// personCandidateIdentityForSegment resolves a candidate's canonical PERSON
// identity from the segment's own evidence. An empty segment (direct callers
// and tests) falls back to the candidate's stamped identity, but a non-PERSON
// EntityID always wins: the catalog must never promote another entity type.
func personCandidateIdentityForSegment(segment scriptpkg.VidRushSegmentResult, graph *entitycatalog.PersonAliasGraph, candidate scriptpkg.SegmentAssetCandidate) (entitycatalog.PersonIdentity, bool, error) {
	query := strings.TrimSpace(candidate.Entity)
	if query == "" {
		query = strings.TrimSpace(candidate.Query)
	}
	if query != "" {
		identity, ok, err := personCatalogIdentityForSegmentQuery(segment, query)
		if err != nil || ok {
			return identity, ok, err
		}
	}
	// The name-only fallback exists for callers with no segment evidence at all
	// (direct compatibility entry points). A segment that DID carry entities
	// already answered above: "not a person" is final, and canonicalizing the
	// contextual query here is exactly how person:<scene-phrase> rows were
	// minted.
	if len(segment.Insights.Entities) > 0 || len(segment.Insights.ImageEntityCanonicalIDs) > 0 {
		return entitycatalog.PersonIdentity{}, false, nil
	}
	identity, ok := personCatalogCandidateIdentity(candidate, entitycatalog.PersonIdentity{}, graph)
	return identity, ok, nil
}

// filterPersonCandidatesByIdentity keeps only the rows the catalog write is
// allowed to attribute to this identity.
func filterPersonCandidatesByIdentity(candidates []scriptpkg.SegmentAssetCandidate, canonicalEntityID string) []scriptpkg.SegmentAssetCandidate {
	canonicalEntityID = strings.TrimSpace(canonicalEntityID)
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidate.EntityID), canonicalEntityID) {
			out = append(out, candidate)
		}
	}
	return out
}

// The YouTube source-hint helpers live in
// vidrush_registry_searchers_youtube.go: this file crossed the 600-line strict
// cap (godlike/08), and that group is the cohesive unit to move.
