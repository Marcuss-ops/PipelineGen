package script

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// internetImageSearchAdapter wraps the canonical ImageSearchResolver
// into the adapters.InternetImageSearcher port.
type internetImageSearchAdapter struct {
	resolver imagesrouting.ImageSearchResolver
	log      *zap.Logger
	cache    *imageSearchCache
}

func NewInternetImageSearchAdapter(resolver imagesrouting.ImageSearchResolver, log *zap.Logger) *internetImageSearchAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &internetImageSearchAdapter{resolver: resolver, log: log, cache: newImageSearchCache()}
}

// SearchImages is the single entry point every consumer uses for internet
// image discovery. Requests for the same canonical subject share one provider
// answer inside the worker: the semantic planner, the VidRush fan-out and the
// internet_images postprocessor no longer repeat the same search. ForceRefresh
// deliberately bypasses the cache because the caller is re-validating bytes.
func (a *internetImageSearchAdapter) SearchImages(ctx context.Context, req adapters.InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if a == nil || a.resolver == nil {
		return nil, fmt.Errorf("internetImageSearchAdapter: resolver not wired")
	}
	if req.ForceRefresh || a.cache == nil {
		return a.searchImagesUncached(ctx, req)
	}
	key := imageSearchCacheKey(req)
	if candidates, ok := a.cache.get(key); ok {
		return candidates, nil
	}
	value, err, _ := a.cache.flight.Do(key, func() (any, error) {
		if candidates, ok := a.cache.get(key); ok {
			return candidates, nil
		}
		candidates, searchErr := a.searchImagesUncached(ctx, req)
		if searchErr != nil {
			return nil, searchErr
		}
		a.cache.put(key, candidates)
		return cloneImageSearchCandidates(candidates), nil
	})
	if err != nil {
		return nil, err
	}
	candidates, ok := value.([]scriptpkg.SegmentAssetCandidate)
	if !ok {
		return nil, fmt.Errorf("internetImageSearchAdapter: invalid cached result type")
	}
	return candidates, nil
}

func (a *internetImageSearchAdapter) searchImagesUncached(ctx context.Context, req adapters.InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if a == nil || a.resolver == nil {
		return nil, fmt.Errorf("internetImageSearchAdapter: resolver not wired")
	}
	var searcher imagesrouting.ImageSearcher
	var err error
	toCandidate := func(r imagesrouting.ImageSearchResult) scriptpkg.SegmentAssetCandidate {
		// Preserve the provider that actually answered (duckduckgo,
		// wikimedia_commons, searxng): the class-routing report and the durable
		// plan cache invalidate per provider, so flattening every hit into the
		// generic "internet_images" label would make both impossible.
		provider := strings.TrimSpace(string(r.Provider))
		if provider == "" {
			provider = "internet_images"
		}
		assetID := strings.TrimSpace(r.AssetID)
		if assetID == "" {
			assetID = strings.TrimSpace(r.Name)
		}
		if assetID == "" {
			sum := digest.SHA256Bytes([]byte(req.SegmentID + "\x00" + req.Query + "\x00" + r.PreviewURL))
			assetID = sum
		}
		return scriptpkg.SegmentAssetCandidate{
			AssetID: assetID, Provider: provider, Query: strings.TrimSpace(req.Query),
			Entity: strings.TrimSpace(req.Entity), SourceURL: strings.TrimSpace(r.PreviewURL),
			SourcePageURL: strings.TrimSpace(r.SourcePageURL), PreviewURL: strings.TrimSpace(r.PreviewURL),
			DriveLink: strings.TrimSpace(r.DriveLink), ContentSHA256: strings.TrimSpace(r.ContentSHA256),
			Score: r.Score, Width: r.Width, Height: r.Height,
			RightsStatus: retrievedImageRightsStatus(r.License), RightsBasis: retrievedImageRightsBasis(r.License, r.Author),
		}
	}
	// Reuse durable retrieved images first — unless the run asked for a force
	// refresh. The bytes behind a stored URL can change upstream (source
	// re-encode), and a recorded hash is only a claim until bytes are
	// re-downloaded and re-hashed in-run. Refresh runs always take the live
	// provider path below so overlay hashes stay content-addressed; a stale
	// recorded hash reaching the render lane fails the whole job at L3.
	if lookup, ok := a.resolver.(interface {
		ExistingImages(context.Context, string, int) ([]imagesrouting.ImageSearchResult, error)
	}); ok && !req.ForceRefresh {
		cached, lookupErr := lookup.ExistingImages(ctx, req.Query, req.Limit)
		if lookupErr != nil {
			a.log.Warn("VidRush existing image lookup failed; falling back to DuckDuckGo", zap.String("query", req.Query), zap.Error(lookupErr))
		} else if len(cached) > 0 {
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			rng.Shuffle(len(cached), func(i, j int) { cached[i], cached[j] = cached[j], cached[i] })
			out := make([]scriptpkg.SegmentAssetCandidate, 0, len(cached))
			for _, row := range cached {
				if strings.TrimSpace(row.DriveLink) == "" || strings.TrimSpace(row.ContentSHA256) == "" {
					continue
				}
				candidate := toCandidate(row)
				candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
				candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
				candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
				candidate.IndexStatus = "pending"
				out = append(out, candidate)
			}
			if len(out) > 0 {
				a.log.Info("VidRush reused persisted image candidates", zap.String("query", req.Query), zap.Int("count", len(out)))
				return out, nil
			}
		}
	}
	// Query-class routing: an institution, place or object has a licensed
	// encyclopedic asset, so Commons is asked first and a hit avoids the broad
	// web provider entirely. A person is the opposite case: Commons returns
	// nothing for proper names, so it is not asked at all. The empty class keeps
	// the historical DDG-first cascade for every caller that does not route.
	filter := imagesrouting.ImageFilter{
		SubjectID: strings.TrimSpace(req.Query),
		Limit:     req.Limit,
	}
	// Every provider query gets a hard deadline. The common postprocessor
	// deadline is intentionally longer because it also covers acquisition,
	// verification and finalization; an unreachable retrieval backend must
	// never consume that entire budget.
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	class := strings.ToLower(strings.TrimSpace(req.QueryClass))
	personRoute := class == "person"
	editorialRoute := class == "organization" || class == "location" || class == "object"
	var results []imagesrouting.ImageSearchResult
	if editorialRoute {
		if explicit, ok := a.resolver.(interface {
			ResolveProvider(string) (imagesrouting.ImageSearcher, error)
		}); ok {
			if commonsSearcher, commonsErr := explicit.ResolveProvider("wikimedia_commons"); commonsErr == nil {
				if commons, searchErr := commonsSearcher.Search(queryCtx, filter); searchErr == nil && len(commons) > 0 {
					results = commons
				}
			}
		}
	}
	if len(results) == 0 {
		// VidRush internet_images is explicitly backed by DuckDuckGo. Resolve
		// it through the same routing/registry seam so provider selection
		// remains centralized and candidate provenance is preserved.
		if explicit, ok := a.resolver.(interface {
			ResolveProvider(string) (imagesrouting.ImageSearcher, error)
		}); ok {
			searcher, err = explicit.ResolveProvider("duckduckgo")
		} else {
			searcher, err = a.resolver.Resolve(imagesrouting.TerritoryRetrieved)
		}
		if err != nil {
			return nil, err
		}
		var searchErr error
		results, searchErr = searcher.Search(queryCtx, filter)
		if searchErr != nil {
			a.log.Warn("VidRush internet image search failed", zap.String("query", req.Query), zap.Error(searchErr))
			// A provider refusal is not an empty result. The licensed fallbacks
			// below (Commons, then SearXNG) are independent retrieval surfaces
			// and must still run: returning here is what let a challenged
			// DuckDuckGo IP starve every entity image in a run. Only a cancelled
			// parent context makes the fallbacks meaningless.
			if queryCtx.Err() != nil {
				return nil, searchErr
			}
			results = nil
		}
	}
	// Concise identity queries get an explicit-license Wikimedia fallback in
	// addition to DuckDuckGo. DDG often returns hotlink-protected thumbnails;
	// Commons supplies a durable, license-bearing original that can pass the
	// same download/verify/finalize lifecycle. Long scene-text queries remain
	// DDG-only and do not create needless external fan-out.
	if !editorialRoute && strings.TrimSpace(req.Entity) != "" && len(strings.Fields(strings.TrimSpace(req.Query))) <= 4 {
		if !personRoute {
			if explicit, ok := a.resolver.(interface {
				ResolveProvider(string) (imagesrouting.ImageSearcher, error)
			}); ok {
				if commonsSearcher, commonsErr := explicit.ResolveProvider("wikimedia_commons"); commonsErr == nil {
					commons, searchErr := commonsSearcher.Search(queryCtx, filter)
					if searchErr != nil {
						a.log.Warn("VidRush Wikimedia Commons fallback failed", zap.String("query", req.Query), zap.Error(searchErr))
					} else if len(commons) > 0 && strings.EqualFold(strings.TrimSpace(commons[0].Provider), "wikimedia_commons") {
						results = append(commons, results...)
						if req.Limit > 0 && len(results) > req.Limit {
							results = results[:req.Limit]
						}
					}
				}
			}
		}
		// Fill a thin durable PERSON pool with a second, focused search. The
		// catalog refresh path explicitly bypasses the old retrieved-image
		// shortcut, so this query reaches providers and can contribute new URLs.
		variant := strings.TrimSpace(req.Entity) + " portrait"
		variantFilter := filter
		variantFilter.SubjectID = variant
		variantResults, variantErr := searcher.Search(queryCtx, variantFilter)
		if variantErr != nil {
			a.log.Warn("VidRush portrait image search failed", zap.String("query", variant), zap.Error(variantErr))
		} else {
			results = append(results, variantResults...)
		}
		if !personRoute {
			if explicit, ok := a.resolver.(interface {
				ResolveProvider(string) (imagesrouting.ImageSearcher, error)
			}); ok {
				if commonsSearcher, commonsErr := explicit.ResolveProvider("wikimedia_commons"); commonsErr == nil {
					commons, searchErr := commonsSearcher.Search(queryCtx, variantFilter)
					if searchErr != nil {
						a.log.Warn("VidRush Wikimedia Commons portrait search failed", zap.String("query", variant), zap.Error(searchErr))
					} else {
						results = append(commons, results...)
					}
				}
			}
		}
	}
	// Long scene queries often return non-empty DDG results that are unusable
	// thumbnails or hotlink-protected pages. An empty-only fallback therefore
	// leaves the materializer with a full list of bad candidates and no chance
	// to recover. Add independent SearXNG candidates for context searches even
	// when DDG returned rows; downstream acquisition and image decoding decide
	// which sources are actually usable. Keep short identity searches on their
	// dedicated person/Commons path so this does not weaken portrait matching.
	// Empty DDG rows always get the recovery path — that is the original
	// fallback and short identity queries rely on it. The always-on fan-out is
	// limited to genuinely long scene queries (or a query that is itself the
	// entity), so a short "John Cena"-shaped search still hits the provider once.
	longContextQuery := strings.EqualFold(strings.TrimSpace(req.Query), strings.TrimSpace(req.Entity)) || len(strings.Fields(strings.TrimSpace(req.Query))) > 4
	// A routed editorial hit already carries a licensed asset from Commons;
	// adding a broad-web provider would spend the budget the route just saved.
	if len(results) == 0 || (!editorialRoute && longContextQuery) {
		if explicit, ok := a.resolver.(interface {
			ResolveProvider(string) (imagesrouting.ImageSearcher, error)
		}); ok {
			if fallback, fallbackErr := explicit.ResolveProvider("searxng"); fallbackErr == nil {
				fallbackResults, searchErr := fallback.Search(queryCtx, filter)
				if searchErr != nil {
					a.log.Warn("VidRush SearXNG image fallback failed", zap.String("query", req.Query), zap.Error(searchErr))
				} else if len(fallbackResults) > 0 {
					a.log.Info("VidRush SearXNG context search added candidates", zap.String("query", req.Query), zap.Int("count", len(fallbackResults)), zap.Int("ddg_count", len(results)))
					results = interleaveImageSearchResults(results, fallbackResults)
				}
			}
		}
	}
	// Keep provider diagnostics at the VidRush boundary. The downstream
	// materializer deliberately drops candidates that cannot be acquired,
	// verified and persisted, so without this count an empty final binding
	// cannot distinguish an empty search from an acquisition rejection.
	if len(results) == 0 {
		a.log.Info("VidRush internet image search returned no candidates", zap.String("query", req.Query))
		return nil, nil
	}
	a.log.Info("VidRush internet image search returned candidates", zap.String("query", req.Query), zap.Int("count", len(results)))
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	metadataRejected := 0
	for _, r := range results {
		key := strings.TrimSpace(r.PreviewURL)
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		candidate := toCandidate(r)
		if !imageCandidateMeetsMinimumMetadata(candidate.Width, candidate.Height) {
			metadataRejected++
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	if metadataRejected > 0 {
		a.log.Debug("VidRush image search skipped candidates below the pixel floor",
			zap.String("query", req.Query), zap.Int("rejected", metadataRejected))
	}
	return out, nil
}

// imageCandidateMeetsMinimumMetadata avoids spending a network request on a
// provider result that already declares dimensions below the decoder policy.
// Unknown dimensions remain eligible because the downloader verifies bytes.
func imageCandidateMeetsMinimumMetadata(width, height int) bool {
	policy := adapters.DefaultVidRushImagePolicy()
	return (width <= 0 || width >= policy.MinWidth) && (height <= 0 || height >= policy.MinHeight)
}

// interleaveImageSearchResults keeps candidates from both independent
// providers inside the fanout's bounded limit. Appending SearXNG after DDG
// meant the registry truncated the slice back to DDG-only before acquisition.
func interleaveImageSearchResults(primary, alternatives []imagesrouting.ImageSearchResult) []imagesrouting.ImageSearchResult {
	out := make([]imagesrouting.ImageSearchResult, 0, len(primary)+len(alternatives))
	for i := 0; i < len(primary) || i < len(alternatives); i++ {
		if i < len(alternatives) {
			out = append(out, alternatives[i])
		}
		if i < len(primary) {
			out = append(out, primary[i])
		}
	}
	return out
}

func retrievedImageRightsStatus(license string) string {
	license = strings.TrimSpace(license)
	if license != "" && !strings.EqualFold(license, "unknown") && !strings.EqualFold(license, "unverified") {
		return "verified"
	}
	return "unknown_allowed"
}

func retrievedImageRightsBasis(license, author string) string {
	license = strings.TrimSpace(license)
	author = strings.TrimSpace(author)
	if license == "" || strings.EqualFold(license, "unknown") || strings.EqualFold(license, "unverified") {
		return "source-license metadata required"
	}
	if author == "" {
		return "source license metadata: " + license
	}
	return "source license metadata: " + license + "; author: " + author
}

var _ adapters.InternetImageSearcher = (*internetImageSearchAdapter)(nil)
