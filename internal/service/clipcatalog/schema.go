package clipcatalog

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"
)

// EnsureSchema adds the new metadata columns to the clips table if they don't exist
func EnsureSchema(ctx context.Context, db *sql.DB, logger *zap.Logger) error {
	columns := []struct {
		name string
		sql  string
	}{
		{"search_text", "ALTER TABLE clips ADD COLUMN search_text TEXT DEFAULT ''"},
		{"embedding_json", "ALTER TABLE clips ADD COLUMN embedding_json TEXT DEFAULT '[]'"},
		{"category", "ALTER TABLE clips ADD COLUMN category TEXT DEFAULT ''"},
		{"scene_type", "ALTER TABLE clips ADD COLUMN scene_type TEXT DEFAULT ''"},
		{"usable_for_json", "ALTER TABLE clips ADD COLUMN usable_for_json TEXT DEFAULT '[]'"},
		{"avoid_for_json", "ALTER TABLE clips ADD COLUMN avoid_for_json TEXT DEFAULT '[]'"},
		{"quality_score", "ALTER TABLE clips ADD COLUMN quality_score REAL DEFAULT 0.0"},
		{"reuse_count", "ALTER TABLE clips ADD COLUMN reuse_count INTEGER DEFAULT 0"},
		{"last_used_at", "ALTER TABLE clips ADD COLUMN last_used_at TEXT DEFAULT ''"},
		{"last_indexed_at", "ALTER TABLE clips ADD COLUMN last_indexed_at TEXT DEFAULT ''"},
	}

	for _, col := range columns {
		if err := addColumnIfNotExists(ctx, db, "clips", col.name, col.sql, logger); err != nil {
			return fmt.Errorf("failed to add column %s: %w", col.name, err)
		}
	}

	// Create index on search_text for faster LIKE queries
	if err := createIndexIfNotExists(ctx, db, "idx_clips_search_text", "CREATE INDEX idx_clips_search_text ON clips(search_text)", logger); err != nil {
		return fmt.Errorf("failed to create search_text index: %w", err)
	}

	// Create index on category
	if err := createIndexIfNotExists(ctx, db, "idx_clips_category", "CREATE INDEX idx_clips_category ON clips(category)", logger); err != nil {
		return fmt.Errorf("failed to create category index: %w", err)
	}

	// Create FTS5 virtual table
	if err := ensureFTSTable(ctx, db, logger); err != nil {
		return fmt.Errorf("failed to ensure FTS table: %w", err)
	}

	logger.Info("clipcatalog schema ensured")
	return nil
}

func ensureFTSTable(ctx context.Context, db *sql.DB, logger *zap.Logger) error {
	// Check if FTS5 is available
	var ftsAvailable int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_compile_options WHERE compile_options = 'ENABLE_FTS5'").Scan(&ftsAvailable)
	
	// Create the FTS table
	// Note: using simple FTS table without triggers for now for robustness
	sqlStmt := `
		CREATE VIRTUAL TABLE IF NOT EXISTS clips_fts USING fts5(
			clip_id UNINDEXED,
			name,
			search_text,
			tags,
			category,
			scene_type,
			tokenize='unicode61 remove_diacritics 2'
		);
	`
	if _, err := db.ExecContext(ctx, sqlStmt); err != nil {
		logger.Warn("failed to create FTS5 table (might not be supported)", zap.Error(err))
		return nil // Don't fail the whole startup if FTS5 is missing
	}

	logger.Info("FTS5 table ensured")
	return nil
}

func addColumnIfNotExists(ctx context.Context, db *sql.DB, table, column, sqlStmt string, logger *zap.Logger) error {
	// Check if column exists
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", table, column).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check column existence: %w", err)
	}

	if count > 0 {
		logger.Debug("column already exists", zap.String("column", column))
		return nil
	}

	// Add column
	if _, err := db.ExecContext(ctx, sqlStmt); err != nil {
		// Ignore "duplicate column" errors
		return err
	}

	logger.Info("added column to table", zap.String("table", table), zap.String("column", column))
	return nil
}

func createIndexIfNotExists(ctx context.Context, db *sql.DB, indexName, sqlStmt string, logger *zap.Logger) error {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", indexName).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to check index existence: %w", err)
	}

	if count > 0 {
		logger.Debug("index already exists", zap.String("index", indexName))
		return nil
	}

	if _, err := db.ExecContext(ctx, sqlStmt); err != nil {
		return err
	}

	logger.Info("created index", zap.String("index", indexName))
	return nil
}
