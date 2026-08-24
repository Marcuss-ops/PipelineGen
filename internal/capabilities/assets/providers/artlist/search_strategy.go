package assets

import "strings"

// ArtlistSearchStrategy is the canonical operator-selected strategy for
// the Artlist live-search fallback chain.
//
// godlike/06 SSOT: this file owns both the strategy enum and the resolver
// that converts the enum into an ordered []Searcher. Composition code may
// cast config strings into this type, but it must not duplicate strategy
// validation or fallback-chain ordering.
type ArtlistSearchStrategy string

const (
	// StrategyArtlistOnly uses the authenticated Artlist scraper only.
	// This is the safest production default: no public-stock fallback unless
	// the operator explicitly opts in.
	StrategyArtlistOnly ArtlistSearchStrategy = "artlist_only"

	// StrategyArtlistThenPublicFallback preserves the legacy fallback chain:
	// Artlist scraper first, then public providers.
	StrategyArtlistThenPublicFallback ArtlistSearchStrategy = "artlist_then_public_fallback"

	// StrategyPublicOnlyForDev is a dev/test convenience mode that skips the
	// Artlist scraper and uses only public providers.
	StrategyPublicOnlyForDev ArtlistSearchStrategy = "public_only_for_dev"

	// DefaultArtlistSearchStrategy is fail-closed: Artlist only, no public
	// fallback without explicit configuration.
	DefaultArtlistSearchStrategy = StrategyArtlistOnly
)

// Normalize trims and lowercases the strategy string. Empty input resolves to
// DefaultArtlistSearchStrategy so zero-value config remains deterministic.
func (s ArtlistSearchStrategy) Normalize() ArtlistSearchStrategy {
	switch strings.ToLower(strings.TrimSpace(string(s))) {
	case "":
		return DefaultArtlistSearchStrategy
	case string(StrategyArtlistOnly):
		return StrategyArtlistOnly
	case string(StrategyArtlistThenPublicFallback):
		return StrategyArtlistThenPublicFallback
	case string(StrategyPublicOnlyForDev):
		return StrategyPublicOnlyForDev
	default:
		return ArtlistSearchStrategy(strings.ToLower(strings.TrimSpace(string(s))))
	}
}

// IsValid reports whether the strategy is one of the canonical enum values.
// Empty string is intentionally not valid here; callers that want the default
// should call Normalize first or use ResolveSearcherChain, which normalizes.
func (s ArtlistSearchStrategy) IsValid() bool {
	switch strings.ToLower(strings.TrimSpace(string(s))) {
	case string(StrategyArtlistOnly), string(StrategyArtlistThenPublicFallback), string(StrategyPublicOnlyForDev):
		return true
	default:
		return false
	}
}

// ResolveSearcherChain translates the operator-chosen ArtlistSearchStrategy
// into an ordered []Searcher for the live-search fallback chain.
//
// The resolver is the SINGLE canonical owner of the per-strategy searcher
// ordering (godlike/06 SSOT). Callers (buildSearcherChain in search_core.go)
// pass the wired searchers + the strategy; the resolver decides which ones
// to include and in what order.
//
// godlike/07 no-fake-availability: every strategy is explicit and visible.
// The zero-value (empty string) defaults to DefaultArtlistSearchStrategy
// (artlist_only) — the safest default. Operators who want the legacy
// Pixabay/Pexels fallback must set ARTLIST_SEARCH_STRATEGY explicitly.
//
// Per-strategy ordering:
//
//	artlist_only                 → [scraper]                 (no external stock)
//	artlist_then_public_fallback → [scraper, pixabay, pexels] (legacy chain)
//	public_only_for_dev          → [pixabay, pexels]          (no scraper needed)
func ResolveSearcherChain(strategy ArtlistSearchStrategy, scraper, pixabay, pexels Searcher) []Searcher {
	strategy = strategy.Normalize()
	if !strategy.IsValid() {
		strategy = DefaultArtlistSearchStrategy
	}

	var searchers []Searcher

	// DB searcher is always included by the caller in buildSearcherChain.
	// The strategy only controls the infra searchers (scraper/pixabay/pexels).

	switch strategy {
	case StrategyArtlistOnly:
		if scraper != nil {
			searchers = append(searchers, scraper)
		}

	case StrategyArtlistThenPublicFallback:
		if scraper != nil {
			searchers = append(searchers, scraper)
		}
		if pixabay != nil {
			searchers = append(searchers, pixabay)
		}
		if pexels != nil {
			searchers = append(searchers, pexels)
		}

	case StrategyPublicOnlyForDev:
		if pixabay != nil {
			searchers = append(searchers, pixabay)
		}
		if pexels != nil {
			searchers = append(searchers, pexels)
		}
	}

	return searchers
}
