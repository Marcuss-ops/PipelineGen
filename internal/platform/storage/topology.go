// Package storage owns the canonical runtime storage topology for PipelineGen.
//
// Runtime code must not rediscover database paths. The only configurable
// filesystem root is DataDir; the primary SQLite path is derived from it.
// pgvector collection identity was removed with the legacy vector engine on 2026-11-17;
// media vectors live in PostgreSQL + pgvector.
package storage

import ()

const (
	// MediaDBFilename is the single primary SQLite database filename.
	MediaDBFilename = "media.db.sqlite"
	// MediaDBDirectory is the canonical directory below DataDir.
	MediaDBDirectory = "media"
)

// StorageTopology is the complete runtime identity of the canonical primary
// stores. Drive is intentionally absent: it stores asset bytes, not catalog
// truth.
type StorageTopology struct {
	MediaDBPath string
}
