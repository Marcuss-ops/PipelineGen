package wiring

import (
	"database/sql"

	renderingwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/rendering"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"go.uber.org/zap"
)

func wireChrononMetricsAdapter(db *sql.DB, log *zap.Logger) *cliprender.ChrononMetricsAdapter {
	return renderingwiring.NewChrononMetricsAdapter(db, log)
}
