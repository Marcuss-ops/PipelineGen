package rendering

import (
	"database/sql"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
	"go.uber.org/zap"
)

// NewChrononMetricsAdapter binds the Chronon metrics capability to the
// canonical SQLite performance store. A nil database means the projection is
// unavailable and returns a nil adapter without inventing availability.
func NewChrononMetricsAdapter(db *sql.DB, log *zap.Logger) *cliprender.ChrononMetricsAdapter {
	if db == nil {
		return nil
	}
	store, err := perfstore.NewOperationStore(db)
	if err != nil {
		return nil
	}
	return cliprender.NewChrononMetricsAdapter(store, log)
}
