// Package scripts_test — postprocessor_registry_test.go exercises
// the PostProcessorRegistry: freeze, duplicate rejection, nil
// safety, skip disabled, fail when processor unavailable, and
// per-processor error isolation.
//
// PR ref fix/required-postprocessor-semantics (June 2026):
//   - 3-policy enum (RequiredAll / RequiredAtLeastOne / BestEffort)
//     replaces the legacy two-value enum under the new semantic.
//   - RequiredAll is the canonical "any failure = hard error"
//     posture (audit #2 central call-out). The legacy
//     "all required failed → error" gate is closed to the new
//     strict-union semantic.
//   - RequiredAtLeastOne is the canonical "at least one succeeds"
//     posture (requires the legacy isolation test to migrate).
//   - BestEffort is the canonical "never blocks" posture (warning
//     folded into PipelineResult.Warnings).
package adapters_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	scripts "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Fakes ──────────────────────────────────────────────────────────

type countingProcessor struct {
	name      string
	calls     int
	docID     string
	err       error
	policy    scripts.ProcessorPolicy // per-test override (default: ProcessorRequired == RequiredAll)
	warnings  []string                // populated into PostProcessResult.Warnings
	resultNil bool                    // when true, returns (nil, nil) to exercise nil-handling
	empty     bool                    // when true, returns empty PostProcessResult
}

func (p *countingProcessor) Name() string { return p.name }

// Policy classifies this fake. Default RequiredAll via the
// ProcessorRequired alias so legacy tests that don't override policy
// see the new RequiredAll semantic (any-failure-is-hard).
func (p *countingProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) scripts.ProcessorPolicy {
	if p.policy != "" {
		return p.policy
	}
	return scripts.ProcessorRequired
}

func (p *countingProcessor) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, _ scripts.ProcessInput) (*scripts.PostProcessResult, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	if p.resultNil {
		return nil, nil
	}
	if p.empty {
		return &scripts.PostProcessResult{Warnings: p.warnings}, nil
	}
	if p.docID != "" {
		return &scripts.PostProcessResult{DocID: p.docID, DocLink: "https://docs.example.com/" + p.docID, Warnings: p.warnings}, nil
	}
	return &scripts.PostProcessResult{Warnings: p.warnings}, nil
}

// ── Registration ───────────────────────────────────────────────────

func TestRegistry_Register(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	ok := r.Register(&countingProcessor{name: "entities"})
	if !ok {
		t.Fatal("first register should succeed")
	}
	if !r.Registered("entities") {
		t.Error("entities should be registered")
	}
	if r.Len() != 1 {
		t.Errorf("len: %d", r.Len())
	}
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	if r.Register(nil) {
		t.Error("nil processor should not register")
	}
	var nilReg *scripts.PostProcessorRegistry
	if nilReg.Register(&countingProcessor{name: "x"}) {
		t.Error("nil registry should not register")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "doc"})
	ok := r.Register(&countingProcessor{name: "doc"})
	if ok {
		t.Error("duplicate registration should be rejected")
	}
	if r.Len() != 1 {
		t.Errorf("len after duplicate: %d", r.Len())
	}
}

// ── Freeze ────────────────────────────────────────────────────────

func TestRegistry_Freeze(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "entities"})
	r.Freeze()

	if !r.IsFrozen() {
		t.Error("should be frozen after Freeze()")
	}

	// Registration after freeze should fail.
	if r.Register(&countingProcessor{name: "metadata"}) {
		t.Error("register after freeze should fail")
	}
	if r.Len() != 1 {
		t.Errorf("len after freeze-register: %d", r.Len())
	}

	// Freeze is idempotent.
	r.Freeze()
	if !r.IsFrozen() {
		t.Error("should still be frozen after second Freeze()")
	}
}

func TestRegistry_FreezeNil(t *testing.T) {
	var r *scripts.PostProcessorRegistry
	r.Freeze() // must not panic
	if r.IsFrozen() {
		t.Error("nil registry should not be frozen")
	}
}

// ── Run ────────────────────────────────────────────────────────────

func TestRegistry_RunCallsEnabledProcessors(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{name: "document", docID: "doc-1"}
	persist := &countingProcessor{name: "persistence"}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Title:          "Test",
		Postprocessors: []string{"document", "persistence"},
	}

	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "Generated script text."})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if doc.calls != 1 {
		t.Errorf("document calls: %d", doc.calls)
	}
	if persist.calls != 1 {
		t.Errorf("persistence calls: %d", persist.calls)
	}
	if result.DocID != "doc-1" {
		t.Errorf("DocID: %s", result.DocID)
	}
}

func TestRegistry_RunSkipsDisabledProcessors(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{name: "document", docID: "d1"}
	persist := &countingProcessor{name: "persistence"}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document"}, // persistence NOT requested
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if doc.calls != 1 {
		t.Errorf("document should be called: got %d", doc.calls)
	}
	if persist.calls != 0 {
		t.Errorf("persistence should NOT be called: got %d", persist.calls)
	}
}

// TestRegistry_Run_RequiredAtLeastOne_ErrorIsIsolated pins the
// RequiredAtLeastOne policy's canonical fail-isolate contract:
// when ONE processor in the cohort fails AND another in the cohort
// succeeds, Run must NOT return an error (the gate fires only when
// zero of the RequiredAtLeastOne processors succeeded).
//
// PR ref fix/required-postprocessor-semantics (June 2026, audit #2):
// migrated from the legacy TestRegistry_RunProcessorErrorIsIsolated
// whose "isolated failure tolerated" semantic only made sense under
// the same RequiredAtLeastOne policy. Under RequiredAll, the
// canonical "any failure = hard error" semantic from
// TestRegistry_Run_RequiredAll_AnyFailureErrorsHard applies.
func TestRegistry_Run_RequiredAtLeastOne_ErrorIsIsolated(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{
		name:   "document",
		policy: scripts.RequiredAtLeastOne,
		err:    errors.New("drive api down"),
	}
	persist := &countingProcessor{
		name:   "persistence",
		policy: scripts.RequiredAtLeastOne,
		docID:  "row-1",
	}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document", "persistence"},
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("RequiredAtLeastOne: run should not fail when one of the cohort succeeded: %v", err)
	}
	if doc.calls != 1 {
		t.Errorf("document should have been attempted: %d", doc.calls)
	}
	if persist.calls != 1 {
		t.Errorf("persistence should still run after document error: %d", persist.calls)
	}
}

// TestRegistry_Run_RequiredAtLeastOne_AllFailedErrorsHard pins the
// complementary RequiredAtLeastOne path: zero successes across the
// RequiredAtLeastOne cohort is a hard error. RequiredAtLeastOne is
// the "tolerant" policy but only when at least one cohort member
// succeeds; if every cohort member fails, the gate fires.
func TestRegistry_Run_RequiredAtLeastOne_AllFailedErrorsHard(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "fallback_a",
		policy: scripts.RequiredAtLeastOne,
		err:    errors.New("service a down"),
	})
	r.Register(&countingProcessor{
		name:   "fallback_b",
		policy: scripts.RequiredAtLeastOne,
		err:    errors.New("service b down"),
	})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"fallback_a", "fallback_b"},
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("RequiredAtLeastOne with zero successes must produce a hard error")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Errorf("must wrap ErrPostprocessFailed: %v", err)
	}
}

// TestRegistry_Run_RequiredAll_AnyFailureErrorsHard pins the
// audit's central fix (PR ref fix/required-postprocessor-semantics,
// June 2026, audit #2): under RequiredAll policy, ANY failure of a
// RequiredAll processor in the cohort is a hard error — even when
// another RequiredAll processor succeeded. The legacy
// `if requiredRequested > 0 && requiredSucceeded == 0` gate
// would silently mark the job successful if one stubborn required
// processor succeeded despite real failures in the cohort.
func TestRegistry_Run_RequiredAll_AnyFailureErrorsHard(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{
		name:   "document",
		policy: scripts.RequiredAll,
		err:    errors.New("drive api down"),
	}
	persist := &countingProcessor{
		name:   "persistence",
		policy: scripts.RequiredAll,
		docID:  "row-1",
	}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document", "persistence"},
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("RequiredAll: any failure in the cohort must produce a hard error (audit #2)")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Errorf("must wrap ErrPostprocessFailed: %v", err)
	}
	if !strings.Contains(err.Error(), "document") {
		t.Errorf("error must name the failing processor: %v", err)
	}
}

// TestRegistry_Run_BestEffortFailureIsWarning pins the BestEffort
// canonical "never blocks" semantic: a BestEffort processor
// returning an error MUST NOT cause Run to error; the failure is
// folded into PipelineResult.Warnings.
func TestRegistry_Run_BestEffortFailureIsWarning(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "voiceover",
		policy: scripts.ProcessorBestEffort,
		err:    errors.New("edge tts down"),
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"},
	}
	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("best-effort failure must NOT error: %v", err)
	}
	if result == nil {
		t.Fatal("result must be non-nil")
	}
	if len(result.Warnings) == 0 {
		t.Fatal("best-effort failure must surface a warning")
	}
	if !strings.Contains(result.Warnings[0], "voiceover") {
		t.Errorf("warning must name the processor: %v", result.Warnings)
	}
}

// TestRegistry_Run_BestEffort_NotRegisteredTolerated covers
// audit #3 in conjunction: a BestEffort processor absent from the
// registry is tolerated (warning, no error). RequiredAll/RequiredAtLeastOne
// absent → hard error is covered by TestRegistry_Run_ProcessorNotRegistered_RequiredAllErrorsHard.
func TestRegistry_Run_BestEffort_NotRegisteredTolerated(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "document", policy: scripts.ProcessorRequired})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"}, // BestEffort, not registered
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("missing-registered BestEffort must not fail: %v", err)
	}
}

// TestRegistry_Run_ProcessorNotRegistered_RequiredAllErrorsHard
// covers audit #3 (RequiredAll missing-registered = hard fail).
// The plan requests a RequiredAll processor absent from the
// registry; under the new semantic the missing registration is a
// hard error so the operator sees the misconfig loudly.
func TestRegistry_Run_ProcessorNotRegistered_RequiredAllErrorsHard(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "document", policy: scripts.RequiredAll})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"persistence"}, // not registered, default policy = RequiredAll
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("RequiredAll missing-registered must produce a hard error (audit #3)")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Errorf("must wrap ErrPostprocessFailed: %v", err)
	}
	if !strings.Contains(err.Error(), "persistence") {
		t.Errorf("error must name the missing processor: %v", err)
	}
}

func TestRegistry_RunNilRegistry(t *testing.T) {
	var r *scripts.PostProcessorRegistry
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document"},
	}

	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Errorf("nil registry should return empty result: %v", err)
	}
	if result == nil {
		t.Fatal("nil registry should return non-nil empty result")
	}
}

func TestRegistry_RunEmptyRegistry(t *testing.T) {
	// Empty registry + BestEffort plan → tolerated via warning.
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"}, // BestEffort missing-registered
	}

	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Errorf("empty registry + best-effort plan should NOT error: %v", err)
	}
	if result == nil {
		t.Fatal("empty registry should return non-nil result")
	}
	if len(result.Warnings) == 0 {
		t.Fatal("missing-registered best-effort must surface a warning")
	}
	foundNamed := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "voiceover") {
			foundNamed = true
			break
		}
	}
	if !foundNamed {
		t.Errorf("best-effort warning should name missing processor 'voiceover'; got: %v", result.Warnings)
	}
}

func TestRegistry_RunEmptyPostprocessors(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "document"})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: nil,
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("empty postprocessors list should succeed: %v", err)
	}
}

// ── Merge ──────────────────────────────────────────────────────────

func TestRegistry_MergeAllFields(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())

	entitiesProc := &countingProcessor{name: "entities"}
	docProc := &countingProcessor{name: "document", docID: "doc-merged"}
	r.Register(entitiesProc)
	r.Register(docProc)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"entities", "document"},
	}

	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.DocID != "doc-merged" {
		t.Errorf("DocID not merged: %s", result.DocID)
	}
}

// ── PR 2: Policy / preflight behaviour ───────────────────────

// TestRegistry_ValidateRequested_PreflightRejectsMissingRequired
// covers a Required/RequiredAll processor requested by the plan
// but absent from the registry. ValidateRequested returns a typed
// *scriptpkg.PlanInvalidError with the missing processor name.
func TestRegistry_ValidateRequested_PreflightRejectsMissingRequired(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence"})

	err := r.ValidateRequested([]string{"persistence", "document"})
	if err == nil {
		t.Fatal("ValidateRequested should reject missing-required processor")
	}
	var inv *scriptpkg.PlanInvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("expected *scriptpkg.PlanInvalidError, got %T: %v", err, err)
	}
	if !strings.Contains(inv.Error(), "document") {
		t.Errorf("error should mention missing processor name: %v", inv)
	}
}

// TestRegistry_ValidateRequested_BestEffortMissingTolerated covers
// the policy asymmetry: a plan can request a BestEffort processor
// that composition failed to register (e.g. ImageService was nil).
// ValidateRequested MUST NOT error in that case.
func TestRegistry_ValidateRequested_BestEffortMissingTolerated(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence"})
	r.Register(&countingProcessor{name: "document"})

	if err := r.ValidateRequested([]string{"persistence", "document", "voiceover", "images"}); err != nil {
		t.Fatalf("ValidateRequested must tolerate best-effort missing: %v", err)
	}
}

func TestRegistry_ValidateRequested_NoProcessorsRequested(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence"})

	if err := r.ValidateRequested(nil); err != nil {
		t.Errorf("nil list should pass: %v", err)
	}
	if err := r.ValidateRequested([]string{}); err != nil {
		t.Errorf("empty list should pass: %v", err)
	}
}

func TestRegistry_ValidateRequested_Deduplicates(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	err := r.ValidateRequested([]string{"document", "document", "document"})
	if err == nil {
		t.Fatal("missing document should still error after dedup")
	}
	if strings.Count(err.Error(), "document") > 2 {
		t.Errorf("dedup failed: %v", err)
	}
}

func TestRegistry_ValidateRequested_NilRegistryIsSafe(t *testing.T) {
	var r *scripts.PostProcessorRegistry
	if err := r.ValidateRequested([]string{"persistence"}); err != nil {
		t.Errorf("nil registry should be no-op: %v", err)
	}
}

// TestRegistry_Run_RequiredAllFailureErrors pins the RequiredAll
// single-processor hard-fail contract. RequiredAll == legacy
// ProcessorRequired under the new alias.
func TestRegistry_Run_RequiredAllFailureErrors(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "persistence",
		policy: scripts.RequiredAll,
		err:    errors.New("sqlite busy"),
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"persistence"},
	}
	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("RequiredAll-fail must produce error")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Errorf("error must wrap ErrPostprocessFailed: %v", err)
	}
	if result == nil {
		t.Error("result must be non-nil (carries partial state)")
	}
}

func TestRegistry_Run_RequiredAllEmptyOutputCountsAsFailure(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "document",
		policy: scripts.RequiredAll,
		empty:  true,
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document"},
	}
	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("RequiredAll-empty must error")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Errorf("must wrap ErrPostprocessFailed: %v", err)
	}
}

func TestRegistry_Run_BestEffortEmptyOutputIsWarning(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "images",
		policy: scripts.ProcessorBestEffort,
		empty:  true,
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"images"},
	}
	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("best-effort empty must NOT error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("best-effort empty must surface a warning")
	}
}

func TestRegistry_Run_RequiredAllNilResultCountsAsFailure(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:      "persistence",
		policy:    scripts.RequiredAll,
		resultNil: true,
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"persistence"},
	}
	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("RequiredAll nil-result must error")
	}
}

func TestRegistry_Run_ProcessorWarningPropagatesToAggregate(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:     "images",
		policy:   scripts.ProcessorBestEffort,
		docID:    "img-1",
		warnings: []string{"alt text missing for scene 0"},
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"images"},
	}
	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "alt text missing") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("processor warning not propagated: %v", result.Warnings)
	}
}

func TestRegistry_LookupPolicy_ReflectsRegisteredPolicy(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence", policy: scripts.RequiredAll})
	r.Register(&countingProcessor{name: "voiceover", policy: scripts.ProcessorBestEffort})

	if got := r.LookupPolicy("persistence"); got != scripts.RequiredAll {
		t.Errorf("persistence policy: %v", got)
	}
	if got := r.LookupPolicy("voiceover"); got != scripts.ProcessorBestEffort {
		t.Errorf("voiceover policy: %v", got)
	}
	if got := r.LookupPolicy("nonexistent"); got != "" {
		t.Errorf("nonexistent should be empty string, got: %v", got)
	}
}
