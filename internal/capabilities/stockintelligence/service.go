// Package stockintelligence — service.go is the application-layer entry
// point that resolves a batch of ResolveRequests through the Resolver and
// aggregates the per-request results into a batch summary. It is the
// surface VidRush calls after the QueryPlanner and before the MediaSampler
// bindings.
package stockintelligence

import (
	"context"
	"fmt"
)

// Service is the application-layer stock-intelligence service. It owns a
// Resolver and applies it per request, aggregating the provider live
// request count so MediaCert can assert the LOCAL FIRST PROVIDER SECOND
// invariant across a whole run.
type Service struct {
	resolver *Resolver
}

// NewService constructs a Service. The resolver must be non-nil.
func NewService(resolver *Resolver) (*Service, error) {
	if resolver == nil {
		return nil, ErrResolverNotWired
	}
	return &Service{resolver: resolver}, nil
}

// Resolve exposes the single-request capability surface used by the VidRush
// pipeline. BatchResult remains available for catalog warmup and reporting;
// request execution must not duplicate resolver policy at the caller.
func (s *Service) Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error) {
	if s == nil || s.resolver == nil {
		return ResolveResult{}, ErrResolverNotWired
	}
	return s.resolver.Resolve(ctx, req)
}

// BatchResult is the aggregated outcome of resolving a batch of requests.
// TotalProviderLiveRequests is the metric MediaCert checks: 0 when
// local-first served the whole batch, >0 on any fallback.
type BatchResult struct {
	Results                   []ResolveResult `json:"results"`
	TotalProviderLiveRequests int             `json:"total_provider_live_requests"`
	LocalFirstServedCount     int             `json:"local_first_served_count"`
}

// ResolveBatch resolves each request in order, aggregating the provider
// live request count. A single request error short-circuits the batch so
// a semantically wrong partial batch is never silently returned.
func (s *Service) ResolveBatch(ctx context.Context, reqs []ResolveRequest) (BatchResult, error) {
	results := make([]ResolveResult, 0, len(reqs))
	total := 0
	served := 0
	for _, req := range reqs {
		res, err := s.resolver.Resolve(ctx, req)
		if err != nil {
			return BatchResult{}, fmt.Errorf("stockintelligence: resolve segment %q: %w", req.SegmentID, err)
		}
		if res.ProviderLiveRequests == 0 {
			served++
		}
		total += res.ProviderLiveRequests
		results = append(results, res)
	}
	return BatchResult{
		Results:                   results,
		TotalProviderLiveRequests: total,
		LocalFirstServedCount:     served,
	}, nil
}
