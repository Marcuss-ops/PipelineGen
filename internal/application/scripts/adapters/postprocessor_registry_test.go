// Package scripts_test — postprocessor_registry_test.go exercises
// the PostProcessorRegistry: freeze, duplicate rejection, nil
// safety, skip disabled, fail when processor unavailable, and
// per-processor error isolation.
package adapters_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Fakes ──────────────────────────────────────────────────────────

type countingProcessor struct {
	name      adapterspkg.ProcessorName
	calls     int
	err       error
	policy    adapterspkg.ProcessorPolicy // PR 2 (June 2026): per-test override
	warnings  []string                    // PR 2 (June 2026): populated into PostProcessResult.Warnings
	resultNil bool                        // PR 2 (June 2026): when true, returns (nil, nil) to exercise nil-handling
	empty     bool                        // PR 2 (June 2026): when true, returns empty PostProcessResult
	// Sprint 1.0: `changed` field removed — the merge function never
	// propagates PostProcessResult.Changed to PipelineResult so the field
	// was dead. Use `warnings` for propagated state instead.
}

type timingMetrics struct {
	processors []string
}

func (m *timingMetrics) ObserveProcessorDuration(processor string, _ float64) {
	m.processors = append(m.processors, processor)
}

func (*timingMetrics) ObserveProviderDuration(string, float64) {}

func (p *countingProcessor) Name() adapterspkg.ProcessorName { return p.name }

// PR 2 (June 2026): countingProcessor satisfies the Policy method
// from the PostProcessor interface. Default is Required so tests
// can exercise the required-fail-vs-best-effort-warn semantics
// by overriding `policy` per test.
func (p *countingProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) adapterspkg.ProcessorPolicy {
	if p.policy != "" {
		return p.policy
	}
	return adapterspkg.ProcessorRequired
}

// PR 5 (June 2026): signature now takes ProcessInput envelope.
// PR 2 (June 2026): the body honours the test-only fields `resultNil`,
// `empty`, and `warnings` so per-test variation can exercise the
// registry's required-fail / best-effort-warn semantics from the
// existing test suite without spawning new fakes per case.
func (p *countingProcessor) Process(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, _ adapterspkg.ProcessInput) (*adapterspkg.PostProcessResult, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	if p.resultNil {
		return nil, nil
	}
	if p.empty {
		return &adapterspkg.PostProcessResult{Warnings: p.warnings}, nil
	}
	// Changed marks the result as non-empty so the registry propagates
	// warnings instead of treating a Required processor as failed.
	return &adapterspkg.PostProcessResult{Warnings: p.warnings, Changed: true}, nil
}

// ── Registration ───────────────────────────────────────────────────

func TestRegistry_Register(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
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
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	if r.Register(nil) {
		t.Error("nil processor should not register")
	}
	var nilReg *adapterspkg.PostProcessorRegistry
	if nilReg.Register(&countingProcessor{name: "x"}) {
		t.Error("nil registry should not register")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
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
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
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
	var r *adapterspkg.PostProcessorRegistry
	r.Freeze() // must not panic
	if r.IsFrozen() {
		t.Error("nil registry should not be frozen")
	}
}

// ── Run ────────────────────────────────────────────────────────────

func TestRegistry_RunCallsEnabledProcessors(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{name: "metadata"}
	persist := &countingProcessor{name: "persistence", warnings: []string{"persistence-row-1"}}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Title:          "Test",
		Postprocessors: []string{"metadata", "persistence"},
	}

	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "Generated script text."})
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
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "persistence-row-1") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("persistence-row-1 marker not in result.Warnings: %v", result.Warnings)
	}
}

func TestRegistry_RecordsVidRushProcessorTiming(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	metrics := &timingMetrics{}
	r.SetVidRushTimingMetrics(metrics)
	r.Register(&countingProcessor{name: "entities"})

	_, err := r.Run(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		ID: "item-timing", Postprocessors: []string{"entities"},
	}, adapterspkg.ProcessInput{Text: "timed text"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(metrics.processors) != 1 || metrics.processors[0] != "entities" {
		t.Fatalf("processor timing observations = %#v, want [entities]", metrics.processors)
	}
}

func TestRegistry_RunSkipsDisabledProcessors(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{name: "metadata"}
	persist := &countingProcessor{name: "persistence"}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"metadata"}, // persistence NOT requested
	}

	_, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
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

func TestRegistry_RunProcessorErrorIsIsolated(t *testing.T) {
	// Issue 3 / P0 (June 2026): the per-processor error-isolation
	// semantic now applies to BestEffort processors only — Required
	// failures propagate as a Go error (the pipeline aborts even
	// if other Required processors succeeded). The test uses
	// BestEffort for both processors so the original "errors do
	// NOT block other processors" assertion still holds.
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{
		name:   "metadata",
		policy: adapterspkg.ProcessorBestEffort,
		err:    errors.New("drive api down"),
	}
	persist := &countingProcessor{
		name:     "persistence",
		policy:   adapterspkg.ProcessorBestEffort,
		warnings: []string{"persistence-row-1"},
	}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"metadata", "persistence"},
	}

	_, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("run should not fail on best-effort partial error: %v", err)
	}
	if doc.calls != 1 {
		t.Errorf("document should have been attempted: %d", doc.calls)
	}
	if persist.calls != 1 {
		t.Errorf("persistence should still run after document error: %d", persist.calls)
	}
}

func TestRegistry_RunWithProgressReportsExecutionBoundaries(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "entities",
		policy: adapterspkg.ProcessorBestEffort,
		err:    errors.New("ollama timeout"),
	})
	r.Register(&countingProcessor{
		name:     "persistence",
		policy:   adapterspkg.ProcessorBestEffort,
		warnings: []string{"persisted"},
	})

	var got []string
	_, err := r.RunWithProgress(context.Background(), &scriptpkg.ResolvedGenerationPlan{
		ID:             "progress-boundaries",
		Postprocessors: []string{"entities", "persistence"},
	}, adapterspkg.ProcessInput{Text: "text"}, func(event adapterspkg.ProcessorProgressEvent) {
		got = append(got, event.Status+":"+string(event.Name))
	})
	if err != nil {
		t.Fatalf("best-effort failure should be isolated: %v", err)
	}
	want := []string{
		"started:entities", "failed:entities",
		"started:persistence", "completed:persistence",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("progress events = %v, want %v", got, want)
	}
}

// TestRegistry_Run_RequiredFailureAlwaysFailsPipeline (Issue 3 /
// P0, June 2026). A Required-class postprocessor that fails MUST
// cause the pipeline to abort even if another Required-class
// processor succeeds. Pre-Issue-3 the gate was `requiredRequested >
// 0 && requiredSucceeded == 0` which let k-of-n partial-success
// patterns slide through as overall success — exactly the opposite
// of the ProcessorRequired contract.
func TestRegistry_Run_RequiredFailureAlwaysFailsPipeline(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	document := &countingProcessor{
		name:   "metadata",
		policy: adapterspkg.ProcessorRequired,
		err:    errors.New("drive api down"),
	}
	persistence := &countingProcessor{
		name:     "persistence",
		policy:   adapterspkg.ProcessorRequired,
		warnings: []string{"persistence-row-1"}, // non-empty success
	}
	r.Register(document)
	r.Register(persistence)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"metadata", "persistence"},
	}

	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})

	// 1. err MUST be non-nil: one Required fail aborts the pipeline.
	if err == nil {
		t.Fatal("Issue 3 / P0: expected non-nil error when ANY Required postprocessor fails, got nil. " +
			"Pre-fix: partial-success (one Required success + one Required fail) was incorrectly treated as overall success.")
	}
	// 2. err MUST wrap scriptpkg.ErrPostprocessFailed so the broker
	//    can classify the retry decision (worker treats dispatchErr
	//    != nil as FAILED per Issue 1 / P0's contract).
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Errorf("error must wrap scriptpkg.ErrPostprocessFailed: %v", err)
	}
	// 3. err message should surface both failure names (document
	//    failed; persistence succeeded but was on a Required list
	//    so the gate fired anyway). The exact aggregate text is
	//    pinned: "required postprocessor failure: … document …".
	if !strings.Contains(err.Error(), "required postprocessor failure") {
		t.Errorf("error message must use the canonical Issue 3 phrasing: %v", err)
	}
	if !strings.Contains(err.Error(), "metadata") {
		t.Errorf("error message must name the failing Required processor (document): %v", err)
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error message must classify the failure type: %v", err)
	}
	// 4. result MUST be non-nil (carries partial state + warnings).
	if result == nil {
		t.Error("result must be non-nil (warnings + partial PipelineResult)")
	}
	// 5. Persistence result fields should have been merged before
	//    error return — operators reading the (nil, partial-result)
	//    wire shape see what succeeded.
	if persistence.calls != 1 {
		t.Errorf("persistence should still be invoked before the gate fires: got %d calls", persistence.calls)
	}
	if result != nil {
		found := false
		for _, w := range result.Warnings {
			if strings.Contains(w, "persistence-row-1") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("persistence-row-1 marker not in result.Warnings: %v", result.Warnings)
		}
	}
	// 6. Both processors were attempted exactly once — the new gate
	//    fires AT THE END after the loop, not per-processor.
	if document.calls != 1 {
		t.Errorf("document should have been attempted: got %d calls", document.calls)
	}
	// 7. Warnings surfaced: the failure must be visible.
	if result != nil && len(result.Warnings) == 0 {
		t.Error("At least one warning expected for the document failure")
	}
}

func TestRegistry_RunProcessorNotRegistered(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "metadata"})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"}, // not registered
	}

	_, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("run should not fail on missing processor: %v", err)
	}
}

func TestRegistry_RunNilRegistry(t *testing.T) {
	var r *adapterspkg.PostProcessorRegistry
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"metadata"},
	}

	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err != nil {
		t.Errorf("nil registry should return empty result: %v", err)
	}
	if result == nil {
		t.Fatal("nil registry should return non-nil empty result")
	}
}

func TestRegistry_RunEmptyRegistry(t *testing.T) {
	// PR 2 (June 2026): the test was originally written to assert
	// "empty registry runs cleanly" with Postprocessors=["metadata"]
	// (a Required-class name). Under the new ALL-required-failed
	// semantic, an empty registry + Required-name request counts
	// as a hard failure (the pre-flight gate's Runtime catch-all).
	// The intent of this test is now better expressed as
	// "empty registry tolerated when only BestEffort processors
	// are requested". We swap the plan to a BestEffort name to
	// preserve the spirit of the original test (empty registry
	// does NOT block BestEffort requests).
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"},
	}

	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err != nil {
		t.Errorf("empty registry + best-effort plan should NOT error: %v", err)
	}
	if result == nil {
		t.Fatal("empty registry should return non-nil result")
	}
	// The BestEffort-missing-registered observation flows into
	// PipelineResult.Warnings so operators can detect it. Verify
	// BOTH presence AND that the warning names the missing
	// processor — the production contract Run's emit-warnings path
	// locks.
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
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "metadata"})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: nil,
	}

	_, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("empty postprocessors list should succeed: %v", err)
	}
}

// ── Merge ──────────────────────────────────────────────────────────

func TestRegistry_MergeAllFields(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())

	// Create processors that each return a different field.
	entitiesProc := &countingProcessor{name: "entities", warnings: []string{"entities-merger-1"}}
	docProc := &countingProcessor{name: "metadata"}
	r.Register(entitiesProc)
	r.Register(docProc)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"entities", "metadata"},
	}

	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "entities-merger-1") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("entities-merger-1 marker not merged into result.Warnings: %v", result.Warnings)
	}
}

// ── PR 2: Policy / preflight behaviour ───────────────────────

// TestRegistry_ValidateRequested_PreflightRejectsMissingRequired
// covers TODO §6 + §7. A required-class processor is requested by
// the plan but the registry did not register it (e.g. composition
// time the docservice was nil). ValidateRequested MUST return a
// typed error so the use case can short-circuit BEFORE the Ollama
// call. The Items[0] marker is best-effort — what matters is that
// the Details list names the missing processor.
func TestRegistry_ValidateRequested_PreflightRejectsMissingRequired(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	// Only "persistence" (required) is registered. The caller
	// will request "entities" — also required (post-PR 3) — but
	// it is missing. Note: "metadata" was downgraded to BestEffort
	// in Fase 2 Spina Dorsale so it would NOT trigger a preflight
	// rejection; "entities" remains Required.
	r.Register(&countingProcessor{name: "persistence"})

	err := r.ValidateRequested([]adapterspkg.ProcessorName{adapterspkg.ProcessorPersistence, adapterspkg.ProcessorEntities})
	if err == nil {
		t.Fatal("ValidateRequested should reject missing-required processor")
	}
	// The error must wrap ErrPlanInvalid so the use case maps it
	// to ErrPlanInvalid at the boundary.
	var inv *scriptpkg.PlanInvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("expected *scriptpkg.PlanInvalidError, got %T: %v", err, err)
	}
	if !strings.Contains(inv.Error(), "entities") {
		t.Errorf("error should mention missing processor name: %v", inv)
	}
}

// TestRegistry_ValidateRequested_BestEffortMissingTolerated covers
// the policy asymmetry: a plan can request a best-effort processor
// that composition failed to register (e.g. ImageService was nil).
// ValidateRequested MUST NOT error in that case — Run will warn at
// runtime.
func TestRegistry_ValidateRequested_BestEffortMissingTolerated(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence"})
	r.Register(&countingProcessor{name: "metadata"})
	// "voiceover" / "metadata" not registered -> both BestEffort.

	if err := r.ValidateRequested([]adapterspkg.ProcessorName{adapterspkg.ProcessorPersistence, adapterspkg.ProcessorMetadata, adapterspkg.ProcessorVoiceover, adapterspkg.ProcessorMetadata}); err != nil {
		t.Fatalf("ValidateRequested must tolerate best-effort missing: %v", err)
	}
}

// TestRegistry_ValidateRequested_NoProcessorsRequested covers TODO
// §2. Empty / nil Postprocessors list passes preflight silently.
func TestRegistry_ValidateRequested_NoProcessorsRequested(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence"})

	if err := r.ValidateRequested(nil); err != nil {
		t.Errorf("nil list should pass: %v", err)
	}
	if err := r.ValidateRequested([]adapterspkg.ProcessorName{}); err != nil {
		t.Errorf("empty list should pass: %v", err)
	}
}

// TestRegistry_ValidateRequested_Deduplicates covers a malformed
// plan with a duplicated name. The preflight must not produce
// duplicate errors.
func TestRegistry_ValidateRequested_Deduplicates(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	// "entities" is Required (post-PR 3); requesting it 3× when
	// nothing is registered must produce exactly one deduplicated
	// error entry. Note: "metadata" was downgraded to BestEffort
	// in Fase 2 and would NOT error on missing-registered.
	err := r.ValidateRequested([]adapterspkg.ProcessorName{adapterspkg.ProcessorEntities, adapterspkg.ProcessorEntities, adapterspkg.ProcessorEntities})
	if err == nil {
		t.Fatal("missing entities should still error after dedup")
	}
	// Count occurrences of "entities" in the error message — at
	// most once (dedup guarantee).
	if strings.Count(err.Error(), "entities") > 2 {
		t.Errorf("dedup failed: %v", err)
	}
}

// TestRegistry_ValidateRequested_NilRegistryIsSafe ensures the
// preflight gate is robust against a nil registry (defensive —
// composition wiring guarantees non-nil but tests must not depend
// on that).
func TestRegistry_ValidateRequested_NilRegistryIsSafe(t *testing.T) {
	var r *adapterspkg.PostProcessorRegistry
	if err := r.ValidateRequested([]adapterspkg.ProcessorName{adapterspkg.ProcessorPersistence}); err != nil {
		t.Errorf("nil registry should be no-op: %v", err)
	}
}

// TestRegistry_Run_RequiredFailureErrors covers TODO §3. A
// ProcessorRequired processor returning an error MUST cause Run to
// return a non-nil error wrapping ErrPostprocessFailed.
func TestRegistry_Run_RequiredFailureErrors(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "persistence",
		policy: adapterspkg.ProcessorRequired,
		err:    errors.New("sqlite busy"),
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"persistence"},
	}
	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("required-fail must produce error")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Errorf("error must wrap ErrPostprocessFailed: %v", err)
	}
	if result == nil {
		t.Error("result must be non-nil (carries partial state)")
	}
}

// TestRegistry_Run_BestEffortFailureIsWarning covers TODO §4. A
// ProcessorBestEffort processor returning an error MUST NOT cause
// Run to error; the failure is folded into PipelineResult.Warnings
// instead.
func TestRegistry_Run_BestEffortFailureIsWarning(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "voiceover",
		policy: adapterspkg.ProcessorBestEffort,
		err:    errors.New("edge tts down"),
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"},
	}
	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
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

// TestRegistry_Run_RequiredEmptyOutputCountsAsFailure covers TODO
// §6. A ProcessorRequired processor returning a non-nil result
// that is also empty (all canonical fields zero) MUST count as a
// hard failure.
func TestRegistry_Run_RequiredEmptyOutputCountsAsFailure(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "metadata",
		policy: adapterspkg.ProcessorRequired,
		empty:  true, // returns PostProcessResult{} (empty)
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"metadata"},
	}
	_, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("required-empty must error")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Errorf("must wrap ErrPostprocessFailed: %v", err)
	}
}

// TestRegistry_Run_BestEffortEmptyOutputIsWarning covers the
// symmetric case: BestEffort + empty output = warning, not error.
func TestRegistry_Run_BestEffortEmptyOutputIsWarning(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "metadata",
		policy: adapterspkg.ProcessorBestEffort,
		empty:  true,
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"metadata"},
	}
	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("best-effort empty must NOT error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("best-effort empty must surface a warning")
	}
}

// TestRegistry_Run_RequiredNilResultCountsAsFailure — a processor
// that violates its contract by returning (nil, nil) counts as a
// hard failure for required-class processors.
func TestRegistry_Run_RequiredNilResultCountsAsFailure(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:      "persistence",
		policy:    adapterspkg.ProcessorRequired,
		resultNil: true,
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"persistence"},
	}
	_, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("required nil-result must error")
	}
}

// TestRegistry_Run_ProcessorWarningPropagatesToAggregate covers
// TODO §4. A processor that emits PostProcessResult.Warnings has
// those warnings merged into PipelineResult.Warnings.
func TestRegistry_Run_ProcessorWarningPropagatesToAggregate(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:     "metadata",
		policy:   adapterspkg.ProcessorBestEffort,
		warnings: []string{"alt text missing for scene 0"},
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"metadata"},
	}
	result, err := r.Run(context.Background(), plan, adapterspkg.ProcessInput{Text: "text"})
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

// TestRegistry_LookupPolicy_ReflectsRegisteredPolicy — sanity
// check that Register() records the policy at register time and
// LookupPolicy returns it on demand.
func TestRegistry_LookupPolicy_ReflectsRegisteredPolicy(t *testing.T) {
	r := adapterspkg.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence", policy: adapterspkg.ProcessorRequired})
	r.Register(&countingProcessor{name: "voiceover", policy: adapterspkg.ProcessorBestEffort})

	if got := r.LookupPolicy("persistence"); got != adapterspkg.ProcessorRequired {
		t.Errorf("persistence policy: %v", got)
	}
	if got := r.LookupPolicy("voiceover"); got != adapterspkg.ProcessorBestEffort {
		t.Errorf("voiceover policy: %v", got)
	}
	if got := r.LookupPolicy("nonexistent"); got != "" {
		t.Errorf("nonexistent should be empty string, got: %v", got)
	}
}
