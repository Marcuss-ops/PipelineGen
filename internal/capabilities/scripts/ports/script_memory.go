// Package ports — script_memory.go defines the canonical port for the
// gemma script exact-cache persistence layer.
//
// The application layer depends on this port; concrete implementations
// live in internal/infrastructure/database/sqlite/scripts (and in
// test fakes). Keeping the port in the application layer enforces the
// PR-REFACTOR-P0-IO-BINDER boundary: no package under
// internal/application/scripts/adapters may import database/sql.
package ports

import "context"

// MemoryGate is the canonical port for reading, writing, and
// maintaining the gemma_script_outputs exact-cache table.
type MemoryGate interface {
	// FindExactOutput returns a previously saved output for the given
	// cache coordinates. Implementations should return (nil, nil) when
	// no matching row exists.
	FindExactOutput(ctx context.Context, channelID, mode, inputHash string) (*GenerationOutput, error)

	// SaveGeneration persists the generated output for the given
	// inputs, performing an upsert on the unique
	// (channel_id, mode, input_hash) tuple. It returns the number of
	// rows affected (1 on insert or update).
	SaveGeneration(ctx context.Context, input SaveGenerationInput, output string) (int64, error)

	// DeleteExactOutputsByTitles removes all rows whose title is in
	// the provided slice. It returns the number of rows deleted.
	DeleteExactOutputsByTitles(ctx context.Context, titles []string) (int64, error)

	// SweepAll removes stale exact-cache rows. It returns the number
	// of rows deleted.
	SweepAll(ctx context.Context) (int64, error)
}

// GenerationOutput is the canonical read-side projection of a single
// gemma_script_outputs row. It intentionally omits columns that the
// service layer does not need.
type GenerationOutput struct {
	ID         string
	OutputText string
	WordCount  int
	Model      string
}

// SaveGenerationInput carries the inputs to save a generation result.
type SaveGenerationInput struct {
	ChannelID string
	Mode      string
	Language  string
	Title     string
	Prompt    string
	Model     string
	WordCount int
	CacheKey  string
	JobID     string
}
