// Package routing — search_resolver.go implements the canonical
// ImageSearchResolver that maps territory keys to ImageSearcher.
package routing

import (
	"context"
	"errors"
)

var ErrUnknownTerritory = errors.New("routing: unknown ImageSearchTerritory")

type resolverConfig struct {
	retrieved RetrievalSearchBackend
	repo      ImageListRepository
}

type Option func(*resolverConfig)

func WithRetrievalBackend(r RetrievalSearchBackend) Option {
	return func(c *resolverConfig) { c.retrieved = r }
}

func WithImageListRepository(r ImageListRepository) Option {
	return func(c *resolverConfig) { c.repo = r }
}

func NewImageSearchResolver(opts ...Option) (ImageSearchResolver, error) {
	cfg := resolverConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.retrieved == nil {
		return nil, errors.New("routing: missing WithRetrievalBackend option (required)")
	}
	if cfg.repo == nil {
		return nil, errors.New("routing: missing WithImageListRepository option (required)")
	}
	return &ImageSearchResolverImpl{
		retrieved: cfg.retrieved,
		repo:      cfg.repo,
	}, nil
}

type ImageSearchResolverImpl struct {
	retrieved RetrievalSearchBackend
	repo      ImageListRepository
}

var _ ImageSearchResolver = (*ImageSearchResolverImpl)(nil)

func (r *ImageSearchResolverImpl) Resolve(territory ImageSearchTerritory) (ImageSearcher, error) {
	if r == nil {
		return nil, errors.New("routing: nil resolver")
	}
	if !territory.IsValid() {
		return nil, ErrUnknownTerritory
	}
	switch territory {
	case TerritoryRetrieved:
		return newRetrievedSearcher(r.retrieved), nil
	case TerritoryGenerated:
		return newGeneratedSearcher(r.repo), nil
	case TerritoryAll:
		return newCompositeSearcher(r.retrieved, r.repo), nil
	}
	return nil, ErrUnknownTerritory
}

// ensure context package used (forward-decl for tests that read
// through context.Background via the consumer wiring).
var _ = context.Background
