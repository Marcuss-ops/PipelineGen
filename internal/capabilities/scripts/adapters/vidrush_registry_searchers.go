package adapters

import (
	"context"
	"strings"
	"sync"

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
				Query: query, Entity: req.Entity, QueryClass: req.QueryClass, Limit: req.Limit,
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
	var candidates []scriptpkg.SegmentAssetCandidate
	for depth := 0; ; depth++ {
		addedAtDepth := false
		for _, results := range resultSets {
			if depth >= len(results) {
				continue
			}
			addedAtDepth = true
			candidates = AppendProviderCandidatesUnique(candidates, results[depth:depth+1])
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
	if _, stockBound := stockBindingForSegment(plan, nil, segment); stockBound {
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
	if pinned := f.pinnedCandidates(plan, updated); len(pinned) > 0 {
		updated.Assets.Candidates = AppendProviderCandidatesUnique(updated.Assets.Candidates, pinned)
		updated.Assets.SecondaryImages = AppendProviderCandidatesUnique(updated.Assets.SecondaryImages, pinned)
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
	contextCandidates := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		_, personQuery, err := personCatalogIdentityForSegmentQuery(segment, candidate.Query)
		if err != nil {
			return nil, err
		}
		if personQuery {
			personCandidates = append(personCandidates, candidate)
		} else {
			contextCandidates = append(contextCandidates, candidate)
		}
	}
	verifiedPeople, err := f.verifyPersonImageCandidates(ctx, personCandidates)
	if err != nil {
		return nil, err
	}
	verifiedContext := filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(contextCandidates, "")))
	return AppendProviderCandidatesUnique(verifiedPeople, verifiedContext), nil
}

func (f *VidRushProviderFanout) verifyPersonImageCandidates(ctx context.Context, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if f == nil {
		return filterVerifiedPersonImageCandidates(candidates), nil
	}
	// Cached/provider candidates arrive PRE-verification: stamp the durable
	// catalog verdicts first so a warm reuse skips the download + VLM call
	// entirely (the scorer itself honors the same ReusablePersonImageVerdict
	// contract per candidate).
	hydratePersonImageVisualVerdicts(ctx, f.catalog, candidates)
	if f.semanticScorer == nil {
		// No scorer, no verdict. Keep discovery candidates pending so the
		// downstream stage/materializer can decide; the catalog still refuses
		// to reuse them until a real pixel verdict exists.
		out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
		recordEntityImageVisualOutcomes(f.metrics, out, nil)
		return out, nil
	}
	if allCandidatesHaveReusableVerdict(candidates) {
		// Every candidate carries a definitive pixel verdict already —
		// re-running the visual scorer is exactly the double download + VLM
		// call this fast path removes.
		recordEntityImageVisualOutcomes(f.metrics, candidates, nil)
		return filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(append([]scriptpkg.SegmentAssetCandidate(nil), candidates...), "person entity image"))), nil
	}
	scored, err := f.semanticScorer.ScoreImageCandidates(ctx, "person entity image", candidates)
	if err != nil {
		verified := filterVerifiedPersonImageCandidates(candidates)
		recordEntityImageVisualOutcomes(f.metrics, verified, err)
		return verified, err
	}
	if scored == nil {
		scored = candidates
	}
	if err := persistPersonImageVisualVerdicts(ctx, f.catalog, scored); err != nil {
		recordEntityImageVisualOutcomes(f.metrics, scored, err)
		return scored, err
	}
	recordEntityImageVisualOutcomes(f.metrics, scored, nil)
	return filterInternetImageCandidates(deduplicateInternetImageCandidates(normalizeInternetImageCandidates(scored, "person entity image"))), nil
}

func personImageSearchQuery(canonicalName string, isPerson bool) string {
	canonicalName = strings.TrimSpace(canonicalName)
	if !isPerson || canonicalName == "" {
		return canonicalName
	}
	// Keep the retrieval intent ("photo") without forcing a tight headshot.
	// The old "single-person portrait headshot photo" suffix excluded editorial
	// and environmental photographs before the identity/quality gate could see
	// them. Canonical entity identity remains a separate provider request field.
	return canonicalName + " photo"
}

// The YouTube source-hint helpers live in
// vidrush_registry_searchers_youtube.go: this file crossed the 600-line strict
// cap (godlike/08), and that group is the cohesive unit to move.
