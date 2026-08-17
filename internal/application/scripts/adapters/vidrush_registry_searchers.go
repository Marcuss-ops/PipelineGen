package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Artlist applies a provider-side rate limit independently of the local
// worker budget. Keep one live browser search in flight across all VidRush
// jobs and use bounded retries for 429 responses; otherwise waves of jobs can
// turn the configured worker count into a provider outage.
var vidRushArtlistSearchGate = make(chan struct{}, 1)

func acquireVidRushArtlistSearch(ctx context.Context) error {
	select {
	case vidRushArtlistSearchGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseVidRushArtlistSearch() { <-vidRushArtlistSearchGate }

func isArtlistRateLimited(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "429")
}

// VidRushRegistryClipSearcher adapts the shared VidRush provider registry to
// the legacy clip-search result shape. It keeps discovery on the same
// provider path as acquisition; the materializer still owns persistence.
type VidRushRegistryClipSearcher struct {
	Registry *VidRushAssetProviderRegistry
}

func (s *VidRushRegistryClipSearcher) SearchClips(ctx context.Context, title string, phrases []string) ([]ArtlistClipMatch, error) {
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
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			candidates, err = s.Registry.Search(queryCtx, scriptpkg.VidRushProviderArtlist, scriptports.VidRushSearchRequest{SceneID: title, Text: title, Query: phrase, Limit: 10})
			cancel()
			if !isArtlistRateLimited(err) || attempt == 2 {
				break
			}
			backoff := time.Duration(1<<attempt) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return queryResult{err: fmt.Errorf("artlist query %q: retry canceled: %w", phrase, ctx.Err())}, nil
			}
		}
		if err != nil {
			return queryResult{err: fmt.Errorf("artlist query %q: %w", phrase, err)}, nil
		}
		match := ArtlistClipMatch{Phrase: phrase, Remote: true}
		for _, candidate := range candidates {
			link := strings.TrimSpace(candidate.SourceURL)
			if link == "" {
				link = strings.TrimSpace(candidate.PreviewURL)
			}
			if link == "" {
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

// VidRushRegistryImageSearcher adapts the shared registry to the image
// discovery port used by InternetImagesProcessor.
type VidRushRegistryImageSearcher struct {
	Registry *VidRushAssetProviderRegistry
}

func (s *VidRushRegistryImageSearcher) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if s == nil || s.Registry == nil {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	return s.Registry.Search(ctx, scriptpkg.VidRushProviderInternetImages, scriptports.VidRushSearchRequest{
		SegmentID: req.SegmentID, TextHash: req.TextHash, Text: req.Query, Query: req.Query, Limit: req.Limit,
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
	cache   scriptports.VidRushCachePort
	metrics VidRushMetrics
}

func NewVidRushProviderFanout(artlist ArtlistClipSearcher, images InternetImageSearcher, metrics ...VidRushMetrics) *VidRushProviderFanout {
	return NewVidRushProviderFanoutWithCache(artlist, images, nil, metrics...)
}

func NewVidRushProviderFanoutWithCache(artlist ArtlistClipSearcher, images InternetImageSearcher, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *VidRushProviderFanout {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushProviderFanout{artlist: artlist, images: images, cache: cache, metrics: m}
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
	perQueryLimit := 10
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		perQueryLimit = plan.MediaPlan.Planner.CandidateLimit
	}
	if perQueryLimit > 50 {
		perQueryLimit = 50
	}

	type providerOutcome struct {
		provider     string
		candidates   []scriptpkg.SegmentAssetCandidate
		primary      *scriptpkg.SegmentAssetCandidate
		allCacheHits bool
		err          error
	}

	artlistEnabled := plan.MediaPlan.ProviderPolicy.Artlist.AsBool() && f.artlist != nil && len(updated.Insights.ArtlistQueries) > 0
	imagesEnabled := plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && f.images != nil && len(updated.Insights.ImageQueries) > 0

	outcomes := make(chan providerOutcome, 2)
	var wg sync.WaitGroup

	// Snapshot the read-only segment inputs each provider goroutine needs so
	// no goroutine reads the shared `updated` result while the collector below
	// mutates its Assets fields. This makes the fanout merge race-free and
	// deterministic regardless of provider completion order.
	segmentID := updated.SegmentID
	textHash := updated.TextHash
	artlistQueries := append([]string(nil), updated.Insights.ArtlistQueries...)
	imageQueries := append([]string(nil), updated.Insights.ImageQueries...)
	firstEntity := ""
	if len(updated.Insights.Entities) > 0 {
		firstEntity = strings.TrimSpace(updated.Insights.Entities[0].Value)
	}
	artlistIdentity := scriptpkg.VidRushSegmentResult{SegmentID: segmentID}

	if artlistEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.metrics != nil {
				f.metrics.IncProviderRequest("artlist")
			}
			matches, err := f.artlist.SearchClips(ctx, plan.Title, artlistQueries)
			if err != nil {
				if f.metrics != nil {
					f.metrics.IncProviderFailure("artlist")
				}
				outcomes <- providerOutcome{provider: "artlist", err: err}
				return
			}
			candidates := artlistMatchesToCandidates(artlistIdentity, dedupeArtlistMatches(matches))
			var primary *scriptpkg.SegmentAssetCandidate
			if len(candidates) > 0 && readyVidRushCandidate(candidates[0]) {
				selected := candidates[0]
				primary = &selected
			}
			outcomes <- providerOutcome{provider: "artlist", candidates: candidates, primary: primary}
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
			segmentCacheKeyStr := segmentCacheKey(
				"internet-images-assets-v3",
				segmentID,
				textHash,
				plan.Language,
				plan.Model,
				plan.PromptVersion,
				fmt.Sprintf("%d", perQueryLimit),
			)
			if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
				if cached, ok := cacheLoad(&vidrushImageCache, segmentCacheKeyStr); ok {
					if payload, ok := cached.(internetImageCachePayload); ok {
						outcomes <- providerOutcome{provider: "internet_images", candidates: append([]scriptpkg.SegmentAssetCandidate(nil), payload.Candidates...), allCacheHits: true}
						return
					}
				}
				var persisted internetImageCachePayload
				if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "internet_images", segmentCacheKeyStr, &persisted); err != nil {
					outcomes <- providerOutcome{provider: "internet_images", err: err}
					return
				} else if hit {
					if len(persisted.Candidates) > 0 {
						cacheStore(&vidrushImageCache, segmentCacheKeyStr, persisted)
					}
					outcomes <- providerOutcome{provider: "internet_images", candidates: append([]scriptpkg.SegmentAssetCandidate(nil), persisted.Candidates...), allCacheHits: true}
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
				var results []scriptpkg.SegmentAssetCandidate
				fromCache := false
				if !plan.MediaPlan.ForceRefreshAssets && !plan.ForceRefresh {
					if cached, ok := cacheLoad(&entityImageCache, entityCacheKey); ok {
						if cachedCandidates, ok := cached.([]scriptpkg.SegmentAssetCandidate); ok {
							results = append([]scriptpkg.SegmentAssetCandidate(nil), cachedCandidates...)
							fromCache = true
						}
					}
					if !fromCache {
						var persisted []scriptpkg.SegmentAssetCandidate
						if hit, err := loadVidRushPersistentJSON(ctx, f.cache, "entity_images", entityCacheKey, &persisted); err != nil {
							outcomes <- providerOutcome{provider: "internet_images", err: err}
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
				if !fromCache {
					allCacheHits = false
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
						outcomes <- providerOutcome{provider: "internet_images", err: err}
						return
					}
					results = searched
					if len(results) > 0 {
						cacheStore(&entityImageCache, entityCacheKey, append([]scriptpkg.SegmentAssetCandidate(nil), results...))
					}
					if results == nil {
						results = []scriptpkg.SegmentAssetCandidate{}
					}
					if cacheErr := storeVidRushPersistentJSON(ctx, f.cache, "entity_images", entityCacheKey, results); cacheErr != nil {
						outcomes <- providerOutcome{provider: "internet_images", err: cacheErr}
						return
					}
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
				outcomes <- providerOutcome{provider: "internet_images", err: cacheErr}
				return
			}
			outcomes <- providerOutcome{provider: "internet_images", candidates: candidates, allCacheHits: allCacheHits}
		}()
	}
	go func() { wg.Wait(); close(outcomes) }()

	for outcome := range outcomes {
		if outcome.err != nil {
			if outcome.provider == scriptpkg.VidRushProviderArtlist && vidRushArtlistOnlyPlan(plan) {
				return updated, fmt.Errorf("vidrush provider fanout: required artlist search failed for segment %s: %w", updated.SegmentID, outcome.err)
			}
			continue
		}
		updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, outcome.candidates)
		switch outcome.provider {
		case scriptpkg.VidRushProviderArtlist:
			updated.Assets.PrimaryVideo = outcome.primary
			updated.Cache.Artlist = "MISS"
		case scriptpkg.VidRushProviderInternetImages:
			updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, outcome.candidates)
			updated.Cache.InternetImages = "MISS"
			if plan.MediaPlan.ForceRefreshAssets {
				updated.Cache.InternetImages = "REFRESHED"
			} else if outcome.allCacheHits {
				updated.Cache.InternetImages = "HIT_EXACT"
			}
		}
	}
	return updated, nil
}
