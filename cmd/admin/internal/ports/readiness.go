// Package ports defines typed ports for cmd/admin commands.
//
// Ports live here so that admin CLI packages can depend on abstract
// contracts rather than on database/sql or concrete infrastructure.
// Adapters for these ports live in cmd/admin/internal/database.
package ports

import "context"

// ReadinessCounters holds the legacy flat counters produced by the
// readiness scan. It is intentionally decoupled from the report type
// in package main so the port package never imports a main package.
type ReadinessCounters struct {
	TotalAssets              int
	NonMediaAssets           int
	InvalidTextVectors       int
	InvalidTranscriptVectors int
	InvalidVisualVectors     int
	InvalidAudioVectors      int
	MissingSourceFile        int
	LegacyStatusRows         int
	LegacyLocatorRows        int
}

// ReadinessInspector abstracts the SQLite inspection operations used by
// the qdrant readiness gate. Implementations are provided by the
// cmd/admin/internal/database package.
type ReadinessInspector interface {
	// InspectRequiredColumns returns the columns from the supplied
	// required list that are present in media_assets, and those that
	// are missing.
	InspectRequiredColumns(ctx context.Context, required []string) (present, missing []string, err error)

	// CollectReadinessCounters scans media_assets and returns the
	// canonical counters used by the readiness report.
	CollectReadinessCounters(ctx context.Context) (ReadinessCounters, error)

	// TableExists reports whether the named table exists in the
	// current database.
	TableExists(ctx context.Context, name string) bool
}
