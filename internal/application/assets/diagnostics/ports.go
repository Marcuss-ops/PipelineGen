// Package diagnostics provides application-layer use cases for system health
// checks: index health, Qdrant status, and asset statistics.
package diagnostics

import "context"

// IndexHealthReport is the canonical health-check result.
type IndexHealthReport struct {
	OK       bool
	Degraded bool

	// PR3-5b fields
	SQLiteAssets    int
	SQLiteIndexed   int
	QdrantPoints    int64
	MissingInQdrant int
	OrphanInQdrant  int
	PendingOutbox   int
	DeadLetter      int
	DegradedSources []string

	// Legacy fields
	DBTotal         int
	WithEmbedding   int
	DBToQdrantDelta int
	StaleQdrantIDs  []string
}

// IndexHealthPort is the narrow interface for the realtime index-health check.
type IndexHealthPort interface {
	IndexHealth(ctx context.Context) (*IndexHealthReport, error)
	VectorStore() VectorStorePort
}

// VectorStorePort is a narrow Qdrant port for health checks.
type VectorStorePort interface {
	Health(ctx context.Context) error
	OperationCollectionInfo(ctx context.Context) (*CollectionInfo, error)
}

// CollectionInfo holds Qdrant collection metadata.
type CollectionInfo struct {
	PointsCount int64
}

// AssetStats are aggregated counts from the asset store.
type AssetStats struct {
	Total    int
	ByType   map[string]int
	ByStatus map[string]int
}

// AssetStatsPort retrieves asset statistics.
type AssetStatsPort interface {
	GetStats(ctx context.Context) (*AssetStats, error)
}

// Logger is a narrow logging port.
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}
