// Package retrieved — spec_aliases.go declares the user-spec surface
// (FASE 5, July 2026) on top of Step 8's canonical implementation.
//
// Canonical (Step 8)            User-spec (this file)     Backed by
// ──────────────────────────     ─────────────────────────  ─────────
// RetrievalProvider              Provider                  type alias
// RetrievalProviderRegistry      Registry (interface)      struct implements via Resolve method in provider_registry.go
// RetrievalProviderRegistry      RetrievalRegistryImpl    type alias (literal *RetrievalRegistryImpl)
// routing.RetrievalSearchOptions RetrievalSearchRequest    type alias
// routing.RetrievalSearchResult  RetrievedCandidate        type alias
// — (new)                        ErrProviderNotFound       sentinel errors.New
//
// FASE 8 (July 2026): the DTO types moved to the routing package
// (the canonical home of the port side). The user-spec aliases now
// forward to routing.* so the FASE 5 surface shape is preserved.
//
// All type aliases preserve identity under reflect.TypeOf, so compile-time
// assertions using either name land on the SAME concrete type. Existing
// callers that reference the canonical names keep working unchanged;
// new callers (FASE 6 ImageSearchResolver) read via the spec shape.
package retrieved

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
)

// Provider aliases the canonical RetrievalProvider interface.
type Provider = RetrievalProvider

// Registry is the user-spec'd interface declaring the single Resolve
// method. The existing *RetrievalProviderRegistry struct satisfies it
// via the Resolve method appended in provider_registry.go.
type Registry interface {
	Resolve(ids []string) ([]Provider, error)
}

// RetrievalRegistryImpl aliases RetrievalProviderRegistry. The user
// spec names the concrete implementation `RetrievalRegistryImpl`; the
// alias keeps the existing canonical struct name while letting the
// literal `var _ Registry = (*RetrievalRegistryImpl)(nil)` assertion
// read naturally.
type RetrievalRegistryImpl = RetrievalProviderRegistry

// RetrievalSearchRequest aliases routing.RetrievalSearchOptions
// (FASE 8: relocated to routing to break the import cycle).
type RetrievalSearchRequest = routing.RetrievalSearchOptions

// RetrievedCandidate aliases routing.RetrievalSearchResult
// (FASE 8: relocated to routing to break the import cycle).
type RetrievedCandidate = routing.RetrievalSearchResult

// RetrievalSearchOptions aliases routing.RetrievalSearchOptions
// (FASE 8: relocated to routing to break the import cycle). The
// bare-name alias preserves the pre-cycle-break test surface
// (provider_registry_test.go references the bare type) while
// keeping a single canonical home in the routing package.
type RetrievalSearchOptions = routing.RetrievalSearchOptions

// RetrievalSearchResult aliases routing.RetrievalSearchResult
// (FASE 8: relocated to routing to break the import cycle). The
// bare-name alias preserves the pre-cycle-break test surface
// (provider_registry_test.go references the bare type) while
// keeping a single canonical home in the routing package.
type RetrievalSearchResult = routing.RetrievalSearchResult

// ErrProviderNotFound is the user-spec sentinel returned by the
// Registry.Resolve method when at least one requested id is missing.
var ErrProviderNotFound = errors.New("retrieved: provider id not found in registry")

// Compile-time assertions (user-spec literal shape).
var (
	_ Provider = (*WikipediaProvider)(nil)
	_ Provider = (*WikimediaCommonsProvider)(nil)
	_ Provider = (*SearXNGProvider)(nil)
	_ Provider = (*DuckDuckGoProvider)(nil)
	_ Provider = (*DriveImageProvider)(nil)

	_ Registry = (*RetrievalProviderRegistry)(nil)
	_ Registry = (*RetrievalRegistryImpl)(nil)
)
