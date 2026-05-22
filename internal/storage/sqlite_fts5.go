package storage

import (
	"database/sql"

	"go.uber.org/zap"
)

func HasFTS5(db *sql.DB, log *zap.Logger) bool {
	if log == nil {
		log = zap.NewNop()
	}
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
