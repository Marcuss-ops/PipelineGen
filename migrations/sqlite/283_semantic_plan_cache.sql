-- database: cache
-- 283_semantic_plan_cache.sql
--
-- Durable L2 cache for the semantic production planner. The planner keys an
-- answer on (normalized entity, query class, language), not on the raw query
-- text, so a warm run reuses the same provider answer across ladder rungs.
--
-- Rebuildable by construction: the cache plane owns no business state, and a
-- missing row is a provider call, never a failed run.
--
-- provider / provider_lane are the invalidation index: when one retrieval
-- provider changes its index or routing contract, only that provider's answers
-- are dropped (InvalidateProvider), not the whole warm set.
--
-- IDEMPOTENCY: idempotent (CREATE ... IF NOT EXISTS only).

CREATE TABLE IF NOT EXISTS semantic_plan_cache (
    cache_key TEXT PRIMARY KEY,
    payload_json TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    provider_lane TEXT NOT NULL DEFAULT '',
    stored_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_semantic_plan_cache_expires
    ON semantic_plan_cache(expires_at);

CREATE INDEX IF NOT EXISTS idx_semantic_plan_cache_provider
    ON semantic_plan_cache(provider);

CREATE INDEX IF NOT EXISTS idx_semantic_plan_cache_provider_lane
    ON semantic_plan_cache(provider_lane);
