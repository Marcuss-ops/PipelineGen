// Package resolver owns the provider-agnostic asset materialization use case.
// Search callers never need to know whether the selected provider uses an API,
// scraper, browser, or local staging implementation.
package assets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

var (
	ErrNotWired        = errors.New("asset resolver: provider registry not wired")
	ErrProviderMissing = errors.New("asset resolver: provider not registered")
	ErrUnsupported     = errors.New("asset resolver: provider does not support resolve")
	ErrAssetReference  = errors.New("asset resolver: asset reference is required")
)

// Request identifies an already-selected search result. SourceRef is the
// provider-native page or media reference; AssetID remains the stable identity
// used for logging and idempotency.
type Request struct {
	Source        string
	AssetID       string
	SourceRef     string
	DestinationID string
	SegmentStart  int64
	SegmentEnd    int64
	NoAudio       bool
}

type Service struct {
	registry *providers.Registry
}

func NewService(registry *providers.Registry) *Service {
	return &Service{registry: registry}
}

func (s *Service) Resolve(ctx context.Context, req Request) (*providers.FetchedAsset, error) {
	if s == nil || s.registry == nil {
		return nil, ErrNotWired
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return nil, fmt.Errorf("%w: source", ErrAssetReference)
	}
	ref := strings.TrimSpace(req.SourceRef)
	if ref == "" {
		return nil, fmt.Errorf("%w: source_ref", ErrAssetReference)
	}
	provider, ok := s.registry.Get(source)
	if !ok || provider == nil {
		return nil, fmt.Errorf("%w: %s", ErrProviderMissing, source)
	}
	fetcher, ok := provider.(providers.FetchProvider)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, source)
	}
	result, err := fetcher.Fetch(ctx, providers.FetchRequest{
		AssetID: req.AssetID, SourceRef: ref, DestinationID: req.DestinationID,
		NoAudio: req.NoAudio,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve %s/%s: %w", source, req.AssetID, err)
	}
	if result == nil {
		return nil, fmt.Errorf("resolve %s/%s: empty provider result", source, req.AssetID)
	}
	return result, nil
}
