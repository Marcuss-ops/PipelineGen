package app

import (
	"database/sql"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	jobsfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"go.uber.org/zap"
)

func wireStockFinalizer(stockDB *sql.DB, root *ComposeRoot, log *zap.Logger) finalization.JobFinalizer {
	if stockDB != nil && root.Outbox != nil && root.Outbox.EventsRepo != nil {
		assetTx := assetfinalizer.NewAssetTxFinalizer(log)
		return jobsfinalizer.New(stockDB, root.Outbox.EventsRepo, assetTx, log)
	}

	log.Warn("WireStockPipeline: Finalizer not constructed (godlike/07: one or more required deps nil — stockDB, root.Outbox, or root.Outbox.EventsRepo). If Publisher is also non-nil, the symmetric gate will fire ErrStockProductionJobFinalizerMissing.",
		zap.Bool("stockDB_nil", stockDB == nil),
		zap.Bool("root_Outbox_nil", root.Outbox == nil),
		zap.Bool("EventsRepo_nil", root.Outbox == nil || root.Outbox.EventsRepo == nil),
	)
	return nil
}
