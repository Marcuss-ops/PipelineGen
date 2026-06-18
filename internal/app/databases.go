package app

import (
	"fmt"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
)

// databases holds the single SQLite database connection.
// All data (scripts, jobs, asset index, media assets) is consolidated
// into a single file at data/media/media.db.sqlite.
type databases struct {
	main *storage.SQLiteDB
}

func (d *databases) Close() {
	if d.main != nil {
		d.main.Close()
	}
}

func initDatabases(cfg *config.Config, log *zap.Logger) (*databases, error) {
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, storage.DBMedia, log)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize main database: %w", err)
	}

	return &databases{
		main: mainDB,
	}, nil
}
