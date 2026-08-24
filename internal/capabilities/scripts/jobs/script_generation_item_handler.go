// Package jobs — script.generate_item child handler: decodes
// ScriptGenerateItemPayload, dispatches to GenerateOneUseCase,
// returns per-item result map.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"

	"go.uber.org/zap"
)

// ScriptGenerateItemPayload is the typed per-item child payload emitted
// by the fan-out adapter (wire_script.go::EnqueueScriptItem) and decoded
// by ScriptGenerateItemJobHandler.HandleJob. Using a typed struct avoids
// double-marshalling and ensures ActiveKey determinism.
type ScriptGenerateItemPayload struct {
	ParentJobID string                        `json:"parent_job_id"`
	Item        domainScript.GenerationItemV2 `json:"item"`
	Preset      domainScript.Preset           `json:"preset"`
	ItemIndex   int                           `json:"item_index"`
}

// GenerateOneExecutor is the narrow Pattern-0 port the child handler
// needs. The production *usecase.GenerateOneUseCase satisfies this
// implicitly; tests inject stubs without instantiating the full
// 5-resolver + postprocessor pipeline. The tracker parameter
// matches the canonical GenerateOneUseCase.Execute signature — the
// canonical tracker is *usecase.ProgressTracker (constructed from
// NewProgressTracker(progressFn, id)); nil is acceptable when the
// caller does not need progress reporting (the canonical child
// handler emits progress via tools.Progress, not via the per-item
// tracker).
type GenerateOneExecutor interface {
	Execute(ctx context.Context, item domainScript.GenerationItemV2, preset domainScript.Preset, tracker *usecase.ProgressTracker) (*domainScript.GenerationResult, error)
}

// Compile-time assertion: *usecase.GenerateOneUseCase satisfies
// GenerateOneExecutor. The assertion runs in the composition root
// (internal/app/composition.go) via `var _ GenerateOneExecutor =
// (*usecase.GenerateOneUseCase)(nil)`; the canonical job pkg does
// not import usecase to keep port directionality clean.
var _ GenerateOneExecutor = (*usecase.GenerateOneUseCase)(nil)

// ScriptGenerateItemJobHandler is the canonical per-item child
// handler for script.generate_item jobs (job.TypeScriptGenerateItem).
// The oneUC field is typed as the narrow GenerateOneExecutor port
// (Pattern 0 — AGENTS.md) so test fixtures can inject a recording
// stub without instantiating the full 5-resolver registry.
type ScriptGenerateItemJobHandler struct {
	oneUC  GenerateOneExecutor
	logger *zap.Logger
}

// NewScriptGenerateItemJobHandler constructs the handler. oneUC is
// MANDATORY (panic on nil — fail-fast per AGENTS.md WireUp pattern).
// Logger is OPTIONAL (nil-safe via zap.NewNop()).
func NewScriptGenerateItemJobHandler(
	oneUC GenerateOneExecutor,
	logger *zap.Logger,
) *ScriptGenerateItemJobHandler {
	if oneUC == nil {
		panic("scripts.Jobs.NewScriptGenerateItemJobHandler: oneUC is required (GenerateOneExecutor port)")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ScriptGenerateItemJobHandler{
		oneUC:  oneUC,
		logger: logger,
	}
}

// Register binds the handler to the canonical jobs.Service dispatcher
// for the script.generate_item job type. Idempotent via the
// dispatcher double-Register protection. Returns error so the
// composition root can fail-closed at boot (mirrors GenerateItemJobHandler
// in voiceover/jobs/generate_item_handler.go).
func (h *ScriptGenerateItemJobHandler) Register(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("ScriptGenerateItemJobHandler.Register: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(domainScript.TypeGenerateItem, appjobs.HandlerFunc(h.HandleJob)); err != nil {
		return fmt.Errorf("ScriptGenerateItemJobHandler.Register: bind %q to dispatcher: %w",
			domainScript.TypeGenerateItem, err)
	}
	if h.logger != nil {
		h.logger.Info("registered script.generate_item handler",
			zap.String("job_type", domainScript.TypeGenerateItem))
	}
	return nil
}

// HandleJob processes a script.generate_item child job: decodes
// payload, dispatches to GenerateOneUseCase, returns per-item result.
// Returns (resultMap, err) so the dispatcher marks the job FAILED
// or SUCCEEDED correctly (godlike/07 no-fake-availability).
func (h *ScriptGenerateItemJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	h.logger.Info("handling script.generate_item job",
		zap.String("job_id", j.ID))

	// job-tools.Progress nil-safe wrapper — canonical for all handlers
	// registered with the broker (voiceover.generate, voiceover.generate_item,
	// script.generate, script.generate_item). The Creator-runtime wrap path
	// passes tools=nil; the SafeProgressFn utility captures the canonical
	// nil-tolerance gate so consumer sites can call pf(...) directly
	// without per-call nil checks.
	pf := appjobs.SafeProgressFn(tools)
	ef := appjobs.SafeEventFn(tools)
	pf(5, "starting script.generate_item")

	// Decode the typed ScriptGenerateItemPayload. The fan-out adapter
	// (wire_script.go::EnqueueScriptItem) emits this typed struct so
	// the child can decode its own work scope without re-contextualising
	// the envelope.
	var childPayload ScriptGenerateItemPayload
	if err := json.Unmarshal(j.Payload, &childPayload); err != nil {
		pf(100, "script.generate_item unmarshal failed")
		return nil, fmt.Errorf("script.generate_item: unmarshal ScriptGenerateItemPayload: %w", err)
	}

	item := childPayload.Item
	parentJobID := childPayload.ParentJobID
	preset := childPayload.Preset
	ef("stage_progress", "Stage progress updated", map[string]any{
		"stage": string(job.StageScript), "language": item.Language,
		"status": string(job.StageRunning), "job_id": j.ID,
	})

	h.logger.Info("decoded script.generate_item payload",
		zap.String("job_id", j.ID),
		zap.String("parent_job_id", parentJobID),
		zap.String("item_id", item.ID))

	if item.ID == "" {
		// Defensive: a missing item.ID would corrupt the parent's
		// child_job_ids index. The fan-out's id defaulting logic
		// (GenerateManyFanoutUseCase.setItemID) should always set
		// this, but the child must reject a bare payload.
		return nil, fmt.Errorf("script.generate_item: item.ID is empty (fan-out id defaulting required)")
	}

	if parentJobID == "" {
		parentJobID = "unknown"
	}

	if preset == "" {
		preset = domainScript.PresetCustom
	}

	// Per-item tracker is discarded — the child's progress is reflected
	// in tools.Progress (above) only. The parent's GenerateManyFanoutUseCase
	// is responsible for the aggregate progress reporting. A nil
	// tracker is canonically accepted by GenerateOneUseCase.Execute.
	tracker := (*usecase.ProgressTracker)(nil)

	execCtx := ctx
	if parentJobID != "" && parentJobID != "unknown" {
		execCtx = context.WithValue(ctx, "script_job_id", parentJobID)
	} else if j.ID != "" {
		execCtx = context.WithValue(ctx, "script_job_id", j.ID)
	}

	res, err := h.oneUC.Execute(execCtx, item, preset, tracker)
	if err != nil {
		ef("stage_progress", "Stage progress updated", map[string]any{
			"stage": string(job.StageScript), "language": item.Language,
			"status": string(job.StageFailed), "job_id": j.ID, "error": err.Error(),
		})
		h.logger.Error("script.generate_item execution failure",
			zap.String("job_id", j.ID),
			zap.String("item_id", item.ID),
			zap.Error(err))
		pf(100, "script.generate_item execution failed")
		return toScriptItemResultMap(item.ID, item.Language, j.ID, parentJobID, false, err.Error(), nil), fmt.Errorf("script.generate_item: execute: %w", err)
	}

	// Structural emptiness gate: no Text, ScriptID, or cache.Hit
	// → semantic failure (false-success gate).
	ok := scriptItemIsSuccessful(res)
	if !ok {
		errMsg := fmt.Sprintf("script.generate_item: per-item pipeline produced structurally empty result (P0 #4 P0.1-gate-extension, ok=false)")
		h.logger.Error("script.generate_item semantic failure (P0 #1-style false-success gate EXTENDED to scripts P0 #4)",
			zap.String("job_id", j.ID),
			zap.String("item_id", item.ID),
			zap.String("result_text_len", fmt.Sprintf("%d", len(res.Output.Text))))
		pf(100, "script.generate_item semantic failure: ok=false")
		return toScriptItemResultMap(item.ID, item.Language, j.ID, parentJobID, false, errMsg, res),
			fmt.Errorf("%s", errMsg)
	}

	ef("stage_progress", "Stage progress updated", map[string]any{
		"stage": string(job.StageScript), "language": item.Language,
		"status": string(job.StageCompleted), "job_id": j.ID,
	})
	if res != nil {
		if progress, ok := res.StageProgress[string(job.StagePersistence)]; ok {
			ef("stage_progress", "Stage progress updated", map[string]any{
				"stage": string(job.StagePersistence), "language": item.Language,
				"status": map[bool]string{true: string(job.StageCompleted), false: string(job.StageFailed)}[progress.Completed == progress.Total && progress.Total > 0],
				"job_id": j.ID, "stage_progress": res.StageProgress,
			})
		}
	}
	pf(100, "script.generate_item execution complete")
	return toScriptItemResultMap(item.ID, item.Language, j.ID, parentJobID, true, "", res), nil
}

// toScriptItemResultMap serialises a per-item outcome into the
// map[string]any shape the dispatcher writes into job.Result JSON.
// The aggregator reads `ok`, `status`, `item_id`, `error`.
// When res is non-nil and carries a Document artifact, the doc_link
// and doc_id are propagated into the result map so downstream
// consumers (aggregator, API handler) can surface the Google Doc
// link to operators (2026-07-07 fix for the child-handler drop).
func toScriptItemResultMap(itemID, requestedLanguage, childJobID, parentJobID string, ok bool, errStr string, res *domainScript.GenerationResult) map[string]any {
	m := map[string]any{
		"item_id":       itemID,
		"job_id":        childJobID,
		"parent_job_id": parentJobID,
		"stage":         string(job.StageScript),
		"language":      scriptItemLanguage(requestedLanguage, res),
		"ok":            ok,
		"status":        scriptItemStatus(ok),
	}
	if errStr != "" {
		m["error"] = errStr
	}
	if res != nil {
		m["stage_progress"] = res.StageProgress
		// Preserve the canonical retrieval trace at the job boundary. Without
		// this, /api/jobs/:id/full exposed the generated scenes but dropped the
		// evidence that source.search actually used Qdrant results and which
		// accepted clip IDs reached SpecScene.
		if len(res.Source.SearchResults) > 0 || len(res.Source.AcceptedClipIDs) > 0 ||
			res.Source.ResearchReport != nil || res.Source.ResearchEvidence != nil {
			m["source"] = res.Source
		}
	}
	if res != nil && res.Artifacts.Document != nil {
		m["doc_id"] = res.Artifacts.Document.DocID
		m["doc_link"] = res.Artifacts.Document.DocLink
	}
	return m
}

// scriptItemLanguage returns the generated item's language when the
// child result carries it. Empty is retained for legacy results that
// predate per-language child telemetry.
func scriptItemLanguage(requested string, res *domainScript.GenerationResult) string {
	if res != nil && res.Language != "" {
		return res.Language
	}
	return requested
}

// scriptItemStatus returns "completed" or "failed" based on the
// caller-computed ok value.
func scriptItemStatus(ok bool) string {
	if ok {
		return "completed"
	}
	return "failed"
}

// scriptItemIsSuccessful is the canonical scripts-side P0 #1 gate
// heuristic. Returns true when the typed GenerationResult carries a
// usable output (Text non-empty OR persisted to DB OR served from
// the in-memory cache). This is the canonical mirror of voiceover's
// explicit `OK *bool` field; scripts derive the boolean from typed
// field presence. The aggregator applies the P0.1 override (broker=
// SUCCEEDED + ok=false → child=FAILED) using THIS function as the
// authoritative truth (see parent_aggregator.go::scriptItemP0_1Gate).
func scriptItemIsSuccessful(res *domainScript.GenerationResult) bool {
	if res == nil {
		return false
	}
	if res.Output.Text != "" {
		return true
	}
	if res.ScriptID != 0 {
		return true
	}
	if res.Cache.Hit {
		return true
	}
	return false
}
