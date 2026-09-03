package media

import (
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// NewPostgresMediaCommitterFromDB constructs the canonical
// PostgresMediaCommitter from a single media Postgres handle: outbox
// repository + registry ledger are derived from the same connection so
// the committer's 8-step transaction stays on one engine.
func NewPostgresMediaCommitterFromDB(db *sql.DB, log *zap.Logger) (*pgmedia.PostgresMediaCommitter, error) {
	if db == nil {
		return nil, fmt.Errorf("media PostgreSQL committer: db handle is nil")
	}
	ledger, err := pgmedia.NewRegistry(db)
	if err != nil {
		return nil, fmt.Errorf("media PostgreSQL committer: registry ledger: %w", err)
	}
	return pgmedia.NewPostgresMediaCommitter(db, pgmedia.NewOutboxRepository(db), ledger, log), nil
}
