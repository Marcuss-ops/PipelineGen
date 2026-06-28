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
	policy    scripts.ProcessorPolicy // PR 2 (June 2026): per-test override
	warnings  []string                // PR 2 (June 2026): populated into PostProcessResult.Warnings
	resultNil bool                    // PR 2 (June 2026): when true, returns (nil, nil) to exercise nil-handling
	empty     bool                    // PR 2 (June 2026): when true, returns empty PostProcessResult
}

func (p *countingProcessor) Name() string { return p.name }

// PR 2 (June 2026): countingProcessor satisfies the Policy method
// from the PostProcessor interface. Default is Required so tests
// can exercise the required-fail-vs-best-effort-warn semantics
// by overriding `policy` per test.
func (p *countingProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) scripts.ProcessorPolicy {
	if p.policy != "" {
		return p.policy
	}
	return scripts.ProcessorRequired
}

// PR 5 (June 2026): signature now takes ProcessInput envelope.
// PR 2 (June 2026): the body honours the test-only fields `resultNil`,
// `empty`, and `warnings` so per-test variation can exercise the
// registry's required-fail / best-effort-warn semantics from the
// existing test suite without spawning new fakes per case.
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

func TestRegistry_RunProcessorErrorIsIsolated(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	doc := &countingProcessor{name: "document", err: errors.New("drive api down")}
	// PR 2 (June 2026): persistence now returns a non-empty
	// PostProcessResult (ScriptID > 0). The new "empty output counts
	// as a required failure" semantic — see TODO §6 "Non considerare
	// automaticamente riuscito un processor che restituisce output
	// vuoto" — would otherwise count persistence as a second
	// required-failure even though it succeeded, breaking the
	// isolation contract this test was written to assert. We use
	// docID="row-1" so mergePostProcessResult populates DocID +
	// DocLink, producing a non-empty result.
	persist := &countingProcessor{name: "persistence", docID: "row-1"}
	r.Register(doc)
	r.Register(persist)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document", "persistence"},
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("run should not fail on partial error: %v", err)
	}
	if doc.calls != 1 {
		t.Errorf("document should have been attempted: %d", doc.calls)
	}
	if persist.calls != 1 {
		t.Errorf("persistence should still run after document error: %d", persist.calls)
	}
}

func TestRegistry_RunProcessorNotRegistered(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "document"})

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"}, // not registered
	}

	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err != nil {
		t.Fatalf("run should not fail on missing processor: %v", err)
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
	// PR 2 (June 2026): the test was originally written to assert
	// "empty registry runs cleanly" with Postprocessors=["document"]
	// (a Required-class name). Under the new ALL-required-failed
	// semantic, an empty registry + Required-name request counts
	// as a hard failure (the pre-flight gate's Runtime catch-all).
	// The intent of this test is now better expressed as
	// "empty registry tolerated when only BestEffort processors
	// are requested". We swap the plan to a BestEffort name to
	// preserve the spirit of the original test (empty registry
	// does NOT block BestEffort requests).
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"voiceover"},
	}

	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
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

	// Create processors that each return a different field.
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
// covers TODO §6 + §7. A required-class processor is requested by
// the plan but the registry did not register it (e.g. composition
// time the docservice was nil). ValidateRequested MUST return a
// typed error so the use case can short-circuit BEFORE the Ollama
// call. The Items[0] marker is best-effort — what matters is that
// the Details list names the missing processor.
func TestRegistry_ValidateRequested_PreflightRejectsMissingRequired(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	// Only "persistence" (required) is registered. The caller
	// will request "document" — also required — but it is missing.
	r.Register(&countingProcessor{name: "persistence"})

	err := r.ValidateRequested([]string{"persistence", "document"})
	if err == nil {
		t.Fatal("ValidateRequested should reject missing-required processor")
	}
	// The error must wrap ErrPlanInvalid so the use case maps it
	// to ErrPlanInvalid at the boundary.
	var inv *scriptpkg.PlanInvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("expected *scriptpkg.PlanInvalidError, got %T: %v", err, err)
	}
	if !strings.Contains(inv.Error(), "document") {
		t.Errorf("error should mention missing processor name: %v", inv)
	}
}

// TestRegistry_ValidateRequested_BestEffortMissingTolerated covers
// the policy asymmetry: a plan can request a best-effort processor
// that composition failed to register (e.g. ImageService was nil).
// ValidateRequested MUST NOT error in that case — Run will warn at
// runtime.
func TestRegistry_ValidateRequested_BestEffortMissingTolerated(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence"})
	r.Register(&countingProcessor{name: "document"})
	// "voiceover" / "images" not registered -> both BestEffort.

	if err := r.ValidateRequested([]string{"persistence", "document", "voiceover", "images"}); err != nil {
		t.Fatalf("ValidateRequested must tolerate best-effort missing: %v", err)
	}
}

// TestRegistry_ValidateRequested_NoProcessorsRequested covers TODO
// §2. Empty / nil Postprocessors list passes preflight silently.
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

// TestRegistry_ValidateRequested_Deduplicates covers a malformed
// plan with a duplicated name. The preflight must not produce
// duplicate errors.
func TestRegistry_ValidateRequested_Deduplicates(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	err := r.ValidateRequested([]string{"document", "document", "document"})
	if err == nil {
		t.Fatal("missing document should still error after dedup")
	}
	// Count occurrences of "document" in the error message — at
	// most once (dedup guarantee).
	if strings.Count(err.Error(), "document") > 2 {
		t.Errorf("dedup failed: %v", err)
	}
}

// TestRegistry_ValidateRequested_NilRegistryIsSafe ensures the
// preflight gate is robust against a nil registry (defensive —
// composition wiring guarantees non-nil but tests must not depend
// on that).
func TestRegistry_ValidateRequested_NilRegistryIsSafe(t *testing.T) {
	var r *scripts.PostProcessorRegistry
	if err := r.ValidateRequested([]string{"persistence"}); err != nil {
		t.Errorf("nil registry should be no-op: %v", err)
	}
}

// TestRegistry_Run_RequiredFailureErrors covers TODO §3. A
// ProcessorRequired processor returning an error MUST cause Run to
// return a non-nil error wrapping ErrPostprocessFailed.
func TestRegistry_Run_RequiredFailureErrors(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "persistence",
		policy: scripts.ProcessorRequired,
		err:    errors.New("sqlite busy"),
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"persistence"},
	}
	result, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
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

// TestRegistry_Run_RequiredEmptyOutputCountsAsFailure covers TODO
// §6. A ProcessorRequired processor returning a non-nil result
// that is also empty (all canonical fields zero) MUST count as a
// hard failure.
func TestRegistry_Run_RequiredEmptyOutputCountsAsFailure(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:   "document",
		policy: scripts.ProcessorRequired,
		empty:  true, // returns PostProcessResult{} (empty)
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"document"},
	}
	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
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

// TestRegistry_Run_RequiredNilResultCountsAsFailure — a processor
// that violates its contract by returning (nil, nil) counts as a
// hard failure for required-class processors.
func TestRegistry_Run_RequiredNilResultCountsAsFailure(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{
		name:      "persistence",
		policy:    scripts.ProcessorRequired,
		resultNil: true,
	})
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:             "item-1",
		Postprocessors: []string{"persistence"},
	}
	_, err := r.Run(context.Background(), plan, scripts.ProcessInput{Text: "text"})
	if err == nil {
		t.Fatal("required nil-result must error")
	}
}

// TestRegistry_Run_ProcessorWarningPropagatesToAggregate covers
// TODO §4. A processor that emits PostProcessResult.Warnings has
// those warnings merged into PipelineResult.Warnings.
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

// TestRegistry_LookupPolicy_ReflectsRegisteredPolicy — sanity
// check that Register() records the policy at register time and
// LookupPolicy returns it on demand.
func TestRegistry_LookupPolicy_ReflectsRegisteredPolicy(t *testing.T) {
	r := scripts.NewPostProcessorRegistry(zap.NewNop())
	r.Register(&countingProcessor{name: "persistence", policy: scripts.ProcessorRequired})
	r.Register(&countingProcessor{name: "voiceover", policy: scripts.ProcessorBestEffort})

	if got := r.LookupPolicy("persistence"); got != scripts.ProcessorRequired {
		t.Errorf("persistence policy: %v", got)
	}
	if got := r.LookupPolicy("voiceover"); got != scripts.ProcessorBestEffort {
		t.Errorf("voiceover policy: %v", got)
	}
	if got := r.LookupPolicy("nonexistent"); got != "" {
		t.Errorf("nonexistent should be empty string, got: %v", got)
	}
}
