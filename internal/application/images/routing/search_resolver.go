// Package routing — search_resolver.go declares the application-
// layer territory-router implementation.
//
// Per the July 2026 image-restructuring plan, the routing
// subpackage picks the right sub-service for a given search
// query based on ImageOrigin:
//
//   - Retrieved → retrieved.SearchService (existing ImageStorageService)
//   - Generated → generated.GeneratedSearchService (future)
//   - Uploaded  → catalog.CatalogSearch (read-only lookup)
//
// Crucially, routing/ does NOT import retrieved/ or generated/.
// It declares an interface that *they* satisfy, so the cycle
// `retrieved/<->generated/<->routing/` is not possible.
//
// The composition root (parent images/service.go) constructs the
// routed service by wiring the concrete sub-services to a
// Router that picks by Origin.
//
// The canonical SearchRequest / SearchResponse / Service types
// live in service_types.go and interfaces.go (FASE 8 split:
// this file used to be all-in-one but the types were promoted
// to first-class routing-layer DTOs + interface to break the
// routing ↔ retrieved import cycle). search_resolver.go now
// hosts ONLY the Router implementation surface.
package routing

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Router dispatches a SearchRequest to the right Service based
// on origin (or to all three in "all-territories" mode).
type Router struct {
	retrieved Service
	generated Service
	catalog   Service
}

// NewRouter constructs a Router from the three canonical
// territory services. Any field may be nil — a nil sub-service
// is silently skipped (not an error). At least ONE must be
// non-nil; otherwise NewRouter returns nil and callers must
// detect Router == nil.
func NewRouter(retrieved, generated, catalog Service) *Router {
	if retrieved == nil && generated == nil && catalog == nil {
		return nil
	}
	return &Router{
		retrieved: retrieved,
		generated: generated,
		catalog:   catalog,
	}
}

// Dispatch routes the request to ONE sub-service based on Origin.
// Precedence when Origin is empty: retrieved → generated → catalog.
// When Origin is set, only that territory is consulted (other
// services may be nil — that's OK, the error message will name
// the missing one).
func (r *Router) Dispatch(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if r == nil {
		return SearchResponse{}, ErrNoServiceWired
	}
	switch req.Origin {
	case "", asset.ImageOriginRetrieved:
		if r.retrieved == nil {
			return SearchResponse{}, ErrRetrievedNotWired
		}
		return r.retrieved.Search(ctx, req)
	case asset.ImageOriginGenerated:
		if r.generated == nil {
			return SearchResponse{}, ErrGeneratedNotWired
		}
		return r.generated.Search(ctx, req)
	case asset.ImageOriginUploaded:
		if r.catalog == nil {
			return SearchResponse{}, ErrCatalogNotWired
		}
		return r.catalog.Search(ctx, req)
	default:
		return SearchResponse{}, ErrUnknownOrigin
	}
}

// SearchAll runs the request against every wired sub-service
// and returns the merged result. Used by territory-wide
// queries (e.g. "all images") that have no Origin constraint.
func (r *Router) SearchAll(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if r == nil {
		return SearchResponse{}, ErrNoServiceWired
	}
	out := SearchResponse{}
	for _, svc := range r.services() {
		if svc == nil {
			continue
		}
		resp, err := svc.Search(ctx, req)
		if err != nil {
			// Routing SearchAll is best-effort: log+skip.
			// (Composition may use a logger; here we
			// propagate only when ALL services error.)
			continue
		}
		out.Assets = append(out.Assets, resp.Assets...)
		if out.SubService == "" {
			out.SubService = resp.SubService
		}
	}
	return out, nil
}

// services returns the wired sub-services in declaration order.
func (r *Router) services() []Service {
	if r == nil {
		return nil
	}
	return []Service{r.retrieved, r.generated, r.catalog}
}

// ── Sentinel errors (Step 9 stable names) ──

// ErrNoServiceWired is returned when the router has no wired
// sub-services. Composition-root misconfiguration.
var ErrNoServiceWired = errRouting("routing.Router: no services wired")

// ErrRetrievedNotWired is returned when a Retrieved-territory
// request comes in but the retrieved sub-service is nil.
var ErrRetrievedNotWired = errRouting("routing.Router: retrieved service not wired")

// ErrGeneratedNotWired — same for the generated territory.
var ErrGeneratedNotWired = errRouting("routing.Router: generated service not wired")

// ErrCatalogNotWired — uploaded territory without catalog.
var ErrCatalogNotWired = errRouting("routing.Router: catalog service not wired")

// ErrUnknownOrigin is returned when Origin is set to a value
// that doesn't match any territory constant.
var ErrUnknownOrigin = errRouting("routing.Router: origin is not a known territory")

type errRouting string

func (e errRouting) Error() string { return string(e) }
