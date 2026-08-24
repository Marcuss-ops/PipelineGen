package defaults

// MediaConfig is the canonical SSOT for media-search defaults.
//
// Pre-fix scattered literals this SSOT replaces (June 2026, Step 4
// PR3 — DRIFT-DEFAULTS-MEDIA):
//
//   - search.DefaultLimit = 20, MaxLimit = 100
//     (internal/capabilities/assets/search/types.go).
//   - mediasearch.DefaultLimit = 10, MaxLimit = 50
//     (internal/application/mediasearch/types.go).
//   - mediasearch.DefaultScore = 0.50
//     (same file; floor below which a hit is dropped pre-hydration,
//     per QDRANT-004).
//
// Every consumer MUST read from DefaultMediaConfig() rather than
// re-implementing these literals inline. A future "loosen the curate
// cap from 50 to 200" or "tighten the score floor to 0.6" change is
// then a one-line edit; pre-fix it required grep + reasoning about
// which call sites must agree.
//
// Shape is intentionally tiny (5 leaf fields) to keep pkg/defaults
// leaf-only: zero imports from internal/, only consumed by callers
// crossing the infra→application seam.
type MediaConfig struct {
	// SearchDefaultLimit is the per-page default for the canonical
	// search aggregator. Legacy inlined value: 20.
	SearchDefaultLimit int

	// SearchMaxLimit is the hard cap applied to search aggregator
	// requests. Legacy inlined value: 100.
	SearchMaxLimit int

	// CurateDefaultLimit is the per-page default for the legacy
	// mediasearch endpoint. Legacy inlined value: 10. Kept separate
	// from SearchDefaultLimit because the curate endpoint is a
	// tenant-scoped private API (QDRANT-004 spec) with tighter
	// limits than the public search aggregator.
	CurateDefaultLimit int

	// CurateMaxLimit is the hard cap applied to curate requests.
	// Legacy inlined value: 50.
	CurateMaxLimit int

	// DefaultScore is the floor below which a hit is dropped
	// pre-hydration. Legacy inlined value: 0.50. Strictly positive
	// per the SSOT rule.
	DefaultScore float64
}

// DefaultMediaConfig returns the canonical DRIFT-DEFAULTS-MEDIA SSOT.
// Treat the returned value as immutable per consumer site (no
// process-global mutation — copy and adjust locally if needed).
func DefaultMediaConfig() MediaConfig {
	return MediaConfig{
		SearchDefaultLimit: 20,
		SearchMaxLimit:     100,
		CurateDefaultLimit: 10,
		CurateMaxLimit:     50,
		DefaultScore:       0.50,
	}
}
