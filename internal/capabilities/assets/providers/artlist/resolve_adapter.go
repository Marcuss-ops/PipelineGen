package assets

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

// ResolveAdapter is deliberately separate from Adapter: search is metadata
// only, while resolve is the opt-in materialization operation.
type ResolveAdapter struct {
	src interface {
		ImportClip(context.Context, *ImportClipRequest) (*ImportClipResponse, error)
	}
}

// GatewayAdapter is the single registry entry for Artlist. It exposes search
// and resolve as separate capability methods while keeping one stable provider
// identity in the registry.
type GatewayAdapter struct {
	search  *Adapter
	resolve *ResolveAdapter
}

func NewGatewayAdapter(src *Service) *GatewayAdapter {
	return &GatewayAdapter{search: NewAdapter(src), resolve: NewResolveAdapter(src)}
}

var _ providers.SearchProvider = (*GatewayAdapter)(nil)
var _ providers.FetchProvider = (*GatewayAdapter)(nil)

func (a *GatewayAdapter) Name() string { return "artlist" }
func (a *GatewayAdapter) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilitySearch, providers.CapabilityFetch, providers.CapabilityVideo, providers.CapabilityMusic}
}
func (a *GatewayAdapter) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	return a.search.Search(ctx, req)
}
func (a *GatewayAdapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchedAsset, error) {
	return a.resolve.Fetch(ctx, req)
}

var ErrResolveNotWired = errors.New("artlist resolve adapter: source not wired")

func NewResolveAdapter(src *Service) *ResolveAdapter { return &ResolveAdapter{src: src} }

var _ providers.FetchProvider = (*ResolveAdapter)(nil)

func (a *ResolveAdapter) Name() string { return "artlist" }
func (a *ResolveAdapter) Capabilities() []providers.Capability {
	return []providers.Capability{providers.CapabilityFetch, providers.CapabilityVideo, providers.CapabilityMusic}
}

func (a *ResolveAdapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchedAsset, error) {
	if a == nil || a.src == nil {
		return nil, ErrResolveNotWired
	}
	ref := strings.TrimSpace(req.SourceRef)
	if ref == "" {
		return nil, fmt.Errorf("artlist resolve: missing source reference")
	}
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		ref = "https://artlist.io/stock-footage/clip/" + url.PathEscape(ref)
	}
	resolved, err := a.src.ImportClip(ctx, &ImportClipRequest{ClipPageURL: ref, Download: true})
	if err != nil {
		return nil, fmt.Errorf("artlist resolve: %w", err)
	}
	if resolved == nil {
		return nil, fmt.Errorf("artlist resolve: empty response")
	}
	return &providers.FetchedAsset{FetchedAt: time.Now().UTC(), LocalPath: resolved.LocalPath}, nil
}
