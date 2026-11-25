package script

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	imagesrouting "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

const (
	imageSearchProviderTimeout = 6 * time.Second
	searxImageSearchTimeout    = 8 * time.Second
)

// searchImageProvider gives one provider a bounded slice of the caller's
// query budget. The child deadline is cancelled on return and never consumes
// the parent context, so a slow provider cannot starve the next fallback.
func searchImageProvider(ctx context.Context, timeout time.Duration, searcher imagesrouting.ImageSearcher, filter imagesrouting.ImageFilter) ([]imagesrouting.ImageSearchResult, error) {
	if timeout <= 0 {
		timeout = imageSearchProviderTimeout
	}
	providerCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return searcher.Search(providerCtx, filter)
}

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

// searchImagesUncached is the orchestration: reuse durable rows when allowed,
// otherwise run the provider cascade and project the results.
func (a *internetImageSearchAdapter) searchImagesUncached(ctx context.Context, req adapters.InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if a == nil || a.resolver == nil {
		return nil, fmt.Errorf("internetImageSearchAdapter: resolver not wired")
	}
	if candidates, ok := a.reusePersistedImageCandidates(ctx, req); ok {
		return candidates, nil
	}

	results, err := a.searchImageProviders(ctx, req)
	if err != nil {
		return nil, err
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
	return a.buildImageCandidates(req, results), nil
}

// reusePersistedImageCandidates tries the durable retrieved-image rows first.
// The bytes behind a stored URL can change upstream, and a recorded hash is
// only a claim until bytes are re-downloaded and re-hashed in-run, so refresh
// runs always take the live provider path.
func (a *internetImageSearchAdapter) reusePersistedImageCandidates(ctx context.Context, req adapters.InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, bool) {
	lookup, ok := a.resolver.(interface {
		ExistingImages(context.Context, string, int) ([]imagesrouting.ImageSearchResult, error)
	})
	if !ok || req.ForceRefresh {
		return nil, false
	}
	// The durable row is keyed by the ENTITY, while the provider query can
	// carry retrieval qualifiers ("Corrado Augias portrait"). Resolving the
	// entity first is what makes a warm reuse hit regardless of the query
	// variant; the raw query remains the fallback for contextual searches.
	subjects := make([]string, 0, 2)
	for _, subject := range []string{req.Entity, req.Query} {
		subject = strings.TrimSpace(subject)
		if subject == "" || slices.Contains(subjects, subject) {
			continue
		}
		subjects = append(subjects, subject)
	}
	var cached []imagesrouting.ImageSearchResult
	seenCached := make(map[string]struct{})
	for _, subject := range subjects {
		rows, lookupErr := lookup.ExistingImages(ctx, subject, req.Limit)
		if lookupErr != nil {
			a.log.Warn("VidRush existing image lookup failed; falling back to DuckDuckGo", zap.String("subject", subject), zap.Error(lookupErr))
			continue
		}
		for _, row := range rows {
			key := strings.TrimSpace(row.AssetID)
			if key == "" {
				key = strings.TrimSpace(row.DriveLink)
			}
			if key != "" {
				if _, dup := seenCached[key]; dup {
					continue
				}
				seenCached[key] = struct{}{}
			}
			cached = append(cached, row)
		}
		if req.Limit > 0 && len(cached) >= req.Limit {
			break
		}
	}
	if len(cached) == 0 {
		return nil, false
	}

	// A time-seeded shuffle made the same plan bind different images on every
	// run. The seed is now the retrieval subject, and the rows are sorted by
	// content identity first, so a warm catalog replay is reproducible without
	// giving up variety across different subjects.
	cached = orderExistingImageResults(cached, imageSearchShuffleSeed(req))
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(cached))
	for _, row := range cached {
		if strings.TrimSpace(row.DriveLink) == "" || strings.TrimSpace(row.ContentSHA256) == "" {
			continue
		}
		candidate := internetImageCandidate(req, row)
		candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
		candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
		candidate.PersistenceStatus = scriptpkg.VidRushStatusPersisted
		candidate.IndexStatus = "pending"
		out = append(out, candidate)
	}
	if len(out) == 0 {
		return nil, false
	}
	a.log.Info("VidRush reused persisted image candidates", zap.String("query", req.Query), zap.Int("count", len(out)))
	return out, true
}

// searchImageProviders runs the provider cascade: Commons for editorial
// subjects, DuckDuckGo as the primary web provider, encyclopedic/portrait
// supplements for short identity queries and SearXNG for long context
// queries. A provider refusal is not an empty result: independent fallbacks
// still run unless the parent context is cancelled.
func (a *internetImageSearchAdapter) searchImageProviders(ctx context.Context, req adapters.InternetImageSearchRequest) ([]imagesrouting.ImageSearchResult, error) {
	var searcher imagesrouting.ImageSearcher
	var err error

	// Query-class routing: an institution, place or object has a licensed
	// encyclopedic asset, so Commons is asked first and a hit avoids the broad
	// web provider entirely. A person also benefits from Wikipedia/Commons
	// pageimages when an encyclopedic article exists, while keeping DuckDuckGo
	// as fallback/supplement for non-encyclopedic subjects and era-specific
	// photos. The empty class keeps the historical DDG-first cascade.
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
	editorialRoute := class == "organization" || class == "brand" || class == "location" || class == "object"
	var results []imagesrouting.ImageSearchResult
	if editorialRoute {
		if explicit, ok := a.resolver.(interface {
			ResolveProvider(string) (imagesrouting.ImageSearcher, error)
		}); ok {
			if commonsSearcher, commonsErr := explicit.ResolveProvider("wikimedia_commons"); commonsErr == nil {
				if commons, searchErr := searchImageProvider(queryCtx, imageSearchProviderTimeout, commonsSearcher, filter); searchErr == nil && len(commons) > 0 {
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
		results, searchErr = searchImageProvider(queryCtx, imageSearchProviderTimeout, searcher, filter)
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
		if explicit, ok := a.resolver.(interface {
			ResolveProvider(string) (imagesrouting.ImageSearcher, error)
		}); ok {
			providerName := "wikimedia_commons"
			commonsFilter := filter
			if personRoute {
				providerName = "wikipedia"
				commonsFilter.SubjectID = strings.TrimSpace(req.Entity)
			}
			if wikiSearcher, wikiErr := explicit.ResolveProvider(providerName); wikiErr == nil {
				wikiResults, searchErr := searchImageProvider(queryCtx, imageSearchProviderTimeout, wikiSearcher, commonsFilter)
				if searchErr != nil {
					a.log.Warn("VidRush encyclopedic fallback failed", zap.String("provider", providerName), zap.String("query", commonsFilter.SubjectID), zap.Error(searchErr))
				} else if len(wikiResults) > 0 && strings.EqualFold(strings.TrimSpace(string(wikiResults[0].Provider)), providerName) {
					results = append(wikiResults, results...)
					if req.Limit > 0 && len(results) > req.Limit {
						results = results[:req.Limit]
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
		variantResults, variantErr := searchImageProvider(queryCtx, imageSearchProviderTimeout, searcher, variantFilter)
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
					commons, searchErr := searchImageProvider(queryCtx, imageSearchProviderTimeout, commonsSearcher, variantFilter)
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
	if len(results) == 0 || (!editorialRoute && !personRoute && longContextQuery) {
		if explicit, ok := a.resolver.(interface {
			ResolveProvider(string) (imagesrouting.ImageSearcher, error)
		}); ok {
			if fallback, fallbackErr := explicit.ResolveProvider("searxng"); fallbackErr == nil {
				fallbackResults, searchErr := searchImageProvider(queryCtx, searxImageSearchTimeout, fallback, filter)
				if searchErr != nil {
					a.log.Warn("VidRush SearXNG image fallback failed", zap.String("query", req.Query), zap.Error(searchErr))
				} else if len(fallbackResults) > 0 {
					a.log.Info("VidRush SearXNG context search added candidates", zap.String("query", req.Query), zap.Int("count", len(fallbackResults)), zap.Int("ddg_count", len(results)))
					results = interleaveImageSearchResults(results, fallbackResults)
				}
			}
		}
	}
	return results, nil
}

// buildImageCandidates projects provider rows, dropping empty URLs, duplicates
// and candidates already below the pixel floor.
func (a *internetImageSearchAdapter) buildImageCandidates(req adapters.InternetImageSearchRequest, results []imagesrouting.ImageSearchResult) []scriptpkg.SegmentAssetCandidate {
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
		candidate := internetImageCandidate(req, r)
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
	return out
}

// internetImageCandidate preserves the provider that actually answered
// (duckduckgo, wikimedia_commons, searxng): the class-routing report and the
// durable plan cache invalidate per provider, so flattening every hit into the
// generic "internet_images" label would make both impossible.
func internetImageCandidate(req adapters.InternetImageSearchRequest, r imagesrouting.ImageSearchResult) scriptpkg.SegmentAssetCandidate {
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

// imageCandidateMeetsMinimumMetadata avoids spending a network request on a
// provider result that already declares dimensions below the decoder policy.
// Unknown dimensions remain eligible because the downloader verifies bytes.
func imageCandidateMeetsMinimumMetadata(width, height int) bool {
	policy := adapters.DefaultVidRushImagePolicy()
	// Unknown dimensions stay eligible: the downloader verifies the real
	// header. Known dimensions use the same orientation-aware policy as the
	// byte verifier, so a 600x900 portrait is not dropped for being narrow.
	if width <= 0 || height <= 0 {
		return true
	}
	return policy.AcceptsDimensions(width, height) && policy.WithinPixelBudget(width, height)
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
