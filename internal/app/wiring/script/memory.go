package script

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
)

// MemoryGate adapts the script memory port to the SQLite memory repository.
// The wiring package owns construction while the capability continues to see
// only scriptports.MemoryGate.
type MemoryGate struct {
	repo *sqlitescripts.MemoryRepository
}

// NewMemoryGate constructs the canonical script memory adapter.
func NewMemoryGate(repo *sqlitescripts.MemoryRepository) *MemoryGate {
	return &MemoryGate{repo: repo}
}

// FindExactOutput implements scriptports.MemoryGate.
func (g *MemoryGate) FindExactOutput(ctx context.Context, channelID, mode, inputHash string) (*scriptports.GenerationOutput, error) {
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
	_, _ = g.repo.TouchExactOutput(ctx, out.ID)
	return &scriptports.GenerationOutput{
		ID:         out.ID,
		OutputText: out.OutputText,
		WordCount:  out.WordCount,
		Model:      out.Model,
	}, nil
}

// SaveGeneration implements scriptports.MemoryGate.
func (g *MemoryGate) SaveGeneration(ctx context.Context, input scriptports.SaveGenerationInput, output string) (int64, error) {
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
func (g *MemoryGate) DeleteExactOutputsByTitles(ctx context.Context, titles []string) (int64, error) {
	if g.repo == nil {
		return 0, nil
	}
	return g.repo.DeleteExactOutputsByTitles(ctx, titles)
}

// SweepAll implements scriptports.MemoryGate.
func (g *MemoryGate) SweepAll(ctx context.Context) (int64, error) {
	if g.repo == nil {
		return 0, nil
	}
	return g.repo.SweepExactOutputs(ctx)
}

var _ scriptports.MemoryGate = (*MemoryGate)(nil)
