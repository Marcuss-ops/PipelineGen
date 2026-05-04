package storage

import (
	"database/sql"

	"go.uber.org/zap"
)

// HasFTS5 checks if the SQLite driver supports FTS5.
// It queries PRAGMA compile_options and looks for "ENABLE_FTS5".
// This check is done once at startup since it depends on the driver build, not the specific database.
func HasFTS5(db *sql.DB, log *zap.Logger) bool {
	rows, err := db.Query("PRAGMA compile_options")
	if err != nil {
		log.Warn("Failed to query PRAGMA compile_options", zap.Error(err))
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var option string
		if err := rows.Scan(&option); err != nil {
			continue
		}
		if option == "ENABLE_FTS5" {
			return true
		}
	}
	return false
}

// LogFTS5Status logs whether FTS5 is available.
// Should be called once at startup with any database connection (driver-dependent).
func LogFTS5Status(log *zap.Logger, dbs ...*SQLiteDB) {
	if len(dbs) == 0 {
		return
	}
	
	// Check using the first DB connection
	available := HasFTS5(dbs[0].DB, log)
	if available {
		log.Info("SQLite FTS5 is available")
	} else {
		log.Warn("SQLite FTS5 not available; clips search will use LIKE fallback")
	}
}
