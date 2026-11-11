package script

import (
	"database/sql"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
	"go.uber.org/zap"
)

// wireSemanticSearchPlanner binds the semantic production plan's exact-source
// ladder to the same internet image provider the rest of the pipeline uses
// (class routing: person → DuckDuckGo, organization/location/object → Commons
// first, then the web cascade) and measures hit rate/latency.
//
// The plan cache is durable when the cache plane is available: a restarted
// worker reuses the warm provider answers instead of re-querying every entity,
// and the TTL is owned by configuration rather than a package constant.
func WireSemanticSearchPlanner(runner *scriptgen.Runner, cfg *config.Config, cacheDB *sql.DB, searcher documentadapters.InternetImageSearcher, log *zap.Logger) {
	if runner == nil || cfg == nil || !cfg.Scripts.SemanticProductionSearch {
		return
	}
	if searcher == nil {
		log.Warn("semantic production search enabled but no internet image searcher is wired")
		return
	}
	batchConfig := scriptgen.DefaultSemanticSearchBatchConfig()
	if ttl := time.Duration(cfg.Scripts.SemanticPlanCacheTTLSeconds) * time.Second; ttl > 0 {
		batchConfig.CacheTTL = ttl
	}
	if ttl := time.Duration(cfg.Scripts.SemanticStableEntityCacheTTLSeconds) * time.Second; ttl > 0 {
		batchConfig.StableEntityCacheTTL = ttl
	}
	cacheLabel := "memory"
	if cacheDB != nil {
		batchConfig.Cache = sqlitescripts.NewSQLiteSemanticPlanCacheAdapter(cacheDB, batchConfig.CacheTTL, log)
		cacheLabel = "sqlite"
	}
	planner, err := scriptgen.NewSemanticSearchBatchPlanner(documentadapters.NewSemanticAssetSearch(searcher), batchConfig)
	if err != nil {
		log.Warn("semantic production search batch unavailable", zap.Error(err))
		return
	}
	runner.SetSemanticSearchBatchPlanner(planner)
	log.Info("semantic production search batch wired (real provider, measured)",
		zap.String("plan_cache", cacheLabel),
		zap.Duration("plan_cache_ttl", batchConfig.CacheTTL),
		zap.Duration("stable_entity_cache_ttl", batchConfig.StableEntityCacheTTL),
		zap.Int("max_concurrency", batchConfig.MaxConcurrency))
}
