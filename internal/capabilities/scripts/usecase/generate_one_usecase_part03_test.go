package usecase

import (
	"context"
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"testing"
)

// TestGenerateOneUseCase_EmitsCanonicalEvents pins the job-event
// contract: a successful Execute must emit the canonical timeline
// events in order. The test uses a text-only item so source
// resolution is a no-op and the event list is deterministic.
func TestGenerateOneUseCase_EmitsCanonicalEvents(t *testing.T) {
	t.Parallel()

	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	ppReg.Register(&stubPostProcessor{
		name:   "persistence",
		result: &adapters.PostProcessResult{Changed: true},
	})
	ppReg.Freeze()

	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil, e, ppReg, zap.NewNop(),
	)

	item := scriptpkg.GenerationItemV2{
		ID:    "event-test-item",
		Title: "Event Test",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "event test",
			SourceText: "This is a generated script with multiple sentences and narrative depth for event testing.",
		},
		ScriptParams: scriptpkg.ScriptSpec{TargetWords: 12},
		Output: scriptpkg.OutputSpec{
			SaveToDB: false,
		},
	}

	var events []struct {
		Type    string
		Message string
		Data    map[string]any
	}
	tracker := NewProgressTracker(nil, item.ID)
	tracker.SetEventFn(func(eventType, message string, data map[string]any) {
		events = append(events, struct {
			Type    string
			Message string
			Data    map[string]any
		}{Type: eventType, Message: message, Data: data})
	})

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), tracker)
	require.NoError(t, err)

	var eventTypes []string
	for _, e := range events {
		eventTypes = append(eventTypes, e.Type)
	}
	want := []string{
		"request.validated",
		"narrative.planned",
		"script.generated",
		"scenes.created",
		"stage_progress",
		"quality.checked",
		"job.completed",
	}
	require.Equal(t, want, eventTypes, "canonical event sequence must match")

	// Every event must carry the item_id so downstream observability can
	// correlate timeline entries with the generation item.
	for _, e := range events {
		require.Equal(t, item.ID, e.Data["item_id"], "event %q must carry item_id", e.Type)
	}

	// Text-only items should not emit clip-related events.
	for _, e := range events {
		require.NotContains(t, []string{"clips.hydrated", "clips.validated", "clip_evidence.built", "clips.bound"}, e.Type,
			"text-only item must not emit clip event %q", e.Type)
	}
}

// ── PR-ERROR-SURFACING commit-5 (2026-07-04): umbrella coverage ──

// errVoResolver is a VoiceoverGroupResolver stub that returns a fixed
// error — used to force Phase 4 voiceover_resolve onto the
// error path. Implements scriptports.VoiceoverGroupResolver per the
// canonical port signature (1-method interface: ResolveGroup with
// (ctx, parentID, name) → (folderID, err)).
type errVoResolver struct{ inner error }

func (r errVoResolver) ResolveGroup(
	_ context.Context, _ string, _ string,
) (string, error) {
	return "", r.inner
}

// errPostProcessor is a postprocessor stub whose Process returns a
// fixed error — used to force Phase 6 postprocess onto the error path.
// Mirrors stubPostProcessor shape but ignores sleepMs and always errors.
type errPostProcessor struct {
	name string
	err  error
}

func (s *errPostProcessor) Name() adapters.ProcessorName { return adapters.ProcessorName(s.name) }

func (s *errPostProcessor) Policy(*scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	return adapters.ProcessorRequired
}

func (s *errPostProcessor) Process(
	_ context.Context,
	_ *scriptpkg.ResolvedGenerationPlan,
	_ adapters.ProcessInput,
) (*adapters.PostProcessResult, error) {
	return nil, s.err
}

// TestGenerateOneUseCase_UmbrellaCoverage_AllPhasePaths pins
// PR-ERROR-SURFACING commit-5 (2026-07-04): every Execute phase
// failure path's error chain MUST contain scriptpkg.ErrScriptGenerationFailed
// as the umbrella sentinel reachable via errors.Is. Three sub-tests
// drive each of the 3 newly-rewrapped paths to error and assert the
// umbrella match survives the typed-struct wrap.
//
// Pre-commit-5: errors.Is(err, ErrScriptGenerationFailed) was FALSE
// for paths (A) voiceover_resolve (bare resolveVOErr escape), (B)
// engine (typed *GenerationError whose Unwrap is ErrGenerationFailed
// not ErrScriptGenerationFailed), (C) postprocess (same pattern).
//
// Post-commit-5: each path's error chain gains ErrScriptGenerationFailed
// as a top-level wrap via logPhaseError's `fmt.Errorf("%w: %w: %w",
// ErrScriptGenerationFailed, phaseSentinel, err)` — Go 1.20+ multi-%w
// chain. errors.Is(err, ErrScriptGenerationFailed) returns true
// everywhere; errors.Is(err, phaseSentinel) still true; errors.As on
// the typed struct still true.
func TestGenerateOneUseCase_UmbrellaCoverage_AllPhasePaths(t *testing.T) {
	t.Parallel()

	// ── Sub-test A: Phase 4 voiceover_resolve ──
	t.Run("voiceover_resolve", umbrellaCoverageVoiceoverResolve)

	// ── Sub-test B: Phase 5 engine ──
	// Drives engineErr via the canonical fakeOllamaGen's existing
	// `returnErr error` field (engine_test.go pumps it through
	// GenerateScript → engine.Generate surfaces it). This avoids
	// inventing helpers (PR-ERROR-SURFACING design discipline):
	// reuse the in-package fake rather than shadowing it.
	t.Run("engine", umbrellaCoverageEngine)

	// ── Sub-test C: Phase 6 postprocess ──
	t.Run("postprocess", umbrellaCoveragePostprocess)

	// ── Sub-test D: pre-construction uc=nil path ──
	// PR-ERROR-SURFACING commit-5: route uc=nil through
	// generateOnePreConstructError so the umbrella sentinel
	// ErrScriptGenerationFailed is reachable. Pre-commit-5, the
	// chain only had ErrGenerationFailed → tests could NOT match
	// ErrScriptGenerationFailed.
	t.Run("uc_nil", umbrellaCoverageUCNil)

	// ── Sub-test E: pre-construction engine=nil path ──
	// PR-ERROR-SURFACING commit-5: route engine=nil through
	// preConstructError (method on non-nil uc) so the umbrella
	// sentinel is reachable. Also preserves the existing
	// `recorded.Len() == 1 + reason="engine_nil"` contract from
	// TestGenerateOneUseCase_LogsAndReturnsTypedError_OnEngineNil.
	t.Run("engine_nil", umbrellaCoverageEngineNil)
}

func umbrellaCoverageVoiceoverResolve(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)

	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	// persistence stub — safety default (July 2026) adds
	// "persistence" to postprocessor list (Required-class),
	// must be registered to pass ValidateRequested.
	ppReg.Register(&stubPostProcessor{
		name:   "persistence",
		result: &adapters.PostProcessResult{Changed: true},
	})
	ppReg.Freeze()

	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil, e, ppReg, log,
	)
	// Wire a voGroupResolver that errors. Set VoiceoverGroup
	// on the item so ResolveVoiceoverFolderForItem does NOT
	// short-circuit on empty group (it short-circuits when
	// item.Output.VoiceoverGroup == "").
	uc.SetVoiceoverRouting(
		errVoResolver{inner: errors.New("forced vo resolve error")},
		"parent-id",
	)

	item := scriptpkg.GenerationItemV2{
		ID:    "umbrella-vo-item",
		Title: "Umbrella VO",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "umbrella vo test",
			SourceText: "text body long enough for validator — please.",
		},
		Output: scriptpkg.OutputSpec{
			VoiceoverGroup: "vo-fail-group", // non-empty → forces routing call
		},
	}

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.Error(t, err, "voiceover_resolve error path must return error")
	// NEW (post-commit-5): umbrella sentinel reachable.
	require.True(t,
		errors.Is(err, scriptpkg.ErrScriptGenerationFailed),
		"PR-ERROR-SURFACING commit-5: errors.Is(err, ErrScriptGenerationFailed) must be true for voiceover_resolve path, got err=%v", err)
	// Existing phase sentinel still matches (resolution-flavor sentinel).
	require.True(t,
		errors.Is(err, scriptpkg.ErrVoiceoverResolveFailed),
		"errors.Is(err, ErrVoiceoverResolveFailed) must remain true for voiceover_resolve path (PR-ERROR-SURFACING commit-5: phase sentinel updated from ErrSourceResolutionFailed to ErrVoiceoverResolveFailed — voiceover folder-routing is a distinct failure domain from clip-search resolution per godlike/06 SSOT)")
	// Inner error preserved.
	require.ErrorContains(t, err, "forced vo resolve error",
		"inner resolver error must be in the chain")
	// Diagnostic log emitted by logPhaseError.
	require.Equal(t, 1, recorded.Len(),
		"exactly 1 Warn log entry must be emitted by logPhaseError")
	fields := recorded.All()[0].ContextMap()
	assert.Equal(t, "voiceover_resolve", fields["phase"])
	assert.Equal(t, "umbrella-vo-item", fields["item_id"])
}
