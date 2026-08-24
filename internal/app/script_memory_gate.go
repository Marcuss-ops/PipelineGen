package app

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
)

// scriptMemoryGate is the canonical adapter between the application-layer
// script memory port (scriptports.MemoryGate) and the infrastructure
// SQLite repository (sqlitescripts.MemoryRepository).
//
// It lives in internal/app because wiring is the composition root's
// responsibility; no application-layer package is allowed to import
// database/sql.
type scriptMemoryGate struct {
	repo *sqlitescripts.MemoryRepository
}

// newScriptMemoryGate constructs the adapter.
func newScriptMemoryGate(repo *sqlitescripts.MemoryRepository) *scriptMemoryGate {
	return &scriptMemoryGate{repo: repo}
}

// FindExactOutput implements scriptports.MemoryGate.
func (g *scriptMemoryGate) FindExactOutput(ctx context.Context, channelID, mode, inputHash string) (*scriptports.GenerationOutput, error) {
	if g.repo == nil {
		return nil, nil
	}
	out, err := g.repo.FindExactOutput(ctx, channelID, mode, inputHash)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	// Best-effort touch: keep the cached row fresh.
	_, _ = g.repo.TouchExactOutput(ctx, out.ID)
	return &scriptports.GenerationOutput{
		ID:         out.ID,
		OutputText: out.OutputText,
		WordCount:  out.WordCount,
		Model:      out.Model,
	}, nil
}

// SaveGeneration implements scriptports.MemoryGate.
func (g *scriptMemoryGate) SaveGeneration(ctx context.Context, input scriptports.SaveGenerationInput, output string) (int64, error) {
	if g.repo == nil {
		return 0, nil
	}
	hashKey := input.CacheKey
	if hashKey == "" {
		hashKey = input.Title
	}
	if hashKey == "" {
		return 0, nil
	}
	in := sqlitescripts.SaveGenerationInput{
		ChannelID:  input.ChannelID,
		Mode:       input.Mode,
		Language:   input.Language,
		Title:      input.Title,
		Prompt:     input.Prompt,
		Model:      input.Model,
		JobID:      input.JobID,
		OutputText: output,
		WordCount:  input.WordCount,
	}
	id, err := g.repo.SaveGeneration(ctx, in, input.Prompt, hashKey)
	if err != nil {
		return 0, err
	}
	if id == "" {
		return 0, nil
	}
	return 1, nil
}

// DeleteExactOutputsByTitles implements scriptports.MemoryGate.
func (g *scriptMemoryGate) DeleteExactOutputsByTitles(ctx context.Context, titles []string) (int64, error) {
	if g.repo == nil {
		return 0, nil
	}
	return g.repo.DeleteExactOutputsByTitles(ctx, titles)
}

// SweepAll implements scriptports.MemoryGate.
func (g *scriptMemoryGate) SweepAll(ctx context.Context) (int64, error) {
	if g.repo == nil {
		return 0, nil
	}
	return g.repo.SweepExactOutputs(ctx)
}

// Compile-time assertion: scriptMemoryGate satisfies the application port.
var _ scriptports.MemoryGate = (*scriptMemoryGate)(nil)
