package wiring

import (
	"database/sql"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
	"go.uber.org/zap"
)

func wireChrononMetricsAdapter(db *sql.DB, log *zap.Logger) *cliprender.ChrononMetricsAdapter {
	if db == nil {
		return nil
	}
	store, err := perfstore.NewOperationStore(db)
	if err != nil {
		return nil
	}
	return cliprender.NewChrononMetricsAdapter(store, log)
}
