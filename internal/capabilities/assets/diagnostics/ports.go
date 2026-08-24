// Package diagnostics provides application-layer use cases for system health
// checks: index health and asset statistics.
//
// QDRANT-005 Fase 1 (June 2026): Qdrant fields restored — vector-store
// capability reintroduced via QDRANT-001..004.
package diagnostics

import "context"

// IndexHealthReport is the canonical health-check result.
// QDRANT-005 Fase 1 (June 2026): Qdrant fields restored.
type IndexHealthReport struct {
	OK       bool `json:"ok"`
	Degraded bool `json:"degraded,omitempty"`

	// SQLite counts (real data from media_assets).
	SQLiteAssets    int `json:"sqlite_assets"`
	SQLiteIndexed   int `json:"sqlite_indexed"`
	SQLiteIndexable int `json:"sqlite_indexable"`

	// Qdrant counts (real data from CountPoints).
	QdrantPoints int `json:"qdrant_points"`

	// Drift detected (negative = missing in Qdrant, positive = orphan).
	MissingInQdrant   int `json:"missing_in_qdrant"`
	OrphanInQdrant    int `json:"orphan_in_qdrant"`
	StaleIndexVersion int `json:"stale_index_version"`

	// Outbox pipeline health.
	PendingOutbox int `json:"pending_outbox"`
	DeadLetter    int `json:"dead_letter"`

	// Operational metadata.
	IndexVersion    string   `json:"index_version"`
	DegradedSources []string `json:"degraded_sources,omitempty"`
	CheckedAt       string   `json:"checked_at"`
}

// IndexHealthPort is the narrow interface for index-health checks.
// QDRANT-005 Fase 1 (June 2026): restored with real Qdrant dependency.
type IndexHealthPort interface {
	IndexHealth(ctx context.Context) (*IndexHealthReport, error)
}

// QdrantHealthPort provides access to Qdrant diagnostics for the
// IndexHealth adapter. Accepts the collection name to query.
type QdrantHealthPort interface {
	CountPoints(ctx context.Context, collection string) (int, error)
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
