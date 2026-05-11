package storage

import "path/filepath"

// Database names and their subdirectories
const (
	DBVelox     = "velox/velox.db.sqlite"     // Main: Scripts, Pipeline, Jobs, Asset Index
	DBStock     = "stock/stock.db.sqlite"     // Stock footage metadata
	DBClips     = "clips/clips.db.sqlite"     // YouTube clips metadata
	DBArtlist   = "artlist/artlist.db.sqlite" // Artlist assets metadata
	DBImages    = "images/images.db.sqlite"   // Images metadata
	DBVoiceover = "voiceover/voiceover.db.sqlite" // Voiceover metadata
)

// GetDBPath returns the full path to a database file given the data directory and db constant.
func GetDBPath(dataDir, dbConstant string) string {
	return filepath.Join(dataDir, dbConstant)
}

// GetAllDBs returns a list of all managed database relative paths.
func GetAllDBs() []string {
	return []string{
		DBVelox,
		DBStock,
		DBClips,
		DBArtlist,
		DBImages,
		DBVoiceover,
	}
}
