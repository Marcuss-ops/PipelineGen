package images

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"golang.org/x/sync/singleflight"
)

// resolverSearchProvider adapts the image retrieval resolver to the common
// provider search contract. It is search-only: it returns metadata and never
// calls the image download/ingest path.
type resolverSearchProvider struct {
	resolver routing.ImageSearchResolver
	flight   singleflight.Group
}

// NewResolverSearchProvider constructs the application-owned image search
// provider. The composition root supplies only the resolver port and receives
// the common provider port back.
func NewResolverSearchProvider(resolver routing.ImageSearchResolver) providers.SearchProvider {
	return &resolverSearchProvider{resolver: resolver}
}

func (p *resolverSearchProvider) Name() string { return "image" }

func (p *resolverSearchProvider) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilitySearch, providers.CapabilityImage}
}

func (p *resolverSearchProvider) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if p == nil || p.resolver == nil {
		return providers.SearchResult{}, errors.New("image search provider not wired")
	}
	key := imageSearchKey(req)
	value, err, _ := p.flight.Do(key, func() (any, error) {
		return p.searchUncached(ctx, req)
	})
	if err != nil {
		return providers.SearchResult{}, err
	}
	result, ok := value.(providers.SearchResult)
	if !ok {
		return providers.SearchResult{}, errors.New("image search provider: invalid coalesced result")
	}
	return cloneProviderSearchResult(result), nil
}

func (p *resolverSearchProvider) searchUncached(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	searcher, err := p.resolver.Resolve(routing.TerritoryRetrieved)
	if err != nil {
		return providers.SearchResult{}, err
	}
	if searcher == nil {
		return providers.SearchResult{}, errors.New("image search provider: retrieved searcher not wired")
	}
	hits, err := searcher.Search(ctx, routing.ImageFilter{
		SubjectID: strings.TrimSpace(req.Query),
		Origins:   []asset.ImageOrigin{asset.ImageOriginRetrieved},
		Tags:      append([]string(nil), req.Filters.Tags...),
		Limit:     req.Limit,
	})
	if err != nil {
		return providers.SearchResult{}, err
	}
	out := make([]providers.Candidate, 0, len(hits))
	for _, hit := range hits {
		id := firstNonEmptyImage(hit.AssetID, hit.SourcePageURL, hit.PreviewURL)
		out = append(out, providers.Candidate{
			Provider: hit.Provider, ExternalID: id, ID: id,
			Title: hit.Name, PageURL: hit.SourcePageURL,
			ThumbnailURL: hit.PreviewURL, PreviewURL: hit.PreviewURL,
			SourceRef: id, Width: hit.Width, Height: hit.Height,
			MediaType: asset.MediaType("image"), Score: hit.Score,
		})
	}
	return providers.SearchResult{Candidates: out}, nil
}

// imageSearchCacheKey is the typed, canonical identity for an image search.
// Length-prefixing each field keeps delimiters inside user data from creating
// collisions (for example, tags ["a,b"] and ["a", "b"]).
type resolverSearchCacheKey struct {
	Query          string
	Tags           []string
	MediaTypes     []string
	Category       string
	Language       string
	Sort           string
	PublishedAfter string
	MinDuration    time.Duration
	MaxDuration    time.Duration
	Limit          int
	TopicOnly      bool
}

func imageSearchKey(req providers.SearchRequest) string {
	tags := append([]string(nil), req.Filters.Tags...)
	sort.Strings(tags)
	mediaTypes := make([]string, 0, len(req.Filters.MediaTypes))
	for _, mediaType := range req.Filters.MediaTypes {
		mediaTypes = append(mediaTypes, string(mediaType))
	}
	sort.Strings(mediaTypes)
	publishedAfter := ""
	if req.Filters.PublishedAfter != nil {
		publishedAfter = req.Filters.PublishedAfter.UTC().Format(time.RFC3339Nano)
	}
	key := resolverSearchCacheKey{
		Query:          normalizeImageQuery(req.Query),
		Tags:           tags,
		MediaTypes:     mediaTypes,
		Category:       strings.TrimSpace(strings.ToLower(req.Filters.Category)),
		Language:       strings.TrimSpace(strings.ToLower(req.Filters.Language)),
		Sort:           string(req.Filters.Sort),
		PublishedAfter: publishedAfter,
		MinDuration:    req.Filters.MinDuration,
		MaxDuration:    req.Filters.MaxDuration,
		Limit:          req.Limit,
		TopicOnly:      req.TopicOnly,
	}
	return key.String()
}

func (k resolverSearchCacheKey) String() string {
	var b strings.Builder
	b.WriteString("image:v1:")
	writeImageCacheKeyField(&b, k.Query)
	b.WriteString(strconv.Itoa(len(k.Tags)))
	b.WriteByte(':')
	for _, tag := range k.Tags {
		writeImageCacheKeyField(&b, tag)
	}
	for _, mediaType := range k.MediaTypes {
		writeImageCacheKeyField(&b, mediaType)
	}
	writeImageCacheKeyField(&b, k.Category)
	writeImageCacheKeyField(&b, k.Language)
	writeImageCacheKeyField(&b, k.Sort)
	writeImageCacheKeyField(&b, k.PublishedAfter)
	writeImageCacheKeyField(&b, k.MinDuration.String())
	writeImageCacheKeyField(&b, k.MaxDuration.String())
	writeImageCacheKeyField(&b, strconv.Itoa(k.Limit))
	writeImageCacheKeyField(&b, strconv.FormatBool(k.TopicOnly))
	return b.String()
}

func writeImageCacheKeyField(b *strings.Builder, value string) {
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
	b.WriteByte('|')
}

func normalizeImageQuery(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}

func cloneProviderSearchResult(in providers.SearchResult) providers.SearchResult {
	in.Candidates = append([]providers.Candidate(nil), in.Candidates...)
	return in
}

func firstNonEmptyImage(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ providers.SearchProvider = (*resolverSearchProvider)(nil)
