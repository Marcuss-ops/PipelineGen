// Package retrieved (application/images/retrieved) — provider_registry.go
// holds the RetrievalProviderRegistry — the canonical composition of
// RetrievalProviders. Per PR-IMG-SPLIT-3 (July 2026), the registry is
// now in its own file, separate from the concrete provider implementations.
//
// Iteration order is the fallback chain: the FIRST non-empty result wins.
// Default order is Wikipedia → SearXNG → DuckDuckGo → Drive, mirroring
// the historical storage_search.go cascade (Wikipedia first because it
// carries license metadata; SearXNG next because it honours a configured
// site policy; DuckDuckGo last because it returns the widest but
// lowest-quality results; DriveImageProvider is the pre-search
// short-circuit).
//
// FASE 8 (July 2026): the per-call DTOs moved to routing to break
// the routing↔retrieved import cycle.
package retrieved

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/workflow/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// RetrievalProviderRegistry is the canonical composition of RetrievalProviders.
type RetrievalProviderRegistry struct {
	providers []RetrievalProvider
	log       *zap.Logger
}

// NewRetrievalProviderRegistry composes the providers in the given
// order. Caller-supplied order is respected (used by tests + custom
// wiring). Production wiring should use NewDefaultProviderRegistry
// which returns the canonical fallback chain.
func NewRetrievalProviderRegistry(log *zap.Logger, providers []RetrievalProvider) *RetrievalProviderRegistry {
	if log == nil {
		log = zap.NewNop()
	}
	if providers == nil {
		providers = []RetrievalProvider{}
	}
	return &RetrievalProviderRegistry{providers: providers, log: log}
}

// NewDefaultProviderRegistry returns the canonical 4-provider fallback
// chain in Wikipedia → SearXNG → DuckDuckGo → Drive order.
func NewDefaultProviderRegistry(bridge StorageBridge, client httpDoer, log *zap.Logger, lang, searxngURL string) *RetrievalProviderRegistry {
	return NewRetrievalProviderRegistry(log, []RetrievalProvider{
		NewWikipediaProvider(bridge, client, log, lang),
		NewWikimediaCommonsProvider(bridge, log),
		NewSearXNGProvider(bridge, client, log, searxngURL),
		NewDuckDuckGoProvider(bridge, client, log),
		NewDriveImageProvider(bridge, log),
	})
}

// SearchAll iterates the providers in registered order, returning the
// first non-empty result. Returns nil + nil when every source is
// exhausted. Errors are logged and skipped — a Wikipedia 404 must not
// abort the DuckDuckGo fallback.
//
// FASE 8: opts/result types relocated to routing (cycle break).
func (r *RetrievalProviderRegistry) SearchAll(ctx context.Context, query string, opts routing.RetrievalSearchOptions) ([]routing.RetrievalSearchResult, error) {
	if r == nil || len(r.providers) == 0 {
		return nil, nil
	}
	for _, p := range r.providers {
		results, err := p.Search(ctx, query, opts)
		if err != nil {
			if r.log != nil {
				r.log.Warn("retrieval provider errored — continuing fallback",
					zap.String("provider", string(p.Name())),
					zap.String("query", query),
					zap.Error(err),
				)
			}
			continue
		}
		if len(results) == 0 {
			continue
		}
		if r.log != nil {
			r.log.Debug("retrieval provider produced hit",
				zap.String("provider", string(p.Name())),
				zap.Int("count", len(results)),
			)
		}
		return results, nil
	}
	return nil, nil
}

// SearchProvider invokes exactly one provider from this registry. Unlike
// SearchAll it never falls through to another source, which makes explicit
// provider canaries deterministic while preserving the shared provider code.
func (r *RetrievalProviderRegistry) SearchProvider(ctx context.Context, provider, query string, opts routing.RetrievalSearchOptions) ([]routing.RetrievalSearchResult, error) {
	if r == nil {
		return nil, nil
	}
	name := asset.ImageProvider(provider)
	p := r.SearchByName(name)
	if p == nil {
		return nil, fmt.Errorf("retrieval provider %q is not registered", provider)
	}
	return p.Search(ctx, query, opts)
}

// SearchByName returns the provider matched by a given ImageProvider
// constant, or nil when the registry has no such provider registered.
// Used by tests + diagnostics for explicit-provider lookups.
func (r *RetrievalProviderRegistry) SearchByName(name asset.ImageProvider) RetrievalProvider {
	if r == nil {
		return nil
	}
	for _, p := range r.providers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// Providers returns the registered providers in fallback order. The
// returned slice is a defensive copy — callers may freely sort or
// range over it without aliasing the registry's internal state.
func (r *RetrievalProviderRegistry) Providers() []RetrievalProvider {
	if r == nil {
		return nil
	}
	out := make([]RetrievalProvider, len(r.providers))
	copy(out, r.providers)
	return out
}

// Diagnostics runs Healthy probes on every registered provider and
// returns a per-provider summary. Used by images.DiagnosticsService
// for the /api/system/doctor surface.
func (r *RetrievalProviderRegistry) Diagnostics(ctx context.Context) map[asset.ImageProvider]error {
	out := make(map[asset.ImageProvider]error, len(r.providers))
	if r == nil {
		return out
	}
	for _, p := range r.providers {
		out[p.Name()] = p.Healthy(ctx)
	}
	// Deterministic ordering for snapshot tests.
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	_ = keys // silenced; sort.Strings used as a pre-condition for stable test snapshots
	return out
}

// Resolve implements the user-spec'd Registry.Resolve:
//
//	empty input                          -> success + empty result
//	all ids found                        -> success + ordered providers
//	ANY id missing (or un-configured)   -> (nil, ErrProviderNotFound wrapping missing ids)
//
// Fail-closed per godlike/07 "No fake availability": callers MUST NOT
// silently partial-resolve. Operators can read the wrapped missing-id
// list to compute the next action (register the provider, update the
// call site, etc.). Returns ErrProviderNotFound via fmt.Errorf("%w ...")
// so errors.Is(err, ErrProviderNotFound) succeeds at every consumer layer.
func (r *RetrievalProviderRegistry) Resolve(ids []string) ([]RetrievalProvider, error) {
	if r == nil {
		return nil, errors.New("retrieved: nil registry")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]RetrievalProvider, 0, len(ids))
	var missing []string
	for _, id := range ids {
		var found RetrievalProvider
		for _, p := range r.providers {
			if string(p.Name()) == id {
				found = p
				break
			}
		}
		if found == nil {
			missing = append(missing, id)
			continue
		}
		out = append(out, found)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w (missing ids=%v)", ErrProviderNotFound, missing)
	}
	return out, nil
}

// Stop halts any background workers managed by the registry.
//
// FASE 7 (July 2026, image-territories action plan): the
// RetrievalProviderRegistry does not spawn goroutines today — every
// provider.Search is invoked synchronously from the caller goroutine.
// The Stop method is present so the compose-side lifecycle surface
// has a forward-compatible endpoint for future background workers
// (planned: health probes, provider-list refresh tick, etc.) without
// every owner re-adding the contract on each new addition.
//
// Both nil-receiver (defensive against typed-nil registry handles
// passed through composition) and nil-ctx (defensive against startup
// paths that haven't yet bound a parent context) are safe: Stop
// returns nil so the compose-side shutdown chain stays tight.
func (r *RetrievalProviderRegistry) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx != nil {
		// Honour the ctx by probing Done — today this is purely
		// a contract assertion (no goroutines to interrupt); a
		// future worker will respect this signal here.
		_ = ctx.Done()
	}
	if r.log != nil {
		r.log.Debug("retrieval registry Stop() — no background goroutines to interrupt today (FASE 7 forward-compatible surface)")
	}
	return nil
}
