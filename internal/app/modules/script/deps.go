package script

// deps.go owns the typed input contracts of the script module.
//
// WireSemanticSearchPlanner took six positional arguments: a runner, a config,
// an optional cache handle, an image searcher, a candidate scorer and a logger.
// The searcher and the scorer are both documentadapters ports reached through
// the same layer, and the cache handle is the only optional one — three facts
// a reader had to recover from the implementation (BON-03 step B).

import (
	"database/sql"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// SemanticSearchPlannerDeps is the input contract of
// WireSemanticSearchPlanner. CacheDB is optional: nil keeps the in-memory plan
// cache, a non-nil handle makes it durable across restarts.
type SemanticSearchPlannerDeps struct {
	Runner   *scriptgen.Runner
	Config   *config.Config
	CacheDB  *sql.DB
	Searcher documentadapters.InternetImageSearcher
	Scorer   documentadapters.SemanticCandidateScorer
	Log      *zap.Logger
}
