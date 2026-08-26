// Package routing — image_search_resolver.go implements the canonical
// ImageSearchResolver with functional-options construction. FASE 8
// (July 2026, image-territories action plan): this file was missing
// from the initial FASE 8 commit — the concrete resolver surface was
// referenced by tests (image_search_resolver_test.go) and the
// composition root (build_bundles_domain.go) but the implementation
// file was not committed. This file provides:
//
//   - ImageSearchResolverImpl (concrete struct, satisfies ImageSearchResolver)
//   - NewImageSearchResolver (functional-options constructor)
//   - WithRetrievalBackend / WithImageListRepository option builders
//   - ErrUnknownTerritory sentinel (one canonical definition; tests + build_bundles use it)
//
// EXPAND-phase: minimal surface, full impl in Wave 1.5.
package images

import (
	"context"
	"errors"
	"fmt"
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ErrUnknownTerritory is returned by Resolve when the territory
// constant is invalid. Mirrors the Err* sentinel pattern in
// search_resolver.go (errRouting unexported type collocated with
// the enums). The canonical definition lives here since the
// resolver is the only consumer that gates on IsValid() at the
// Resolve method boundary.
var ErrUnknownTerritory = errors.New("routing.Resolve: unknown territory")

// ImageSearchResolverImpl is the concrete implementation of
// ImageSearchResolver. It wires the two canonical backends
// (retrieval + generated/list) into territory-scoped ImageSearcher
// instances via lazy construction at Resolve time.
type ImageSearchResolverImpl struct {
	retrieved RetrievalSearchBackend
	repo      ImageListRepository
}

// compile-time assertion: ImageSearchResolverImpl satisfies ImageSearchResolver.
var _ ImageSearchResolver = (*ImageSearchResolverImpl)(nil)

// ImageSearchResolverOption is the functional-options parameter
// accepted by NewImageSearchResolver. The struct is unexported
// so callers can only create options through the exported With*
// builders.
type ImageSearchResolverOption func(*ImageSearchResolverImpl)

// WithRetrievalBackend injects the RetrievalSearchBackend port.
// Must be non-nil; NewImageSearchResolver fails-closed when
// omitted (godlike/07 no-fake-availability).
func WithRetrievalBackend(b RetrievalSearchBackend) ImageSearchResolverOption {
	return func(r *ImageSearchResolverImpl) {
		r.retrieved = b
	}
}

// WithImageListRepository injects the ImageListRepository port.
// Must be non-nil; NewImageSearchResolver fails-closed when
// omitted.
func WithImageListRepository(repo ImageListRepository) ImageSearchResolverOption {
	return func(r *ImageSearchResolverImpl) {
		r.repo = repo
	}
}

// NewImageSearchResolver constructs the canonical resolver from
// functional options. At least WithRetrievalBackend (non-nil) and
// WithImageListRepository (non-nil) are required; omitting either
// returns an error (fail-closed per godlike/07).
func NewImageSearchResolver(opts ...ImageSearchResolverOption) (ImageSearchResolver, error) {
	r := &ImageSearchResolverImpl{}
	for _, o := range opts {
		o(r)
	}
	if r.retrieved == nil {
		return nil, errors.New("routing.NewImageSearchResolver: WithRetrievalBackend is required (nil backend — fail-closed per godlike/07)")
	}
	if r.repo == nil {
		return nil, errors.New("routing.NewImageSearchResolver: WithImageListRepository is required (nil repo — fail-closed per godlike/07)")
	}
	return r, nil
}

// Resolve returns a territory-scoped ImageSearcher for the given
// ImageSearchTerritory. The searcher is a lightweight wrapper over
// the wired backends; it carries no mutable state and is safe for
// concurrent calls.
func (r *ImageSearchResolverImpl) Resolve(territory ImageSearchTerritory) (ImageSearcher, error) {
	if r == nil {
		return nil, ErrUnknownTerritory
	}
	switch territory {
	case TerritoryRetrieved:
		return newRetrievedSearcher(r.retrieved), nil
	case TerritoryGenerated:
		return newGeneratedSearcher(r.repo), nil
	case TerritoryAll:
		return newCompositeSearcher(r.retrieved, r.repo), nil
	default:
		return nil, ErrUnknownTerritory
	}
}

// ResolveProvider returns a retrieved-territory searcher pinned to one
// registered provider. It is intentionally an optional capability on the
// resolver: legacy backends continue to support Resolve(TerritoryRetrieved),
// while provider-enabled callers fail closed when explicit selection is not
// available.
func (r *ImageSearchResolverImpl) ResolveProvider(provider string) (ImageSearcher, error) {
	if r == nil || r.retrieved == nil {
		return nil, ErrUnknownTerritory
	}
	backend, ok := r.retrieved.(RetrievalProviderSearchBackend)
	if !ok {
		return nil, fmt.Errorf("routing.ResolveProvider: explicit provider selection unavailable")
	}
	return &retrievedProviderSearcher{backend: backend, provider: provider}, nil
}

// ExistingImages returns retrieved images already persisted for a subject.
// It is an optional read-through seam for callers that want to reuse durable
// images before contacting an external provider.
func (r *ImageSearchResolverImpl) ExistingImages(ctx context.Context, subject string, limit int) ([]ImageSearchResult, error) {
	if r == nil || r.repo == nil {
		return nil, nil
	}
	subjectID := textutil.Slugify(subject)
	if subjectID == "" {
		return nil, nil
	}
	return r.repo.ListImages(ctx, ImageFilter{
		SubjectID: subjectID,
		Origins:   []detail.ImageOrigin{detail.ImageOriginRetrieved},
		Limit:     ResolvedLimit(limit),
	})
}

type retrievedProviderSearcher struct {
	backend  RetrievalProviderSearchBackend
	provider string
}

func (s *retrievedProviderSearcher) Search(ctx context.Context, filter ImageFilter) ([]ImageSearchResult, error) {
	if s == nil || s.backend == nil {
		return nil, nil
	}
	hits, err := s.backend.SearchProvider(ctx, s.provider, filter.SubjectID, RetrievalSearchOptions{Limit: ResolvedLimit(filter.Limit)})
	if err != nil {
		return nil, err
	}
	out := make([]ImageSearchResult, 0, len(hits))
	for _, h := range hits {
		out = append(out, ImageSearchResult{
			Origin:        string(detail.ImageOriginRetrieved),
			Provider:      string(h.Provider),
			Name:          h.Title,
			PreviewURL:    h.PreviewURL,
			SourcePageURL: h.PageURL,
			Width:         h.Width,
			Height:        h.Height,
			Score:         1,
			StyleID:       h.StyleID,
			License:       h.License,
			Author:        h.Author,
		})
	}
	return out, nil
}
