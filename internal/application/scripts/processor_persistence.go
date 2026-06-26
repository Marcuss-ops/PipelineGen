// Package scripts — processor_persistence.go saves the generated
// script to the database. Enabled as "persistence" in the plan's
// Postprocessors list.
package scripts

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// PersistenceProcessor saves the generated script to the scripts
// table via ScriptRepository.
type PersistenceProcessor struct {
	repo ScriptRepository
}

// NewPersistenceProcessor creates a PersistenceProcessor.
func NewPersistenceProcessor(repo ScriptRepository) *PersistenceProcessor {
	return &PersistenceProcessor{repo: repo}
}

func (p *PersistenceProcessor) Name() string { return "persistence" }

func (p *PersistenceProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, script string) (*PostProcessResult, error) {
	if p.repo == nil {
		return nil, fmt.Errorf("%w: persistence processor: ScriptRepository not configured", scriptpkg.ErrPostprocessFailed)
	}
	if script == "" {
		return &PostProcessResult{}, nil
	}

	rec := &ScriptRecord{
		Title:          plan.Title,
		Topic:          plan.Topic,
		Language:       plan.Language,
		Tone:           plan.Tone,
		Model:          plan.Model,
		ModelUsed:      plan.Model,
		Mode:           plan.Mode,
		Status:         "completed",
		TargetWords:    plan.TargetWords,
		FinalWordCount: 0, // filled after generation
		OutputText:     script,
		NarrativeText:  script,
		FullDocument:   script,
		Version:        1,
	}

	scriptID, err := p.repo.SaveScript(ctx, rec, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: persistence processor: SaveScript failed: %w", scriptpkg.ErrPostprocessFailed, err)
	}

	return &PostProcessResult{
		ScriptID: scriptID,
	}, nil
}
