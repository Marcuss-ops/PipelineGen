package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
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

// MediaResolver is the unified provider discovery boundary used by the
// production fan-out. A resolver may expose several provider queries, but it
// never owns acquisition or winner selection.
type MediaResolver interface {
	ArtlistClipSearcher
	InternetImageSearcher
}

func NewVidRushProviderFanoutWithResolver(resolver MediaResolver, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushMetrics) *VidRushProviderFanout {
	return NewVidRushProviderFanoutWithCatalog(resolver, resolver, cache, catalog, metrics...)
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
	return s.Registry.Search(ctx, scriptpkg.VidRushProviderInternetImages, scriptports.VidRushSearchRequest{
		SegmentID: req.SegmentID, Position: req.Position, TextHash: req.TextHash, Text: req.Query, Query: req.Query, Limit: req.Limit,
	})
}

// VidRushProviderFanout resolves a single enriched segment's visual providers
// in parallel through the shared searcher ports (which dispatch the canonical
// provider registry). It owns only the concurrency and the candidate merge; the
// provider-specific search, rate limiting and retry stay in the searchers, so
// no provider orchestration is duplicated for the incremental path.
type VidRushProviderFanout struct {
	artlist ArtlistClipSearcher
	images  InternetImageSearcher
	youtube scriptports.VidRushAssetProvider
	cache   scriptports.VidRushCachePort
	catalog entitycatalog.Repository
	metrics VidRushMetrics
	log     zap.Logger
}

func (f *VidRushProviderFanout) WithLogger(log *zap.Logger) *VidRushProviderFanout {
	if f != nil && log != nil {
		f.log = *log
	}
	return f
}

func NewVidRushProviderFanout(artlist ArtlistClipSearcher, images InternetImageSearcher, metrics ...VidRushMetrics) *VidRushProviderFanout {
	return NewVidRushProviderFanoutWithCatalog(artlist, images, nil, nil, metrics...)
}

// NewVidRushProviderFanoutWithYouTube adds the canonical YouTube provider to
// the existing fan-out while preserving the legacy constructor.
func NewVidRushProviderFanoutWithYouTube(artlist ArtlistClipSearcher, images InternetImageSearcher, youtube scriptports.VidRushAssetProvider, metrics ...VidRushMetrics) *VidRushProviderFanout {
	fanout := NewVidRushProviderFanout(artlist, images, metrics...)
	fanout.youtube = youtube
	return fanout
}

func NewVidRushProviderFanoutWithCache(artlist ArtlistClipSearcher, images InternetImageSearcher, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *VidRushProviderFanout {
	return NewVidRushProviderFanoutWithCatalog(artlist, images, cache, nil, metrics...)
}

func NewVidRushProviderFanoutWithCatalog(artlist ArtlistClipSearcher, images InternetImageSearcher, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushMetrics) *VidRushProviderFanout {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushProviderFanout{artlist: artlist, images: images, cache: cache, catalog: catalog, metrics: m}
}

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
		// The incremental fanout resolves providers per segment with the plan
		// as the only binding surface. A direct stock binding is the scene's
		// authoritative visual source: no provider search may run for it.
		updated.Cache.Artlist = "BYPASSED"
		updated.Cache.InternetImages = "BYPASSED"
		updated.Cache.YouTube = "BYPASSED"
		updated.Cache.Binding = "STOCK_BOUND"
		return updated, nil
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
	if f.metrics != nil {
		f.metrics.IncProviderRequest("artlist")
	}
	matches, err := f.artlist.SearchClips(ctx, plan.Title, artlistQueries)
	if err != nil {
		if f.metrics != nil {
			f.metrics.IncProviderFailure("artlist")
		}
		outcomes <- vidRushProviderOutcome{provider: "artlist", err: err}
		return
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

func (f *VidRushProviderFanout) resolveInternetImageCandidates(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, fanoutPlan vidRushFanoutPlan, workerInput scriptpkg.VidRushSegmentResult, outcomes chan<- vidRushProviderOutcome) {
	perQueryLimit := fanoutPlan.perQueryLimit
	segmentID := fanoutPlan.segmentID
	textHash := fanoutPlan.textHash
	imageQueries := fanoutPlan.imageQueries
	// Segment-level cache is keyed on segment identity (SegmentID +
	// TextHash + prompt/model/limit), matching the canonical resolver cache.
	// On the text path the TextHash is deterministic across a warm
	// replay (memory gate), so this cache produces HIT_EXACT without
	// re-calling the provider.
	segmentCacheKeyStr := vidRushFanoutImageCacheKey(&fanoutPlan, plan)
	if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
		if cached, ok := cacheLoad(vidrushImageCache, segmentCacheKeyStr); ok {
			if payload, ok := cached.(internetImageCachePayload); ok {
				outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: append([]scriptpkg.SegmentAssetCandidate(nil), payload.Candidates...), allCacheHits: true}
				return
			}
		}
		var persisted internetImageCachePayload
		if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, &persisted); err != nil {
			outcomes <- vidRushProviderOutcome{provider: "internet_images", err: err}
			return
		} else if hit {
			if len(persisted.Candidates) > 0 {
				cacheStore(vidrushImageCache, segmentCacheKeyStr, persisted)
			}
			outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: append([]scriptpkg.SegmentAssetCandidate(nil), persisted.Candidates...), allCacheHits: true}
			return
		}
	}
	candidates := make([]scriptpkg.SegmentAssetCandidate, 0)
	seen := make(map[string]struct{})
	allCacheHits := len(imageQueries) > 0
	for _, query := range imageQueries {
		// Per-query cache keyed on (topic, query, language), stable on
		// the research path where the generated scene text (and thus
		// TextHash) is non-deterministic across runs.
		entityCacheKey := versionedSegmentCacheKey("discovery", scriptports.DiscoveryCacheVersion, strings.ToLower(strings.TrimSpace(plan.Topic)), strings.ToLower(strings.TrimSpace(query)), plan.Language)
		catalogIdentity, catalogEligible, catalogErr := personCatalogIdentityForSegmentQuery(workerInput, query)
		if catalogErr != nil {
			outcomes <- vidRushProviderOutcome{provider: "internet_images", err: catalogErr}
			return
		}
		var results []scriptpkg.SegmentAssetCandidate
		catalogFallback := []scriptpkg.SegmentAssetCandidate(nil)
		catalogRefreshRequired := false
		fromCache := false
		var releaseCatalog func()
		catalogMetrics := entityImageCatalogMetricsFor(f.metrics)
		if catalogEligible && f.catalog != nil {
			// Canonical reference-counted per-key locker: the entry is
			// removed when the last holder releases, so the registry
			// never grows with the set of entity IDs seen over the
			// process lifetime (retired sync.Map-of-mutexes pattern).
			releaseCatalog = entityImageLocks.Lock("entity-catalog:" + catalogIdentity.CanonicalEntityID)
			if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				lookupStarted := time.Now()
				pool, err := entityImageCatalogCandidates(ctx, f.catalog, catalogIdentity, perQueryLimit)
				observeEntityImageCatalogLookup(f.metrics, lookupStarted)
				if err != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: err}
					return
				}
				if pool.Sufficient {
					if catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogLookup(true)
					}
					results = pool.Candidates
					fromCache = true
				} else {
					if catalogMetrics != nil {
						catalogMetrics.IncEntityImageCatalogLookup(false)
					}
					catalogFallback = pool.Candidates
					catalogRefreshRequired = true
				}
			}
		}
		if !catalogRefreshRequired && !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh && !fromCache {
			if cached, ok := cacheLoad(entityImageCache, entityCacheKey); ok {
				if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
					results = append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...)
					fromCache = true
				}
			}
			if !fromCache {
				var persisted []scriptpkg.SegmentAssetCandidate
				if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "entity_images", entityCacheKey, &persisted); err != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: err}
					return
				} else if hit {
					if len(persisted) > 0 {
						cacheStore(entityImageCache, entityCacheKey, persisted)
					}
					results = persisted
					fromCache = true
				}
			}
		}
		if fromCache {
			results = normalizeInternetImageCatalogResults(results, query)
			if catalogEligible && f.catalog != nil {
				results = filterPersonEntityImageCandidates(catalogIdentity, results)
				if catalogErr := persistEntityImageCatalogCandidates(ctx, f.catalog, catalogIdentity, results); catalogErr != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: catalogErr}
					return
				}
			}
		}
		if !fromCache {
			allCacheHits = false
			if catalogMetrics != nil {
				if catalogEligible && (catalogRefreshRequired || plan.MediaPlan.ForceRefreshAssets || plan.ForceRefresh) {
					catalogMetrics.IncEntityImageCatalogRefresh()
				}
				if catalogEligible {
					catalogMetrics.IncEntityImageCatalogProviderCall()
				}
			}
			if f.metrics != nil {
				f.metrics.IncProviderRequest("internet_images")
			}
			searched, err := f.images.SearchImages(ctx, InternetImageSearchRequest{
				// The query is the identity surface for entity-image
				// searches; preserve it through provider normalization.
				SegmentID: segmentID, Position: workerInput.Position, Query: query, Entity: query,
				TextHash: textHash, Language: plan.Language, Limit: perQueryLimit,
				Provider: "internet_images",
			})
			if err != nil {
				if f.metrics != nil {
					f.metrics.IncProviderFailure("internet_images")
				}
				if releaseCatalog != nil {
					releaseCatalog()
					releaseCatalog = nil
				}
				outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: catalogFallback, err: err}
				return
			}
			if f.log.Core() != nil {
				f.log.Info("VidRush image candidates received", zap.String("segment_id", segmentID), zap.String("query", query), zap.Int("count", len(searched)), zap.String("first_asset_id", func() string {
					if len(searched) > 0 {
						return searched[0].AssetID
					}
					return ""
				}()), zap.String("first_source_url", func() string {
					if len(searched) > 0 {
						return searched[0].SourceURL
					}
					return ""
				}()))
			}
			providerResults := normalizeInternetImageCatalogResults(searched, query)
			if catalogEligible && f.catalog != nil {
				providerResults = filterPersonEntityImageCandidates(catalogIdentity, providerResults)
				if catalogErr := persistEntityImageCatalogCandidates(ctx, f.catalog, catalogIdentity, providerResults); catalogErr != nil {
					if releaseCatalog != nil {
						releaseCatalog()
					}
					outcomes <- vidRushProviderOutcome{provider: "internet_images", err: catalogErr}
					return
				}
			}
			results = AppendProviderCandidatesUnique(catalogFallback, providerResults)
			if len(results) > 0 {
				cacheStore(entityImageCache, entityCacheKey, append([]scriptpkg.SegmentAssetCandidate(nil), results...))
			}
			if results == nil {
				results = []scriptpkg.SegmentAssetCandidate{}
			}
			if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "entity_images", entityCacheKey, results); cacheErr != nil {
				// The per-query cache is only a replay projection. Keep the
				// live provider results and continue fan-out when that
				// projection is unavailable.
				if f.log.Core() != nil {
					f.log.Warn("VidRush image query cache write failed; retaining live candidates", zap.String("query", query), zap.Error(cacheErr))
				}
			}
		}
		if releaseCatalog != nil {
			releaseCatalog()
		}
		for _, cand := range results {
			if strings.TrimSpace(cand.Provider) == "" {
				cand.Provider = "internet_images"
			}
			if strings.TrimSpace(cand.Query) == "" {
				cand.Query = query
			}
			if strings.ToLower(strings.TrimSpace(cand.Provider)) != "internet_images" {
				continue
			}
			key := vidRushCandidateIdentity(cand)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, cand)
		}
	}
	// Durable-cache the segment result (including an empty result set)
	// so a warm replay of this exact segment is deterministic.
	payload := internetImageCachePayload{Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)}
	if len(payload.Candidates) > 0 {
		cacheStore(vidrushImageCache, segmentCacheKeyStr, payload)
	}
	if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, payload); cacheErr != nil {
		// Cache persistence is a projection, not the source of truth for
		// provider discovery. Preserve the live candidates so a cache
		// outage cannot erase usable media before materialization.
		outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, err: cacheErr}
		return
	}
	if f.log.Core() != nil {
		f.log.Info("VidRush image fanout merged candidates", zap.String("segment_id", segmentID), zap.Int("count", len(candidates)))
	}
	outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, allCacheHits: allCacheHits}
}

// The YouTube source-hint helpers live in
// vidrush_registry_searchers_youtube.go: this file crossed the 600-line strict
// cap (godlike/08), and that group is the cohesive unit to move.
