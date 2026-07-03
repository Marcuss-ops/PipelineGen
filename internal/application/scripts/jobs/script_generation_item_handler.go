// Package jobs — script_generation_item_handler.go (P0 #4 audit 2026-07-03
// closure: per-item retry in script batches via canonical child-job
// architecture).
//
// ScriptGenerateItemJobHandler is the canonical per-item child handler
// for script.generate_item jobs scheduled by GenerateManyFanoutUseCase
// inside generation_job.go::Handle multi-item path. Reaches the canonical
// single-item pipeline via the narrow GenerateOneExecutor port
// (Pattern 0 — AGENTS.md) rather than the legacy "use the parent's
// GenerateOneUseCase directly" indirection.
//
// Audit-pin invariants (P0 #4 — pass-through, no recalc):
//   - item.ID, item.Source, item.Preset are pre-populated by the
//     fan-out handler; Execute trusts them verbatim. NO re-derivation
//     in the child handler.
//   - the handler dispatches oneUC.Execute(ctx, &item) directly with
//     no envelope re-contextualisation at any layer.
//
// NO goroutines are spawned inside the handler — sibling dispatch is
// regulated by the worker pool's per-job-type Concurrency field
// (= 4, configured in registry.go::Compose for TypeScriptGenerateItem).
//
// godlike/07 fail-closed contract: every failure path returns a
// non-nil Go error so the broker marks the job FAILED. Per-item
// result.ok=false on a >StatusCompleted result is the canonical
// P0.1-style false-success gate — the handler returns
// (resultMap, wrappedErr) so the broker cannot silently mark
// the child SUCCEEDED with result.ok=false.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	jobpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	"go.uber.org/zap"
)

// ScriptGenerateItemPayload is the typed per-item child payload emitted
// by the fan-out adapter (wire_script.go::EnqueueScriptItem) and decoded
// by ScriptGenerateItemJobHandler.HandleJob. Using a typed struct avoids
// double-marshalling and ensures ActiveKey determinism.
type ScriptGenerateItemPayload struct {
	ParentJobID string                      `json:"parent_job_id"`
	Item        domainScript.GenerationItemV2 `json:"item"`
	Preset      domainScript.Preset          `json:"preset"`
	ItemIndex   int                          `json:"item_index"`
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
	oneUC       GenerateOneExecutor
	normalCfg   adapters.NormalizationConfig
	requestIDFn func(ctx context.Context, parentJobID string) string
	logger      *zap.Logger
}

// NewScriptGenerateItemJobHandler constructs the handler. oneUC is
// MANDATORY (panic on nil — fail-fast per AGENTS.md WireUp pattern).
// Logger is OPTIONAL (nil-safe via zap.NewNop()).
func NewScriptGenerateItemJobHandler(
	oneUC GenerateOneExecutor,
	normalCfg adapters.NormalizationConfig,
	requestIDFn func(ctx context.Context, parentJobID string) string,
	logger *zap.Logger,
) *ScriptGenerateItemJobHandler {
	if oneUC == nil {
		panic("scripts.Jobs.NewScriptGenerateItemJobHandler: oneUC is required (GenerateOneExecutor port)")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	if requestIDFn == nil {
		// Default: derive a stable request_id from parentJobID per-item
		// so the per-item child result is greppable from the parent.
		requestIDFn = func(_ context.Context, parentJobID string) string {
			return parentJobID + ":item"
		}
	}
	return &ScriptGenerateItemJobHandler{
		oneUC:       oneUC,
		normalCfg:   normalCfg,
		requestIDFn: requestIDFn,
		logger:      logger,
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
	if err := jobsSvc.RegisterHandler(jobpkg.TypeScriptGenerateItem, h.HandleJob); err != nil {
		return fmt.Errorf("ScriptGenerateItemJobHandler.Register: bind %q to dispatcher: %w",
			jobpkg.TypeScriptGenerateItem, err)
	}
	if h.logger != nil {
		h.logger.Info("registered script.generate_item handler",
			zap.String("job_type", jobpkg.TypeScriptGenerateItem))
	}
	return nil
}

// HandleJob processes a script.generate_item child job from the
// queue. Dispatch contract (P0 #4 + godlike/07 — no fake availability):
//   - json.Unmarshal failure → (nil, err): dispatcher marks job FAILED.
//   - oneUC.Execute infra error → (toItemResultMap(res, &item, j.ID, false), err):
//     dispatcher marks job FAILED. The resultMap carries the per-item
//     status so operators see exactly which item failed + why.
//   - oneUC.Execute returns res with res.OK == false (semantic failure,
//     P0 #1-style false-success gate EXTENDED to scripts P0 #4):
//     the per-item pipeline returned a failed result without a Go
//     error. The handler MUST surface this as a dispatcher error so
//     the worker treats it as FAILED — returning (resultMap, nil) here
//     would produce a silent false-success at the child layer.
//   - oneUC.Execute success (res.OK == true) → (resultMap, nil):
//     dispatcher marks job SUCCEEDED.
//
// Progress: tools.Progress is called once at start (5%) and once at
// end (100%). Per-item progress is reported by the parent's
// GenerateManyFanoutUseCase aggregate (mirrors voiceover's per-language
// progress model).
func (h *ScriptGenerateItemJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	h.logger.Info("handling script.generate_item job",
		zap.String("job_id", j.ID),
		zap.String("parent_job_id", string(j.Payload))) // log full payload as raw for fallback; payload-bytes compare is cheap

	// job-tools.Progress nil-safe wrapper — canonical for all handlers
	// registered with the broker (voiceover.generate, voiceover.generate_item,
	// script.generate, script.generate_item). The Creator-runtime wrap path
	// passes tools=nil; the SafeProgressFn utility captures the canonical
	// nil-tolerance gate so consumer sites can call pf(...) directly
	// without per-call nil checks.
	pf := appjobs.SafeProgressFn(tools)
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

	res, err := h.oneUC.Execute(ctx, item, preset, tracker)
	if err != nil {
		h.logger.Error("script.generate_item execution failure",
			zap.String("job_id", j.ID),
			zap.String("item_id", item.ID),
			zap.Error(err))
		pf(100, "script.generate_item execution failed")
		return toScriptItemResultMap(res, item.ID, j.ID, parentJobID, false, err.Error()), fmt.Errorf("script.generate_item: execute: %w", err)
	}

	// P0 #4 P0.1-gate-extension: even when err is nil, if the typed
	// result is structurally empty (no Text, no ScriptID, no
	// cache.Hit), it's a semantic failure — the engine produced
	// NOTHING usable. Worker must surface a dispatcher error so the
	// broker does NOT mark the child SUCCEEDED with result.ok=false.
	// The structural check is the canonical scripts analog of
	// voiceover's per-child OK *bool gate; voiceover carries an
	// explicit `OK *bool` on VoiceoverChildResult, scripts carry
	// implicit "did this item ship a usable output" via the typed
	// GenerationResult fields. A future contributor may add an
	// explicit OK bool to GenerationResult; the heuristic below
	// would then collapse to `if !res.OK { … }`.
	ok := scriptItemIsSuccessful(res)
	if !ok {
		errMsg := fmt.Sprintf("script.generate_item: per-item pipeline produced structurally empty result (P0 #4 P0.1-gate-extension, ok=false)")
		h.logger.Error("script.generate_item semantic failure (P0 #1-style false-success gate EXTENDED to scripts P0 #4)",
			zap.String("job_id", j.ID),
			zap.String("item_id", item.ID),
			zap.String("result_text_len", fmt.Sprintf("%d", len(res.Output.Text))))
		pf(100, "script.generate_item semantic failure: ok=false")
		return toScriptItemResultMap(res, item.ID, j.ID, parentJobID, false, errMsg),
			fmt.Errorf("%s", errMsg)
	}

	pf(100, "script.generate_item execution complete")
	return toScriptItemResultMap(res, item.ID, j.ID, parentJobID, true, ""), nil
}

// toScriptItemResultMap serialises a per-item GenerationResult into
// the map[string]any shape the dispatcher writes into job.Result JSON.
// The aggregator reads `ok`, `status`, `item_id`, `error` to apply the
// P0.1-style false-success gate + StateMachine.Transition. The shape
// mirrors voiceover/jobs/generate_item_handler.go::toItemResultMap
// for symmetry.
//
// ok is *bool-style via the ok bool (not pointer) because the
// aggregator reads "ok" AS A TRUTHY CHECK: false → FAILED (gate),
// true → SUCCEEDED. The pointer was voiceover-specific to distinguish
// absent-as-unset from explicit false; scripts use a hardcoded bool
// because the child ALWAYS emits the field (absent would be a wire-shape
// future bug, not a deliberate signal).
func toScriptItemResultMap(res *domainScript.GenerationResult, itemID, childJobID, parentJobID string, ok bool, errStr string) map[string]any {
	m := map[string]any{
		"item_id":       itemID,
		"job_id":        childJobID,
		"parent_job_id": parentJobID,
		"ok":            ok,
		"status":        scriptItemStatus(res, ok),
	}
	if res != nil {
		// Embed the typed result so operators inspecting the DB row
		// can recover the execution data without an extra join.
		// The aggregator reads only the high-level fields (ok, status)
		// from the result map.
		if res.Title != "" {
			m["title"] = res.Title
		}
		if res.Language != "" {
			m["language"] = res.Language
		}
	}
	if errStr != "" {
		m["error"] = errStr
	}
	return m
}

// scriptItemStatus produces the canonical status string for the child
// result map. "completed" on success, "failed" on any failure. The
// aggregator's StateMachine.Transition reads this as boolean truth via
// Succeeded=(status=="completed") mapping.
func scriptItemStatus(res *domainScript.GenerationResult, ok bool) string {
	if !ok {
		return "failed"
	}
	if res == nil {
		return "failed"
	}
	if !scriptItemIsSuccessful(res) {
		return "failed"
	}
	return "completed"
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

// (the canonical status→succeeded/failed map lives in the aggregator's
// PerItemStatusToOutcome helper so the child and aggregator agree on
// the meaning — see parent_aggregator.go)

// errorIsPipe carries errors.Is probing for the typed-error contract.
func errorIsPipe(err error, target error) bool {
	if err == nil || target == nil {
		return false
	}
	return errors.Is(err, target)
}
