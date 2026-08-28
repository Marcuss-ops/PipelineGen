package adapters

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

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
	updated := cloneVidRushSegmentResult(segment)
	if plan == nil {
		return updated, nil
	}
	// Provider work is represented by a small outcome value and merged only
	// by the caller, keeping concurrent providers away from shared state.

	profile := profileFromVidRushSegment(updated)
	fanoutPlan := buildVidRushFanoutPlan(plan, updated, f.artlist, f.images, f.youtube)
	artlistEnabled := fanoutPlan.artlistEnabled
	imagesEnabled := fanoutPlan.imagesEnabled
	youtubeEnabled := fanoutPlan.youtubeEnabled
	outcomes := make(chan vidRushProviderOutcome, 3)
	var wg sync.WaitGroup

	// Snapshot the read-only segment inputs each provider goroutine needs so
	// no goroutine reads the shared `updated` result while the collector below
	// mutates its Assets fields. This makes the fanout merge race-free and
	// deterministic regardless of provider completion order.
	perQueryLimit := fanoutPlan.perQueryLimit
	segmentID := fanoutPlan.segmentID
	textHash := fanoutPlan.textHash
	artlistQueries := fanoutPlan.artlistQueries
	imageQueries := fanoutPlan.imageQueries
	youtubeSources := fanoutPlan.youtubeSources
	firstEntity := fanoutPlan.firstEntity
	artlistIdentity := scriptpkg.VidRushSegmentResult{SegmentID: segmentID}

	if youtubeEnabled && len(youtubeSources) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidates, err := f.youtube.Search(ctx, scriptports.VidRushSearchRequest{
				SegmentID: segmentID, SceneID: plan.Title, TextHash: textHash, Text: updated.Text,
				Query: youtubeQuery(updated), Limit: 3, Sources: youtubeSources,
			})
			if err != nil {
				outcomes <- vidRushProviderOutcome{provider: scriptpkg.VidRushProviderYouTube, err: err}
				return
			}
			outcomes <- vidRushProviderOutcome{provider: scriptpkg.VidRushProviderYouTube, candidates: candidates}
		}()
	}
	if artlistEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cacheKey := vidRushFanoutArtlistCacheKey(&fanoutPlan, plan)
			/*
				cacheKey := artlistSegmentCacheKey(
					segmentID,
					textHash,
					artlistIntentHash,
					plan.Language,
					plan.Model,
					plan.PromptVersion,
				)
			*/
			if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				if cached, ok := cacheLoad(&vidrushArtlistCache, cacheKey); ok {
					if payload, ok := cached.(artlistSegmentCachePayload); ok {
						candidates := append([]scriptpkg.SegmentAssetCandidate(nil), payload.Candidates...)
						var primary *scriptpkg.SegmentAssetCandidate
						if len(candidates) > 0 && readyVidRushCandidate(candidates[0]) {
							selected := candidates[0]
							primary = &selected
						}
						if f.metrics != nil {
							f.metrics.IncAssetCache("artlist", true)
						}
						outcomes <- vidRushProviderOutcome{provider: "artlist", candidates: candidates, primary: primary, allCacheHits: true}
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
						cacheStore(&vidrushArtlistCache, cacheKey, persisted)
					}
					var primary *scriptpkg.SegmentAssetCandidate
					if len(persisted.Candidates) > 0 && readyVidRushCandidate(persisted.Candidates[0]) {
						selected := persisted.Candidates[0]
						primary = &selected
					}
					if f.metrics != nil {
						f.metrics.IncAssetCache("artlist", true)
					}
					outcomes <- vidRushProviderOutcome{provider: "artlist", candidates: persisted.Candidates, primary: primary, allCacheHits: true}
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
			var primary *scriptpkg.SegmentAssetCandidate
			if len(candidates) > 0 && readyVidRushCandidate(candidates[0]) {
				selected := candidates[0]
				primary = &selected
			}
			payload := artlistSegmentCachePayload{
				Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...),
				Matches:    cloneArtlistMatches(dedupeArtlistMatches(matches)),
			}
			cacheStore(&vidrushArtlistCache, cacheKey, payload)
			if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "artlist", cacheKey, payload); cacheErr != nil {
				outcomes <- vidRushProviderOutcome{provider: "artlist", err: cacheErr}
				return
			}
			outcomes <- vidRushProviderOutcome{provider: "artlist", candidates: candidates, primary: primary}
		}()
	}
	if imagesEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Segment-level cache is keyed on segment identity (SegmentID +
			// TextHash + prompt/model/limit), mirroring InternetImagesProcessor.
			// On the text path the TextHash is deterministic across a warm
			// replay (memory gate), so this cache produces HIT_EXACT without
			// re-calling the provider.
			segmentCacheKeyStr := vidRushFanoutImageCacheKey(&fanoutPlan, plan)
			/*
				segmentCacheKeyStr := segmentCacheKey(
					"internet-images-assets-v3",
					segmentID,
					textHash,
					plan.Language,
					plan.Model,
					plan.PromptVersion,
					fmt.Sprintf("%d", perQueryLimit),
				)
			*/
			if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				if cached, ok := cacheLoad(&vidrushImageCache, segmentCacheKeyStr); ok {
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
						cacheStore(&vidrushImageCache, segmentCacheKeyStr, persisted)
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
				entityCacheKey := segmentCacheKey("entity-image-v1", strings.ToLower(strings.TrimSpace(plan.Topic)), strings.ToLower(strings.TrimSpace(query)), plan.Language)
				catalogIdentity, catalogEligible, catalogErr := personCatalogIdentityForSegmentQuery(updated, query)
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
					actual, _ := entityImageLocks.LoadOrStore("entity-catalog:"+catalogIdentity.CanonicalEntityID, &sync.Mutex{})
					catalogLock := actual.(*sync.Mutex)
					catalogLock.Lock()
					releaseCatalog = catalogLock.Unlock
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
					if cached, ok := cacheLoad(&entityImageCache, entityCacheKey); ok {
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
								cacheStore(&entityImageCache, entityCacheKey, persisted)
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
						SegmentID: segmentID, Query: query, Entity: firstEntity,
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
					results = appendProviderCandidatesUnique(catalogFallback, providerResults)
					if len(results) > 0 {
						cacheStore(&entityImageCache, entityCacheKey, append([]scriptpkg.SegmentAssetCandidate(nil), results...))
					}
					if results == nil {
						results = []scriptpkg.SegmentAssetCandidate{}
					}
					if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "entity_images", entityCacheKey, results); cacheErr != nil {
						if releaseCatalog != nil {
							releaseCatalog()
						}
						outcomes <- vidRushProviderOutcome{provider: "internet_images", err: cacheErr}
						return
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
				cacheStore(&vidrushImageCache, segmentCacheKeyStr, payload)
			}
			if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, payload); cacheErr != nil {
				outcomes <- vidRushProviderOutcome{provider: "internet_images", err: cacheErr}
				return
			}
			outcomes <- vidRushProviderOutcome{provider: "internet_images", candidates: candidates, allCacheHits: allCacheHits}
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

func youtubeSourcesForSegment(plan *scriptpkg.ResolvedGenerationPlan, segmentID string) []scriptports.VidRushSourceHint {
	if plan == nil {
		return nil
	}
	out := make([]scriptports.VidRushSourceHint, 0)
	for _, source := range plan.MediaPlan.Sources {
		if source.SegmentID != segmentID || !strings.EqualFold(source.Provider, scriptpkg.VidRushProviderYouTube) {
			continue
		}
		out = append(out, scriptports.VidRushSourceHint{URL: source.SourceURL, Priority: source.Priority, Required: string(source.Mode) == "required"})
	}
	return out
}

func youtubeSourceRequired(plan *scriptpkg.ResolvedGenerationPlan, segmentID string) bool {
	for _, source := range plan.MediaPlan.Sources {
		if source.SegmentID == segmentID && strings.EqualFold(source.Provider, scriptpkg.VidRushProviderYouTube) && source.Mode == "required" {
			return true
		}
	}
	return false
}

func youtubeQuery(segment scriptpkg.VidRushSegmentResult) string {
	if len(segment.Insights.YouTubeQueries) > 0 {
		return strings.Join(segment.Insights.YouTubeQueries, " ")
	}
	return segment.Text
}
