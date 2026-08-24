// Package artlist — cache_ports.go: typed-port surface for the
// persistent Artlist search cache (PR-P0-3, July 2026).
//
// P0-3 (godlike/07 zero-legacy): the persistent cache no longer
// inflates *sql.DB into the application layer; consumers pass a
// typed port that the concrete SQLite adapter at
// internal/platform/sqlite/artlist_search_cache_adapter.go
// satisfies. Pattern A from the verdict (Tier-A migration) lands
// here; the deadline-15-luglio-2026 allowlist entry for
// internal/capabilities/assets/providers/artlist/search_cache.go
// is removed in the same PR.
//
// godlike/06 SSOT (one owner per fact): ArtlistSearchCachePort is
// the SOLE canonical owner of the persistent search-cache
// contract. The legacy `database/sql` direct import in the
// application layer was a godlike/06 SSOT violation caught by
// `scripts/ci-architectural-checks.sh Check 42`; this file is
// the post-migration SSOT marker so a future agent looking at
// git blame finds the port surface here.
package artlist

import (
	"context"
	"errors"
	"time"
)

// CachedEntry is a single persistent-cache row, typed for the
// application layer so the SQLite-adapter-private column shape
// (term, clips_json, cached_at) does NOT leak into the cache
// struct — the legacy direct-import path forced the application
// layer to know the schema. With the port, the schema is owned
// by the adapter alone (canonical owner per godlike/06 SSOT).
type CachedEntry struct {
	Term     string
	Clips    []Candidate
	CachedAt time.Time
}

// ArtlistSearchCachePort is the narrow typed port for the
// persistent Artlist live-search cache (Level 2 in the two-level
// L1+L2 cache design in search_cache.go).
//
// Implementations MUST be safe for concurrent use; the
// application caller serialises the higher-level cache atomic
// mutations through the in-memory L1 map and treats the port as
// a bulk-load + per-term-set helper.
//
// godlike/06 SSOT (one owner per fact): ArtlistSearchCachePort
// is the SOLE canonical writer of artlist_search_cache rows;
// no other code path may insert or update that table.
//
// godlike/07 NO-FAKE-AVAILABILITY: warm returns its error
// honestly (caller logs + proceeds with empty in-memory map).
// Set returns its error honestly. Get distinguishes "no row"
// (false, nil, no error) from "transport failure" (false, err)
// so the caller does not silently consume a corrupt row.
type ArtlistSearchCachePort interface {
	// Warm bulk-loads fresh (<48h) entries. Called once at cache
	// construction time inside a background goroutine, so MUST
	// NOT block the caller on long-running IO; a transport
	// failure returns a non-nil error which the caller logs +
	// proceeds with an empty in-memory map. The bound is
	// enforced by the caller's 15s background timeout, NOT by
	// the port itself.
	Warm(ctx context.Context) ([]CachedEntry, error)

	// Get reads a single entry by term. Returns
	// (nil, {}, false, nil) when no row exists or the entry
	// is expired; callers branch on the boolean rather than
	// the error. The port MUST delete the expired row
	// internally so subsequent Warm calls don't repeat the read
	// (the delete is best-effort; an internal delete failure
	// is silently logged + the read still returns false).
	Get(ctx context.Context, term string) ([]Candidate, time.Time, bool, error)

	// Set upserts an entry keyed by term. Implementations MUST
	// use ON CONFLICT(term) DO UPDATE so concurrent writers
	// collapse into a single row. Errors are surfaced to the
	// caller for log accounting; silent-degrade is forbidden.
	Set(ctx context.Context, term string, clips []Candidate) error

	// Delete removes a single entry by term. Errors are logged
	// by the caller; tombstoning is best-effort.
	Delete(ctx context.Context, term string) error

	// CleanupExpired removes entries older than the supplied
	// TTL (caller-supplied; the port applies its 48h hard-limit
	// because a stale cached_at at any age >= 48h is unsafe to
	// keep — see search_cache.go for the rationale).
	CleanupExpired(ctx context.Context, ttl time.Duration) error
}

// ErrCacheUnavailable is the typed sentinel composition-time
// fail-closed guard returned by wrappers that need a SQLite
// pool but were constructed with nil. Mirrors
// ErrPublisherUnavailable / ErrRunRepositoryUnavailable
// fail-closed discipline (godlike/07 no-fake-availability). It
// is intentionally NOT raised by the port surface itself; the
// port is whatever the caller wires (storm failure cases are
// transport errors propagating verbatim).
var ErrCacheUnavailable = errors.New(
	"artlist: ArtlistSearchCachePort unavailable at composition — production must wire SQLite adapter (godlike/07 no-fake-availability: persistent search-cache is observable truth for non-zero rates)",
)
