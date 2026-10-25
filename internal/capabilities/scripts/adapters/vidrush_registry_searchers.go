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

func (s *VidRushRegistryMediaResolver) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if s == nil || s.Registry == nil {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	queries := []string{req.Query}
	if strings.Contains(strings.ToLower(req.Query), "single-person portrait headshot photo") {
		// One broad portrait query repeatedly returned the same top photograph
		// for every scene. Search a small set of identity-safe contexts through
		// the same provider and merge them; downstream person verification and
		// perceptual deduplication remain authoritative.
		name := strings.TrimSpace(req.Entity)
		queries = []string{
			req.Query,
			name + " foto intervista",
			name + " foto udienza tribunale Garlasco",
			name + " dichiarazioni foto avvocato Garlasco",
			name + " servizio televisivo ritratto",
		}
	}
	resultSets := make([][]scriptpkg.SegmentAssetCandidate, 0, len(queries))
	var lastErr error
	for _, query := range queries {
		found, err := s.Registry.Search(ctx, scriptpkg.VidRushProviderInternetImages, scriptports.VidRushSearchRequest{
			SegmentID: req.SegmentID, Position: req.Position, TextHash: req.TextHash, Text: query,
			Query: query, Entity: req.Entity, Limit: req.Limit,
		})
		if err != nil {
			lastErr = err
			continue
		}
		resultSets = append(resultSets, found)
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
	// Provider work is represented by a small outcome value and merged only
	// by the caller, keeping concurrent providers away from shared state.

	profile := updated.CanonicalSemanticProfile()
	fanoutPlan := buildVidRushFanoutPlan(plan, updated, f.images, f.youtube)
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
	if f == nil || len(candidates) == 0 {
		return append([]scriptpkg.SegmentAssetCandidate(nil), candidates...), nil
	}
	if f.semanticScorer == nil {
		recordEntityImageVisualOutcomes(f.metrics, candidates, nil)
		return append([]scriptpkg.SegmentAssetCandidate(nil), candidates...), nil
	}
	scored, err := f.semanticScorer.ScoreImageCandidates(ctx, "person entity image", candidates)
	if err != nil {
		recordEntityImageVisualOutcomes(f.metrics, candidates, err)
		return candidates, err
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
	// Search uses a narrow subject description to avoid article hero collages;
	// canonical entity identity remains a separate provider request field.
	return canonicalName + " single-person portrait headshot photo"
}

// The YouTube source-hint helpers live in
// vidrush_registry_searchers_youtube.go: this file crossed the 600-line strict
// cap (godlike/08), and that group is the cohesive unit to move.
