package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	sqliteops "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

type sqliteTxManager struct {
	db *sql.DB
}

func (m *sqliteTxManager) BeginTx(ctx context.Context) (*sql.Tx, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("script submission: tx manager not wired")
	}
	return m.db.BeginTx(ctx, nil)
}

func buildScriptSubmissionService(root *wiring.ComposeRoot, log *zap.Logger) (*opsapp.Service, error) {
	if root == nil || root.DB == nil || root.Jobs == nil || root.Jobs.Repo == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil {
		return nil, fmt.Errorf("script submission: required runtime dependencies are nil")
	}
	opsRepo := sqliteops.NewSQLiteRepository(root.DB.DB)
	txMgr := &sqliteTxManager{db: root.DB.DB}
	// FASE 2 close-out: jobsStore satisfies JobGetter natively
	// (its Get(ctx, id) method matches the port shape). Wired
	// twice — once as JobEnqueuer (CreateInTx use) and once as
	// JobGetter (canonical-state-on-replay read on the HTTP 202
	// idempotency-hit path).
	return opsapp.NewService(opsRepo, root.Jobs.Repo, root.Jobs.Repo, root.Outbox.EventsRepo, txMgr, log), nil
}

// Compile-time assertion: *sqlitejobs.SQLiteStore implements
// BOTH the submission service's JobEnqueuer port AND the
// JobGetter port. Drift in either surface is a build failure,
// not a runtime panic (godlike/06 Pattern 0).
var (
	_ opsapp.TxManager     = (*sqliteTxManager)(nil)
	_ opsapp.JobEnqueuer   = (*sqlitejobs.SQLiteStore)(nil)
	_ opsapp.JobGetter     = (*sqlitejobs.SQLiteStore)(nil)
	_ opsapp.OutboxEmitter = (*outboxevents.Repository)(nil)
)
