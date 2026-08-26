package wiring

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	sqlitemediaregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
)

// newCanonicalAssetCommitter is the composition-root factory for every
// production asset writer. The returned compatibility port is implemented by
// SQLiteMediaCommitter, so legacy callers retain their existing signatures
// while all writes converge on the media registry transaction gate.
func newCanonicalAssetCommitter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *imagesregistry.SQLiteMediaCommitter {
	ledger, err := sqlitemediaregistry.NewLedger(db)
	if err != nil {
		panic("canonical media committer: registry ledger: " + err.Error())
	}
	return imagesregistry.NewSQLiteMediaCommitter(db, box, ledger, log)
}
