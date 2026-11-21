// render_cache.go owns the deterministic render cache for clip.render:
// fingerprint → certified artifact locator. The cache makes the
// cost-marginal-zero property concrete: a repeated POST with identical
// semantics returns in milliseconds (no download, no Chronon, no probe)
// by reusing the locator-first artifact the previous render already
// certified, staged and committed.
//
// Storage is PostgreSQL media path (a new table, not the legacy SQLite
// sidecar). No extra janitor beyond DB vacuum.
//
// STALENESS IS CHECKED, NOT ASSUMED. The comment above used to claim the
// cache "naturally misses" when the media row is deleted. It did not:
// clip_render_cache carries no foreign key to media_assets and Get() returned
// the row without reading the asset at all, so a deleted or overwritten asset
// still produced a HIT — and the batch handler trusts the record immediately
// (fingerprint -> asset_id -> status=CACHED), so the product would report a
// certified artifact that no longer exists. Get() therefore JOINs the media
// row and requires BOTH identity and content to agree:
//
//	cache row + asset exists + same content sha256 + ACTIVE/not-deleted -> HIT
//	cache row + asset missing                                          -> MISS
//	cache row + asset sha256 differs (overwritten)                     -> MISS
//	cache row + asset retired (DELETED / deleted_at set)               -> MISS
//
// A MISS is the fail-safe direction: the worker renders a fresh artifact
// instead of advertising a stale locator.
//
// Lookup key: fingerprint hex (64 chars, lower hex). Value is the
// certified locator and the committed asset id, so the worker can
// fabricate a valid completion without touching the GPU.
//
// SEGMENT IDENTITY — not modelled, because nothing produces segments on this
// lane. A segmented render (N jobs rendering disjoint frame ranges of one
// sealed plan) would need a per-SEGMENT entry keyed by (fingerprint,
// frame_range), since one plan legitimately produces many artifacts. The plan
// carries no range by contract (bench_seek_test.go pins that the source block
// exposes exactly asset_id/path/sha256 and no hidden offset), and the chunk
// producer/port pair was removed as dead code (ticket CR-19-16). A range key
// must be added by the change that introduces the producer, not before: an
// unused key silently never matches. The boot DDL therefore creates only
// clip_render_cache.

package cliprender

import (
	"errors"

	"go.uber.org/zap"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// RenderCacheRecord is the durable cache value addressed by fingerprint.
// The record and the port live in the kernel so the PostgreSQL adapter can
// implement them without importing this capability (RA-11); the aliases keep
// the cliprender API unchanged for every caller.
type RenderCacheRecord = kernelmedia.RenderCacheRecord

// RenderCache is the port for the deterministic render cache.
type RenderCache = kernelmedia.RenderCache

// ErrCacheMiss is returned when no cache entry exists for a fingerprint.
var ErrCacheMiss = kernelmedia.ErrCacheMiss

// logCacheLookupFailure records a cache read that failed for a reason other
// than a miss. ErrCacheMiss is the normal path and stays silent; anything else
// (a broken media DB, a missing table) would otherwise degrade to "always
// miss" invisibly on every handler and worker lookup.
func logCacheLookupFailure(log *zap.Logger, scope, fingerprint string, err error) {
	if log == nil || err == nil || errors.Is(err, ErrCacheMiss) {
		return
	}
	log.Debug("clip.render cache lookup failed",
		zap.String("scope", scope),
		zap.String("fingerprint", digestPrefix(fingerprint)),
		zap.Error(err))
}
