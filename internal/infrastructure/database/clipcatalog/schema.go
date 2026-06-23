package clipcatalog

import (
	"context"
	"database/sql"

	"go.uber.org/zap"
)

// EnsureSchema adds the new metadata columns to the clips table if they don't exist
func EnsureSchema(ctx context.Context, db *sql.DB, logger *zap.Logger) error {
	logger.Info("clipcatalog schema is managed by unified media migrations")
	return nil
}
