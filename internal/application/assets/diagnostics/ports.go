// Package diagnostics provides application-layer use cases for system health
// checks: index health and asset statistics.
//
// PG-034 (June 2026): Qdrant status removed — vector-store capability deleted.
package diagnostics

import "context"

// IndexHealthReport is the canonical health-check result.
// PG-034 (June 2026): Qdrant-pointed fields removed — vector-search backend deleted.
type IndexHealthReport struct {
	OK       bool
	Degraded bool

	SQLiteAssets    int
	SQLiteIndexed   int
	PendingOutbox   int
	DeadLetter      int
	DegradedSources []string
}

// IndexHealthPort is the narrow interface for the realtime index-health check.
// PG-034 (June 2026): VectorStore() method removed — Qdrant capability deleted.
type IndexHealthPort interface {
	IndexHealth(ctx context.Context) (*IndexHealthReport, error)
}

// PG-034 (June 2026): VectorStorePort + CollectionInfo removed — Qdrant
// capability deleted.

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
