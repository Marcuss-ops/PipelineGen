package main

// qdrantReadinessReport is the production-grade readiness gate
// output. `Checks` is the canonical {check-name -> "pass"|"fail"} map
// the operational dashboard consumes; the flat fields below preserve
// the v1 shape for backwards compat with ops scripts.
type qdrantReadinessReport struct {
	Ready                      bool              `json:"ready"`
	Checks                     map[string]string `json:"checks,omitempty"`
	QdrantReachable            bool              `json:"qdrant_reachable"`
	SQLiteMigrationsComplete   bool              `json:"sqlite_migrations_complete"`
	ActiveCollection           string            `json:"active_collection,omitempty"`
	ActiveCollectionCompatible bool              `json:"active_collection_compatible"`
	RequiredColumnsPresent     []string          `json:"required_columns_present,omitempty"`
	MissingColumns             []string          `json:"missing_columns,omitempty"`
	TotalAssets                int               `json:"total_assets"`
	NonMediaAssets             int               `json:"non_media_assets"`
	InvalidTextVectors         int               `json:"invalid_text_vectors"`
	InvalidTranscriptVectors   int               `json:"invalid_transcript_vectors"`
	InvalidVisualVectors       int               `json:"invalid_visual_vectors"`
	InvalidAudioVectors        int               `json:"invalid_audio_vectors"`
	SchemaErrors               int               `json:"schema_errors"`
	// Projection parity (plan item #14): eligible SQLite asset IDs vs
	// the ACTIVE Qdrant projection — never the total INDEXED count.
	ProjectionEligibleSQLite int               `json:"projection_eligible_sqlite"`
	ProjectionQdrantPoints   int               `json:"projection_qdrant_points"`
	ProjectionMissingCount   int               `json:"projection_missing_count"`
	ProjectionOrphanCount    int               `json:"projection_orphan_count"`
	MissingSourceFile        int               `json:"missing_source_file"`
	LegacyStatusRows           int               `json:"legacy_status_rows"`
	LegacyLocatorRows          int               `json:"legacy_locator_rows"`
	OutboxOperational          bool              `json:"outbox_operational"`
}
