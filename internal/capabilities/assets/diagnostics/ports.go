// Package diagnostics provides application-layer use cases for system health
// checks: index health and asset statistics.
package diagnostics

import "context"

// IndexHealthReport is the canonical health-check result.
type IndexHealthReport struct {
	OK       bool `json:"ok"`
	Degraded bool `json:"degraded,omitempty"`

	// SQLite counts (real data from media_assets).
	SQLiteAssets    int `json:"sqlite_assets"`
	SQLiteIndexed   int `json:"sqlite_indexed"`
	SQLiteIndexable int `json:"sqlite_indexable"`

	// pgvector counts (real data from CountPoints).
	VectorPoints int `json:"vector_points"`

	// Drift detected (negative = missing in pgvector, positive = orphan).
	MissingInVectorIndex int `json:"missing_in_vectors"`
	OrphanInVectorIndex  int `json:"orphan_in_vectors"`
	StaleIndexVersion    int `json:"stale_index_version"`

	// Outbox pipeline health.
	PendingOutbox int `json:"pending_outbox"`
	DeadLetter    int `json:"dead_letter"`

	// Operational metadata.
	IndexVersion    string   `json:"index_version"`
	DegradedSources []string `json:"degraded_sources,omitempty"`
	CheckedAt       string   `json:"checked_at"`
}

// IndexHealthPort is the narrow interface for index-health checks.
// VECTOR-005 Fase 1 (June 2026): restored with real pgvector dependency.
type IndexHealthPort interface {
	IndexHealth(ctx context.Context) (*IndexHealthReport, error)
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
