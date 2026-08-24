// cmd/admin/qdrant_readiness_db.go — SQLite inspection helpers for the
// qdrant readiness gate.
//
// These helpers are thin wrappers around the typed
// ports.ReadinessInspector implemented in
// cmd/admin/internal/database/readiness.go. They are kept here so
// existing callers (qdrant_readiness.go, qdrant_readiness_checks_db.go,
// qdrant_readiness_test.go) continue to compile without changes during
// the phased migration away from raw *sql.DB.
package qdrant

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/database"
)

func inspectRequiredColumns(ctx context.Context, db *sql.DB, required []string) ([]string, []string, error) {
	return database.NewReadinessInspector(db).InspectRequiredColumns(ctx, required)
}

func collectReadinessCounters(ctx context.Context, db *sql.DB, report *qdrantReadinessReport) error {
	counters, err := database.NewReadinessInspector(db).CollectReadinessCounters(ctx)
	if err != nil {
		return err
	}
	report.TotalAssets = counters.TotalAssets
	report.NonMediaAssets = counters.NonMediaAssets
	report.InvalidTextVectors = counters.InvalidTextVectors
	report.InvalidTranscriptVectors = counters.InvalidTranscriptVectors
	report.InvalidVisualVectors = counters.InvalidVisualVectors
	report.InvalidAudioVectors = counters.InvalidAudioVectors
	report.MissingSourceFile = counters.MissingSourceFile
	report.LegacyStatusRows = counters.LegacyStatusRows
	report.LegacyLocatorRows = counters.LegacyLocatorRows
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) bool {
	return database.NewReadinessInspector(db).TableExists(ctx, name)
}
