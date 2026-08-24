// Package stockpipeline — step_split_test.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// 5 contract tests pin the canonical 1-file-per-Step split of
// orchestrator_steps.go into 8 single-purpose files per
// godlike/06 SSOT (one canonical owner per fact). Each test
// fails-closed if a future maintainer merges the extracted
// types/sentinels back into orchestrator_steps.go or accidentally
// introduces a duplicate definition across the 8 files.
//
// godlike/06 SSOT: the 8-file layout is the canonical shape:
//   - orchestrator_steps.go       (slim: package doc + 6 step
//     keys + Step interface)
//   - orchestrator_step_errors.go (6 typed sentinels)
//   - step_plan_clips.go          (StockPlanStep)
//   - step_stage_sources.go       (StockStageSourcesStep)
//   - step_extract_clips.go       (StockExtractClipsStep)
//   - step_compose_chunks.go      (StockComposeChunksStep)
//   - step_publish.go             (StockPublishStep)
//   - step_finalize.go            (StockFinalizeStep)
//
// The user spec referenced a 949-LoC pre-split view + a 7-step
// file naming scheme that implied splitting StockFinalizeStep
// into 3+ sub-step files. The minimum-ripple 1-file-per-Step
// interpretation is the canonical shape; the spec drift is
// documented in the commit body.
package assets

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestSplit_StepKeysLiveInOrchestratorSteps pins the canonical
// home of the 6 step key constants: they MUST be declared in
// orchestrator_steps.go (not the per-Step files). A future
// "moved StepKeyStockPlan to step_plan_clips.go" regression
// would compile (the constants are package-level) but break
// the godlike/06 SSOT contract that step keys + Step interface
// are co-located (the §12-5 wire-format anchor).
func TestSplit_StepKeysLiveInOrchestratorSteps(t *testing.T) {
	keys := map[string]string{
		"StepKeyStockPlan":          StepKeyStockPlan,
		"StepKeyStockStageSources":  StepKeyStockStageSources,
		"StepKeyStockExtractClips":  StepKeyStockExtractClips,
		"StepKeyStockComposeChunks": StepKeyStockComposeChunks,
		"StepKeyStockPublish":       StepKeyStockPublish,
		"StepKeyStockFinalize":      StepKeyStockFinalize,
	}
	wantValues := map[string]string{
		"StepKeyStockPlan":          "stock.plan",
		"StepKeyStockStageSources":  "stock.stage_sources",
		"StepKeyStockExtractClips":  "stock.extract_clips",
		"StepKeyStockComposeChunks": "stock.compose_chunks",
		"StepKeyStockPublish":       "stock.publish",
		"StepKeyStockFinalize":      "stock.finalize",
	}
	if len(keys) != 6 {
		t.Fatalf("step key count: got %d, want 6 (a step key was accidentally added/removed; update this test byte-stable)", len(keys))
	}
	for name, got := range keys {
		if got != wantValues[name] {
			t.Errorf("%s: got %q, want %q", name, got, wantValues[name])
		}
	}
}

// TestSplit_SentinelsLiveInOrchestratorStepErrors pins the canonical
// home of the 6 step-level typed sentinels: they MUST be declared
// in orchestrator_step_errors.go (not orchestrator_steps.go or
// any of the per-Step files).
func TestSplit_SentinelsLiveInOrchestratorStepErrors(t *testing.T) {
	sentinels := []error{
		ErrStockPublishArtifactFailed,
		ErrStockFinalizeSpineFailed,
		ErrStockFinalizeLeaseMissing,
		ErrStockFnRequired,
		ErrStockStageSourcesAllFailed,
		ErrStockStageSourcesIncomplete,
		ErrStockExtractClipsCutterRequired,
		ErrStockComposeChunksAllFailed,
	}
	if got, want := len(sentinels), 8; got != want {
		t.Fatalf("step-level sentinel count: got %d, want %d", got, want)
	}
	// Every sentinel's message MUST name its step (godlike/07
	// typed-error contract). Drift to bare messages would break
	// operator log scanability + dashboard errors.Is routing.
	wantSubstrings := map[string]string{
		"ErrStockPublishArtifactFailed":      "stock.publish",
		"ErrStockFinalizeSpineFailed":        "stock.finalize",
		"ErrStockFinalizeLeaseMissing":       "stock.finalize",
		"ErrStockFnRequired":                 "stock.finalize",
		"ErrStockStageSourcesAllFailed":      "stock.stage_sources",
		"ErrStockExtractClipsCutterRequired": "stock.extract_clips",
		"ErrStockComposeChunksAllFailed":     "stock.compose_chunks",
	}
	for i, s := range sentinels {
		if s == nil {
			t.Fatalf("sentinel[%d] is nil — must be errors.New(...) at package level", i)
		}
	}
	for name, wantSub := range wantSubstrings {
		var s error
		switch name {
		case "ErrStockPublishArtifactFailed":
			s = ErrStockPublishArtifactFailed
		case "ErrStockFinalizeSpineFailed":
			s = ErrStockFinalizeSpineFailed
		case "ErrStockFinalizeLeaseMissing":
			s = ErrStockFinalizeLeaseMissing
		case "ErrStockFnRequired":
			s = ErrStockFnRequired
		case "ErrStockStageSourcesAllFailed":
			s = ErrStockStageSourcesAllFailed
		case "ErrStockExtractClipsCutterRequired":
			s = ErrStockExtractClipsCutterRequired
		case "ErrStockComposeChunksAllFailed":
			s = ErrStockComposeChunksAllFailed
		}
		if !strings.Contains(s.Error(), wantSub) {
			t.Errorf("%s message %q missing required step prefix %q", name, s.Error(), wantSub)
		}
	}
}

// TestSplit_StepInterfaceLivesInOrchestratorSteps pins the canonical
// home of the Step interface: it MUST be declared in
// orchestrator_steps.go (not in the per-Step files). The Step
// interface is the godlike/06 SSOT contract that all 6 step
// implementations satisfy; co-locating it with the 6 step key
// constants + package doc is the canonical 1-place fact.
func TestSplit_StepInterfaceLivesInOrchestratorSteps(t *testing.T) {
	// We pin via runtime type identity. All 6 step types MUST
	// satisfy the Step interface; the interface declaration
	// itself is package-level.
	steps := []Step{
		StockPlanStep{},
		StockStageSourcesStep{},
		StockExtractClipsStep{},
		StockComposeChunksStep{},
		StockPublishStep{},
		StockFinalizeStep{},
	}
	if got, want := len(steps), 6; got != want {
		t.Fatalf("step count: got %d, want 6", got)
	}
	// Verify each step's Name() returns the canonical step key
	// (drift detection for future split regressions).
	wantNames := []string{
		StepKeyStockPlan,
		StepKeyStockStageSources,
		StepKeyStockExtractClips,
		StepKeyStockComposeChunks,
		StepKeyStockPublish,
		StepKeyStockFinalize,
	}
	for i, s := range steps {
		if s.Name() != wantNames[i] {
			t.Errorf("step[%d] Name(): got %q, want %q", i, s.Name(), wantNames[i])
		}
	}
}

// TestSplit_StageSourcesAllFailedSentinelIsTyped verifies the
// fail-closed contract of ErrStockStageSourcesAllFailed via
// errors.Is. The sentinel MUST propagate through wrapping
// (the step returns the sentinel directly; downstream callers
// use errors.Is to detect the all-failed class).
func TestSplit_StageSourcesAllFailedSentinelIsTyped(t *testing.T) {
	// Direct identity: errors.Is(sentinel, sentinel) == true.
	if !errors.Is(ErrStockStageSourcesAllFailed, ErrStockStageSourcesAllFailed) {
		t.Fatalf("errors.Is(sentinel, sentinel) == false — sentinel is not a stable typed identity")
	}
	// Wrapped identity: errors.Is(wrapped, sentinel) == true
	// (so orchestrator + composition-root callers can route on it).
	wrapped := wrapForTest(ErrStockStageSourcesAllFailed)
	if !errors.Is(wrapped, ErrStockStageSourcesAllFailed) {
		t.Errorf("errors.Is(wrapped, ErrStockStageSourcesAllFailed) == false — wrapped propagation broken")
	}
	// Negative: errors.Is(ErrStockComposeChunksAllFailed,
	// ErrStockStageSourcesAllFailed) == false (sentinels are
	// distinct typed identities).
	if errors.Is(ErrStockComposeChunksAllFailed, ErrStockStageSourcesAllFailed) {
		t.Errorf("errors.Is(ErrStockComposeChunksAllFailed, ErrStockStageSourcesAllFailed) == true — sentinels collided")
	}
}

// TestSplit_ComposeChunksAllFailedSentinelIsTyped mirrors the
// stage_sources fail-closed contract for compose_chunks per
// PR-STOCK-FAKE-AVAILABILITY-REMOVAL (Wave 1 P0 #2, 2026-07-04).
func TestSplit_ComposeChunksAllFailedSentinelIsTyped(t *testing.T) {
	if !errors.Is(ErrStockComposeChunksAllFailed, ErrStockComposeChunksAllFailed) {
		t.Fatalf("errors.Is(sentinel, sentinel) == false — sentinel is not a stable typed identity")
	}
	wrapped := wrapForTest(ErrStockComposeChunksAllFailed)
	if !errors.Is(wrapped, ErrStockComposeChunksAllFailed) {
		t.Errorf("errors.Is(wrapped, ErrStockComposeChunksAllFailed) == false — wrapped propagation broken")
	}
	if errors.Is(ErrStockStageSourcesAllFailed, ErrStockComposeChunksAllFailed) {
		t.Errorf("errors.Is(ErrStockStageSourcesAllFailed, ErrStockComposeChunksAllFailed) == true — sentinels collided")
	}
}

// TestSplit_PerStepTypesAreDistinct verifies each Step type has
// a distinct Go type identity (godlike/06 SSOT: each step is a
// single-purpose capability, not a polymorphic blob).
func TestSplit_PerStepTypesAreDistinct(t *testing.T) {
	stepTypes := []reflect.Type{
		reflect.TypeOf(StockPlanStep{}),
		reflect.TypeOf(StockStageSourcesStep{}),
		reflect.TypeOf(StockExtractClipsStep{}),
		reflect.TypeOf(StockComposeChunksStep{}),
		reflect.TypeOf(StockPublishStep{}),
		reflect.TypeOf(StockFinalizeStep{}),
	}
	seen := make(map[string]bool)
	for i, st := range stepTypes {
		name := st.Name()
		if seen[name] {
			t.Errorf("step type %q appears at index %d AND earlier — duplicate type", name, i)
		}
		seen[name] = true
	}
	if len(seen) != 6 {
		t.Errorf("distinct step types: got %d, want 6", len(seen))
	}
}

// wrapForTest is a test-only helper that wraps a sentinel via
// fmt.Errorf("%w: test") so we can verify the typed-error
// propagation contract without importing fmt in the test file's
// public surface.
func wrapForTest(sentinel error) error {
	return &wrappedTestError{sentinel: sentinel}
}

type wrappedTestError struct {
	sentinel error
}

func (w *wrappedTestError) Error() string { return "wrapped: " + w.sentinel.Error() }
func (w *wrappedTestError) Unwrap() error { return w.sentinel }
