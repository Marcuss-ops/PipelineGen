package media

import (
	"fmt"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// NewPostgresMediaCommitter constructs the canonical PostgresMediaCommitter
// from a single media Postgres handle: outbox repository + registry ledger are
// derived from the same connection so the committer's 8-step transaction stays
// on one engine.
func NewPostgresMediaCommitter(deps CommitterDeps) (*pgmedia.PostgresMediaCommitter, error) {
	if deps.DB == nil {
		return nil, fmt.Errorf("media PostgreSQL committer: db handle is nil")
	}
	ledger, err := pgmedia.NewRegistry(deps.DB)
	if err != nil {
		return nil, fmt.Errorf("media PostgreSQL committer: registry ledger: %w", err)
	}
	return pgmedia.NewPostgresMediaCommitter(deps.DB, pgmedia.NewOutboxRepository(deps.DB), ledger, deps.Log), nil
}
