// Package retrieved — spec_aliases.go declares the user-spec surface
// (FASE 5, July 2026) on top of Step 8's canonical implementation.
//
// Canonical (Step 8)            User-spec (this file)     Backed by
// ──────────────────────────     ─────────────────────────  ─────────
// RetrievalProvider              Provider                  type alias
// RetrievalProviderRegistry      Registry (interface)      struct implements via Resolve method in provider_registry.go
// RetrievalProviderRegistry      RetrievalRegistryImpl    type alias (literal *RetrievalRegistryImpl)
// RetrievalSearchOptions         RetrievalSearchRequest    type alias
// RetrievalSearchResult          RetrievedCandidate        type alias
// — (new)                        ErrProviderNotFound       sentinel errors.New
//
// All type aliases preserve identity under reflect.TypeOf, so compile-time
// assertions using either name land on the SAME concrete type. Existing
// callers that reference the canonical names keep working unchanged;
// new callers (FASE 6 ImageSearchResolver) read via the spec shape.
package retrieved

import "errors"

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

// RetrievalSearchRequest aliases RetrievalSearchOptions.
type RetrievalSearchRequest = RetrievalSearchOptions

// RetrievedCandidate aliases RetrievalSearchResult.
type RetrievedCandidate = RetrievalSearchResult

// ErrProviderNotFound is the user-spec sentinel returned by the
// Registry.Resolve method when at least one requested id is missing.
var ErrProviderNotFound = errors.New("retrieved: provider id not found in registry")

// Compile-time assertions (user-spec literal shape).
var (
	_ Provider = (*WikipediaProvider)(nil)
	_ Provider = (*SearXNGProvider)(nil)
	_ Provider = (*DuckDuckGoProvider)(nil)
	_ Provider = (*DriveImageProvider)(nil)

	_ Registry = (*RetrievalProviderRegistry)(nil)
	_ Registry = (*RetrievalRegistryImpl)(nil)
)
