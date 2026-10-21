package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// The Artlist search gate (env override, process-wide gate channel, retry
// helpers) lives in vidrush_artlist_isolation.go — same provider family, and
// this file sat past the godlike/08 strict line cap with the gate inline.

// VidRushRegistryMediaResolver is the single provider-registry discovery
// adapter. It implements both discovery ports consumed by the fan-out; it
// never acquires, scores, ranks or selects a winner.
type VidRushRegistryMediaResolver struct {
	Registry *VidRushAssetProviderRegistry
}

func (s *VidRushRegistryMediaResolver) SearchClips(ctx context.Context, title string, phrases []string) ([]ArtlistClipMatch, error) {
	if s == nil || s.Registry == nil {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	type queryResult struct {
		match ArtlistClipMatch
		err   error
	}
	results, mapErr := concurrent.Map(ctx, phrases, 3, func(ctx context.Context, _ int, rawPhrase string) (queryResult, error) {
		phrase := strings.TrimSpace(rawPhrase)
		if phrase == "" {
			return queryResult{}, nil
		}
		if err := acquireVidRushArtlistSearch(ctx); err != nil {
			return queryResult{err: fmt.Errorf("artlist query %q: acquire search slot: %w", phrase, err)}, nil
		}
		defer releaseVidRushArtlistSearch()
		var candidates []scriptpkg.SegmentAssetCandidate
		// Shared retry engine (2026-09-12 audit F6c; replaces the
		// hand-rolled `attempt < 3` loop): 3 attempts, 1s→2s backoff,
		// retrying ONLY on Artlist rate-limit errors.
		candidates, searchErr := retry.DoWithValue(ctx, func() ([]scriptpkg.SegmentAssetCandidate, error) {
			queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			return s.Registry.Search(queryCtx, scriptpkg.VidRushProviderArtlist, scriptports.VidRushSearchRequest{SceneID: title, Text: title, Query: phrase, Limit: 10})
		}, retry.Options{
			MaxAttempts:    3,
			InitialBackoff: 1 * time.Second,
			MaxBackoff:     2 * time.Second,
			BackoffFactor:  2.0,
			DisableJitter:  true,
			IsRetryable:    isArtlistRateLimited,
		})
		err := searchErr
		if err != nil {
			return queryResult{err: fmt.Errorf("artlist query %q: %w", phrase, err)}, nil
		}
		match := ArtlistClipMatch{Phrase: phrase, Remote: true}
		for _, candidate := range candidates {
			link := strings.TrimSpace(candidate.SourceURL)
			if !isM3U8URL(link) {
				link = strings.TrimSpace(candidate.PreviewURL)
			}
			if !isM3U8URL(link) {
				continue
			}
			match.ClipNames = append(match.ClipNames, candidate.AssetID)
			match.ClipDriveLinks = append(match.ClipDriveLinks, link)
			if match.FolderLink == "" {
				match.FolderLink = candidate.SourcePageURL
			}
		}
		return queryResult{match: match}, nil
	})
	if mapErr != nil {
		return nil, mapErr
	}
	out := make([]ArtlistClipMatch, 0, len(results))
	var firstErr error
	for _, result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		if len(result.match.ClipDriveLinks) > 0 {
			out = append(out, result.match)
		}
	}
	return out, firstErr
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
// ResolveProviders runs Artlist and internet-image discovery concurrently for
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
		updated.Cache.Artlist = "BYPASSED"
		updated.Cache.InternetImages = "BYPASSED"
		updated.Cache.YouTube = "BYPASSED"
		return updated, nil
	}
	if _, stockBound := stockBindingForSegment(plan, nil, segment); stockBound {
		// A stock binding owns the scene's video. The explicit entity-image
		// policy may still fetch verified portrait overlays; keep every video
		// provider disabled on this sidecar path.
		if !stockBoundEntityImageMaterializationEnabled(plan) {
			updated.Cache.Artlist = "BYPASSED"
			updated.Cache.InternetImages = "BYPASSED"
			updated.Cache.YouTube = "BYPASSED"
			updated.Cache.Binding = "STOCK_BOUND"
			return updated, nil
		}
		imagePlan := *plan
		imagePlan.MediaPlan = plan.MediaPlan.Clone()
		imagePlan.MediaPlan.ProviderPolicy.Artlist = mediadomain.MediaToggleDisabled
		imagePlan.MediaPlan.ProviderPolicy.YouTube = mediadomain.MediaToggleDisabled
		plan = &imagePlan
		updated.Cache.Artlist = "BYPASSED"
		updated.Cache.YouTube = "BYPASSED"
		updated.Cache.Binding = "STOCK_BOUND"
	}
	// Provider work is represented by a small outcome value and merged only
	// by the caller, keeping concurrent providers away from shared state.

	profile := updated.CanonicalSemanticProfile()
	fanoutPlan := buildVidRushFanoutPlan(plan, updated, f.artlist, f.images, f.youtube)
	artlistEnabled := fanoutPlan.artlistEnabled
	imagesEnabled := fanoutPlan.imagesEnabled
	youtubeEnabled := fanoutPlan.youtubeEnabled
	outcomes := make(chan vidRushProviderOutcome, 3)
	var wg sync.WaitGroup

	// Providers read a private snapshot; only the collector mutates updated.
	segmentID := fanoutPlan.segmentID
	youtubeSources := fanoutPlan.youtubeSources
	workerInput := CloneVidRushSegmentResult(updated)
	// Keep the complete canonical identity when converting Artlist matches.
	// Using only SegmentID would silently reset Position/TextHash and make a
	// valid candidate indistinguishable from a foreign-segment binding.
	artlistIdentity := workerInput

	if youtubeEnabled && len(youtubeSources) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.resolveYouTubeCandidates(ctx, plan, fanoutPlan, workerInput, outcomes)
		}()
	}

	if artlistEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.resolveArtlistCandidates(ctx, plan, fanoutPlan, artlistIdentity, outcomes)
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

func (f *VidRushProviderFanout) resolveArtlistCandidates(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, artlistIdentity scriptpkg.VidRushSegmentResult, outcomes chan<- vidRushProviderOutcome) {
	cacheKey := vidRushFanoutArtlistCacheKey(&fanoutPlan, plan)
	artlistQueries := fanoutPlan.artlistQueries
	if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
		if cached, ok := cacheLoad(vidrushArtlistCache, cacheKey); ok {
			if payload, ok := cached.(artlistSegmentCachePayload); ok {
				candidates := append([]scriptpkg.SegmentAssetCandidate(nil), payload.Candidates...)
				if f.metrics != nil {
					f.metrics.IncAssetCache("artlist", true)
				}
				outcomes <- vidRushProviderOutcome{provider: "artlist", candidates: candidates, allCacheHits: true}
				return
			}
		}
		var persisted artlistSegmentCachePayload
		if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "artlist", cacheKey, &persisted); err != nil {
			outcomes <- vidRushProviderOutcome{provider: "artlist", err: err}
			return
		} else if hit {
			persisted = cloneArtlistSegmentCachePayload(persisted)
			if len(persisted.Candidates) > 0 {
				cacheStore(vidrushArtlistCache, cacheKey, persisted)
			}
			if f.metrics != nil {
				f.metrics.IncAssetCache("artlist", true)
			}
			outcomes <- vidRushProviderOutcome{provider: "artlist", candidates: persisted.Candidates, allCacheHits: true}
			return
		}
	}
	if f.metrics != nil {
		f.metrics.IncAssetCache("artlist", false)
	}
	// P6a (anti-muda): query-set dedup across segments. The segment cache
	// above keys on segment identity, so N segments about the same entities
	// pay N identical provider searches. Raw matches carry no segment
	// identity — share them, then convert per owning segment below.
	querysetKey := vidRushArtlistQuerySetKey(plan.Title, plan.Language, artlistQueries)
	matches, querysetHit := artlistQuerySetLoad(querysetKey)
	if !querysetHit && !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
		var persisted artlistQuerySetPayload
		if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "artlist-queryset", querysetKey, &persisted); err != nil {
			outcomes <- vidRushProviderOutcome{provider: "artlist", err: err}
			return
		} else if hit {
			matches, querysetHit = cloneArtlistMatches(persisted.Matches), true
			artlistQuerySetStore(querysetKey, matches)
		}
	}
	if !querysetHit {
		if f.metrics != nil {
			f.metrics.IncProviderRequest("artlist")
		}
		var err error
		matches, err = f.artlist.SearchClips(ctx, plan.Title, artlistQueries)
		if err != nil {
			if f.metrics != nil {
				f.metrics.IncProviderFailure("artlist")
			}
			outcomes <- vidRushProviderOutcome{provider: "artlist", err: err}
			return
		}
		matches = dedupeArtlistMatches(matches)
		artlistQuerySetStore(querysetKey, matches)
		// Best-effort L2: a store failure must not turn a successful
		// search into an error outcome (the segment store below owns
		// the fail-closed path).
		if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
			_ = storeVidRushPersistentJSON(ctx, f.cache, "artlist-queryset", querysetKey, artlistQuerySetPayload{Matches: matches})
		}
	} else if f.metrics != nil {
		f.metrics.IncAssetCache("artlist", true)
	}
	candidates := artlistMatchesToCandidates(artlistIdentity, dedupeArtlistMatches(matches))
	payload := artlistSegmentCachePayload{
		Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...),
		Matches:    cloneArtlistMatches(dedupeArtlistMatches(matches)),
	}
	cacheStore(vidrushArtlistCache, cacheKey, payload)
	if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "artlist", cacheKey, payload); cacheErr != nil {
		outcomes <- vidRushProviderOutcome{provider: "artlist", err: cacheErr}
		return
	}
	outcomes <- vidRushProviderOutcome{provider: "artlist", candidates: candidates}
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
