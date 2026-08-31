// Package storage owns the canonical runtime storage topology for PipelineGen.
//
// Runtime code must not rediscover database paths or Qdrant collection names.
// The only configurable filesystem root is DataDir; the primary SQLite path is
// derived from it. Qdrant runtime identity is fixed by code/schema.
package storage

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// MediaDBFilename is the single primary SQLite database filename.
	MediaDBFilename = "media.db.sqlite"
	// MediaDBDirectory is the canonical directory below DataDir.
	MediaDBDirectory = "media"
	// QdrantProductionCollection is the sole physical collection available to
	// runtime readers/writers.
	QdrantProductionCollection = "media_assets"
	// QdrantRuntimeAlias is the sole runtime alias. It must resolve to the
	// production physical collection and never to candidates/recovery builds.
	QdrantRuntimeAlias = "media_assets_current"
)

// StorageTopology is the complete runtime identity of the canonical primary
// stores. Drive is intentionally absent: it stores asset bytes, not catalog
// truth.
type StorageTopology struct {
	MediaDBPath      string
	QdrantCollection string
	QdrantAlias      string
}

// CanonicalStorageTopology derives all runtime storage identity from DataDir.
// An empty dataDir intentionally resolves relative to ./data, matching the
// configuration default; callers that require an absolute path should pass an
// already-canonical absolute DataDir.
func CanonicalStorageTopology(dataDir string) StorageTopology {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "./data"
	}
	return StorageTopology{
		MediaDBPath:      filepath.Join(dataDir, MediaDBDirectory, MediaDBFilename),
		QdrantCollection: QdrantProductionCollection,
		QdrantAlias:      QdrantRuntimeAlias,
	}
}

// CanonicalMediaDBPath returns the only primary SQLite path runtime may open.
func CanonicalMediaDBPath(dataDir string) string {
	return CanonicalStorageTopology(dataDir).MediaDBPath
}

// RequireRuntimeCollection fails closed for candidates, versioned,
// recovery/synthetic/test collections, aliases, and arbitrary names.
func RequireRuntimeCollection(name string) error {
	if strings.TrimSpace(name) != QdrantProductionCollection {
		return fmt.Errorf("runtime Qdrant collection %q is forbidden; only %q is allowed", name, QdrantProductionCollection)
	}
	return nil
}

// RequireRuntimeAlias fails closed when a runtime path attempts to resolve a
// non-canonical alias.
func RequireRuntimeAlias(name string) error {
	if strings.TrimSpace(name) != QdrantRuntimeAlias {
		return fmt.Errorf("runtime Qdrant alias %q is forbidden; only %q is allowed", name, QdrantRuntimeAlias)
	}
	return nil
}
