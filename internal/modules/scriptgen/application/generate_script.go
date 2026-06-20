package application

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/modules/scriptgen/domain"
	"github.com/Marcuss-ops/PipelineGen/internal/modules/scriptgen/ports"
)

// GenerateScriptCommand is the typed command for the unified script
// generation use case. The same use case is invoked by both
// /api/script/generate-from-clips (async) and /api/script/generate-with-images
// (sync when the caller wants to wait inline).
type GenerateScriptCommand struct {
	Spec          domain.GenerationSpec
	Sync          bool // false ⇒ enqueue; true ⇒ run inline.
	CorrelationID string
}

// GenerateScript is the unified use case that absorbs (in dependency-free
// steps) planning, curation, writing, scene building, clip assignment,
// metadata, image request, document building, memory, batch.
type GenerateScript struct {
	deps Dependencies
}

// NewGenerateScript wires the use case with its injected dependencies.
func NewGenerateScript(deps Dependencies) *GenerateScript {
	return &GenerateScript{deps: deps}
}

// Execute runs the unified pipeline.
//
// Async path (cmd.Sync == false): the command is forwarded to the
// JobSubmitter port and only the JobReference is surfaced.
//
// Sync path (cmd.Sync == true): the pipeline orchestrates inline.
// Today the sync body is a typed stub that mirrors the contract so
// Agent 3+ can fold the legacy phases (planning, curation, writing,
// scenes, clip assignment, metadata, image request, doc, memory,
// batch) into Execute without changing its signature.
//
// TODOs are intentionally explicit and one-line so a future diff
// search across this file will find every phase that still needs to be
// migrated.
func (uc *GenerateScript) Execute(ctx context.Context, cmd GenerateScriptCommand) (*domain.GenerationResult, error) {
	started := time.Now().UTC()
	log := uc.logger()

	if cmd.Spec.Topic == "" || cmd.Spec.SourceText == "" {
		return nil, domain.ErrInvalidPayload
	}
	if uc.deps.LLM == nil {
		return nil, domain.ErrUnavailable
	}

	if !cmd.Sync {
		if uc.deps.Jobs == nil {
			return nil, domain.ErrUnavailable
		}
		ref, err := uc.deps.Jobs.SubmitGeneration(ctx, ports.GenerationPayload{
			Spec:          cmd.Spec,
			CorrelationID: cmd.CorrelationID,
		})
		if err != nil {
			return nil, err
		}
		log.Info("scriptgen.async.submitted",
			zap.String("job_id", ref.JobID),
			zap.String("correlation_id", cmd.CorrelationID),
		)
		return &domain.GenerationResult{
			OK:        true,
			JobID:     ref.JobID,
			Status:    ref.Status,
			Sync:      false,
			StartedAt: started,
		}, nil
	}

	// TODO: planning phase (LLM outline → Plan)
	// TODO: curation phase (Assets.PickForTopic + Search.SearchForScenes)
	// TODO: writing phase (LLM.Generate per OutlineSection)
	// TODO: scene building (split text → Scene[])
	// TODO: clip assignment (Search.SearchForScenes per Scene, then Assets.GetByID)
	// TODO: metadata (LLM.GenerateJSON over Scenes into typed POCO)
	// TODO: image request (port TBD; image gen owned by Agent Next)
	// TODO: document building (Docs.Create when Spec.GenerateDocs)
	// TODO: memory (write-back to long-term store, port TBD)
	// TODO: batch (fan-out via Jobs when N>1 chapters; here the spec is single-shot)
	log.Info("scriptgen.sync.completed_stub",
		zap.String("topic", cmd.Spec.Topic),
		zap.String("correlation_id", cmd.CorrelationID),
	)
	return &domain.GenerationResult{
		OK:         true,
		Sync:       true,
		Status:     domain.ScriptStatusCompleted,
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
	}, nil
}

// logger returns a non-nil logger even when deps.Log is nil.
func (uc *GenerateScript) logger() *zap.Logger {
	if uc.deps.Log != nil {
		return uc.deps.Log
	}
	return zap.NewNop()
}
