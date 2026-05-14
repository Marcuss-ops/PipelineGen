package storage

import "path/filepath"

// Database names and their subdirectories
const (
	DBVelox     = "velox/velox.db.sqlite"     // Main: Scripts, Pipeline, Jobs, Asset Index
	DBMedia     = "media/media.db.sqlite"     // Unificato: YouTube, Artlist, Stock, Immagini, Voiceovers
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
