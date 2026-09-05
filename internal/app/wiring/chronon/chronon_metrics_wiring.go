package chronon

import (
	"database/sql"

	renderingwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/rendering"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"go.uber.org/zap"
)

// WireChrononMetricsAdapter builds the chronon.* phase metrics adapter
// over the performance-operations SQLite plane.
func WireChrononMetricsAdapter(db *sql.DB, log *zap.Logger) *cliprender.ChrononMetricsAdapter {
	return renderingwiring.NewChrononMetricsAdapter(db, log)
}
