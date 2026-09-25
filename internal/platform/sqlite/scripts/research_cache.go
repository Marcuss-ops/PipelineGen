// Package scripts — research_cache.go: the ScriptRepository's research_cache
// surface.
//
// SINGLE OWNER: the research_cache table has exactly ONE SQL implementation,
// internal/platform/sqlite/topicsourcecache.Repository. This file used to
// carry a second, independently maintained copy of the same SELECT / INSERT /
// ON CONFLICT / UPDATE statements, so a schema change required editing two
// files and the two copies could drift. Every method below delegates; do not
// reintroduce SQL here.
package scripts

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/topicsourcecache"
)

// researchCacheRepository binds the canonical research_cache owner to this
// repository's connection.
func (r *ScriptRepository) researchCacheRepository() *topicsourcecache.Repository {
	return topicsourcecache.NewRepository(r.db)
}

// GetResearchCache returns a non-expired source_text for the given key.
// On hit it atomically increments hit_count and refreshes last_used.
// On miss or expiry it returns ("", nil).
func (r *ScriptRepository) GetResearchCache(ctx context.Context, key string) (string, error) {
	return r.researchCacheRepository().GetResearchCache(ctx, key)
}

// GetResearchCacheRecord reads provenance for a validated cache hit without
// incrementing hit_count; GetResearchCache owns hit accounting.
func (r *ScriptRepository) GetResearchCacheRecord(ctx context.Context, key string) (scriptpkg.ResearchCacheRecord, error) {
	return r.researchCacheRepository().GetResearchCacheRecord(ctx, key)
}

// SaveResearchCache inserts or replaces a research_cache row from the
// canonical ResearchCacheRecord. The caller must compute rec.Key with
// scriptpkg.ComputeResearchCacheKey.
func (r *ScriptRepository) SaveResearchCache(ctx context.Context, rec scriptpkg.ResearchCacheRecord) error {
	return r.researchCacheRepository().SaveResearchCache(ctx, rec)
}

// TouchResearchCache refreshes last_used for a key and returns the number
// of rows affected.
func (r *ScriptRepository) TouchResearchCache(ctx context.Context, key string) (int64, error) {
	return r.researchCacheRepository().TouchResearchCache(ctx, key)
}

// SweepExpiredResearchCache deletes rows whose expires_at is in the past.
func (r *ScriptRepository) SweepExpiredResearchCache(ctx context.Context) (int64, error) {
	return r.researchCacheRepository().SweepExpiredResearchCache(ctx)
}

// SweepStaleResearchCache deletes rows whose last_used is older than
// maxAgeDays. This is the legacy sweeper; prefer SweepExpiredResearchCache.
func (r *ScriptRepository) SweepStaleResearchCache(ctx context.Context, maxAgeDays int) (int64, error) {
	return r.researchCacheRepository().SweepStaleResearchCache(ctx, maxAgeDays)
}
