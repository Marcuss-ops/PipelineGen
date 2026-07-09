package artlist

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
//	artlist_only               → [DB, scraper]              (no external stock)
//	artlist_then_public_fallback → [DB, scraper, pixabay, pexels] (legacy chain)
//	public_only_for_dev         → [DB, pixabay, pexels]     (no scraper needed)
func ResolveSearcherChain(strategy ArtlistSearchStrategy, scraper, pixabay, pexels Searcher) []Searcher {
	if !strategy.IsValid() {
		strategy = DefaultArtlistSearchStrategy
	}

	var searchers []Searcher

	// DB searcher is always included (caller prepends it in buildSearcherChain).
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
