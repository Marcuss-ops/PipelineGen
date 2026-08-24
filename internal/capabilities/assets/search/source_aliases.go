// Package search — source_aliases.go is the PR-1 source alias
// resolution layer for BackendRegistry.Eligible.
//
// PR-1 spec: "Source Registry normalizza alias". The Query.Sources
// filter is currently declared but not applied (Eligible only
// filters by MediaTypes∩Backend.Capabilities). This file adds the
// alias resolution so callers may pass either canonical backend
// names ("youtube", "artlist", "image", "local", "semantic", "stock") OR
// well-known short aliases ("yt", "clips", "vector"), and the
// registry treats them identically. Unknown aliases resolve to an
// empty canonical form and contribute to filtering-out the
// backend, NOT to a silent fallback (no fake availability).
//
// Wave 19 invariant: this file is stdlib-only. No new imports are
// added to the search package — the alias table is a package-level
// map literal + a constructor for tests to extend (RegisterSourceAlias).
package assets

import (
	"strings"
	"sync"
)

// AliasRegistry is the canonical source-alias → canonical-name map.
// All keys and values are lowercase canonical names. The map is
// mutable at startup via RegisterSourceAlias; searches at fanout
// time use CanonicalizeSource which reads a snapshot for fast,
// lock-free resolution. Tests may add entries; production code does
// not register after composition root boot.
type AliasRegistry struct {
	mu    sync.RWMutex
	alias map[string]string
}

// NewAliasRegistry returns a registry seeded with the canonical
// aliases shipped in PR-1. The set is intentionally conservative —
// new aliases are adopted only when needed (avoid speculative name
// expansion that diverges between code paths). Both keys and
// canonical values are stored lowercase.
func NewAliasRegistry() *AliasRegistry {
	r := &AliasRegistry{
		alias: make(map[string]string, 16),
	}
	// YouTube — providers.SearchProvider returns "youtube" as canonical.
	for _, a := range []string{"yt", "youtube"} {
		r.alias[a] = "youtube"
	}
	// Artlist — providers.SearchProvider returns "artlist".
	r.alias["artlist"] = "artlist"
	// Stock — providers.SearchProvider returns "stock".
	r.alias["stock"] = "stock"
	// Images — the canonical retrieval facade is exposed as "image".
	for _, a := range []string{"image", "images", "internet_images"} {
		r.alias[a] = "image"
	}
	// Local — localSearchBackend returns "local".
	// Aliases accepted: "clips" (legacy clipssearch vocabulary),
	// "local" (canonical), "db" (power users referring to the
	// SQLite-backed catalog).
	for _, a := range []string{"clips", "local", "db", "catalog"} {
		r.alias[a] = "local"
	}
	// Semantic — semanticSearchBackend returns "semantic".
	// Aliases: "vector" (qdrant-vocabulary), "qdrant".
	for _, a := range []string{"semantic", "vector", "qdrant"} {
		r.alias[a] = "semantic"
	}
	return r
}

// CanonicalizeSource normalises one source string to its canonical
// (lowercase) backend name. Empty input returns ""; unknown alias
// also returns "" — callers should treat empty as "excludes all
// backends" rather than a fallback to all (no fake availability).
func (r *AliasRegistry) CanonicalizeSource(s string) string {
	if s == "" {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	lower := strings.ToLower(s)
	if lower == "" {
		return ""
	}
	return r.alias[lower]
}

// CanonicalizeSources applies CanonicalizeSource to every input,
// dropping empties and de-duplicating the resulting canonical
// names. Order is preserved by first-appearance so callers can
// grep logs for "which canonical did the user request".
func (r *AliasRegistry) CanonicalizeSources(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		canonical := r.CanonicalizeSource(raw)
		if canonical == "" {
			continue
		}
		if _, dup := seen[canonical]; dup {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out
}

// RegisterSourceAlias adds an alias to the registry. Intended for
// test-time extension only; production code must NOT mutate the
// registry after composition root boot (so AlreadyRegistered in
// PR-10+ won't be silently broken). Returns false if alias or
// canonical is empty or if the alias already maps to a different
// canonical name (idempotent re-registration to the same canonical
// returns true).
func (r *AliasRegistry) RegisterSourceAlias(alias, canonical string) bool {
	if alias == "" || canonical == "" {
		return false
	}
	aliasLower := strings.ToLower(alias)
	canonicalLower := strings.ToLower(canonical)
	if aliasLower == "" || canonicalLower == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.alias[aliasLower]; ok && existing != canonicalLower {
		return false
	}
	r.alias[aliasLower] = canonicalLower
	return true
}

// Snapshot returns a read-only copy of the alias table — useful for
// diagnostics and tests. Not optimised for hot paths; Equal is the
// faster path used at fanout time.
func (r *AliasRegistry) Snapshot() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.alias))
	for k, v := range r.alias {
		out[k] = v
	}
	return out
}

// defaultSourceAliases is the package-level registry used by
// BackendRegistry.Eligible. Tests can swap it via
// SetDefaultAliasRegistry to inject per-test customisations; this
// keeps the public BackendRegistry.Eligible signature unchanged
// while still allowing test-driven extension.
var (
	defaultAliasMu       sync.RWMutex
	defaultSourceAliases = NewAliasRegistry()
)

// SetDefaultAliasRegistry replaces the package-level alias registry
// used by BackendRegistry.Eligible for caller-supplied source
// names. Intended for tests; production code does not call it.
func SetDefaultAliasRegistry(r *AliasRegistry) {
	defaultAliasMu.Lock()
	defer defaultAliasMu.Unlock()
	if r == nil {
		defaultSourceAliases = NewAliasRegistry()
		return
	}
	defaultSourceAliases = r
}

// ResolveCanonical applies the default registry's
// CanonicalizeSource to a single string. Returns "" if s is empty
// or unknown. The function lives at package level for use inside
// BackendRegistry.Eligible so the registry itself stays
// self-contained.
func ResolveCanonical(s string) string {
	defaultAliasMu.RLock()
	r := defaultSourceAliases
	defaultAliasMu.RUnlock()
	return r.CanonicalizeSource(s)
}

// ResolveCanonicals applies the default registry's
// CanonicalizeSources to the full input slice. Returns nil for
// empty input.
func ResolveCanonicals(in []string) []string {
	defaultAliasMu.RLock()
	r := defaultSourceAliases
	defaultAliasMu.RUnlock()
	return r.CanonicalizeSources(in)
}
