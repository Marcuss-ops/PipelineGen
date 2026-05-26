package storage

import "path/filepath"

// Database names and their subdirectories
// All databases consolidated into a single file.
const (
	DBVelox = "velox/velox.db.sqlite" // Unico database: Scripts, Pipeline, Jobs, Asset Index, Media Assets
	DBMedia = "velox/velox.db.sqlite" // Alias al database unico
)

// GetDBPath returns the full path to a database file given the data directory and db constant.
func GetDBPath(dataDir, dbConstant string) string {
	return filepath.Join(dataDir, dbConstant)
}

// GetAllDBs returns a list of all managed database relative paths.
func GetAllDBs() []string {
	return []string{
		DBVelox,
		DBMedia,
	}
}
