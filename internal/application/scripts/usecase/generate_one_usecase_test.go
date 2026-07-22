// Package scripts — generate_one_usecase_test.go (June 2026,
// Issue #3) pins the per-stage timings wiring:
// GenerateOneUseCase.Execute must mirror the registry's
// PipelineResult.StageDurations verbatim into
// GenerationTimings.PostprocessMs so downstream consumers (job
// dashboards, response shape) read the registry's authoritative
// per-processor wall-clock timing.
//
// Strategy: build a real Engine with stubbed ollama + nil
// memory gate (in-package test helpers from engine_test.go),
// a real PostProcessorRegistry, register two stub processors at
// canonical Required-class names ("entities" 5ms + "metadata"
// 50ms) — both flagged Required per `defaultPolicyByName`, so
// they're guaranteed to land in plan.Postprocessors whenever
// the caller sets `OutputSpec.ExtractEntities=true` (which
// BuildPlan / buildPostprocessorList consults). The two procs
// sleep deliberately different durations so the captured
// per-stage variance proves Issue #3 plumbing (not the prior
// uniform-division approximation).
package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// stubPostProcessor implements adapters.PostProcessor for tests.
// Returns a canned result after `sleepMs` so the registry's
// per-stage elapsed timestamp is observable and distinguishable
// across processors. `name` matches a canonical postprocessor
// name so buildPostprocessorList emits it without any test-side
// plan override.
//
// Policy always returns BestEffort so the registry's
// ValidateRequested + Run gate treats a downstream failure as a
// warning rather than aborting Execute. The test only cares
// about the timing plumbing; processor-policy gates are out of
// scope.
type stubPostProcessor struct {
	name    string
	sleepMs int
	result  *adapters.PostProcessResult
}

func (s *stubPostProcessor) Name() adapters.ProcessorName { return adapters.ProcessorName(s.name) }

func (s *stubPostProcessor) Policy(*scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	return adapters.ProcessorBestEffort
}

func (s *stubPostProcessor) Process(
	_ context.Context,
	_ *scriptpkg.ResolvedGenerationPlan,
	_ adapters.ProcessInput,
) (*adapters.PostProcessResult, error) {
	if s.sleepMs > 0 {
		time.Sleep(time.Duration(s.sleepMs) * time.Millisecond)
	}
	return s.result, nil
}

// itemForTimingsTest builds a minimal text-only GenerationItemV2
// that:
//   - emits SourceSpec.Type=SourceText so source-resolution is
//     a no-op (no clip-search / Qdrant / drive calls)
//   - opts-in to ExtractEntities + GenerateMetadata so
//     buildPostprocessorList emits "entities" + "metadata" in
//     plan.Postprocessors (both Required-class per
//     `defaultPolicyByName` and registered in the test's
//     registry — the canonical two procs whose per-stage
//     variance the assertion inspects).
//   - SaveToDB is a safety default (July 2026) — the test
//     explicitly sets it false so NormalizeItem overrides it
//     to true; persistence is registered in the test registry to
//     satisfy ValidateRequested.
//   - leaves VoiceoverFolderID empty so ResolveVoiceoverFolderForItem
//     short-circuits before touching anything
//
// SourceSpec.Topic is populated so the model's prompts carry an
// anchor; the validator reads Topic + SourceText + Title through
// ResolveGenerationPlan downstream.
func itemForTimingsTest() scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID:       "iss3-timings-item",
		Title:    "Stage Durations Plumbing",
		Language: "en",
		Tone:     "neutral",
		Style:    "standard",
		Model:    "llama3:8b",
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: "Stage durations plumbing",
			// SourceText length is well above any sensible
			// validator minimum so ValidateItem succeeds even
			// if the validator enforces one.
			SourceText: "This is a generated script with multiple sentences and narrative depth. A canonical test about per-stage postprocessor duration plumbing.",
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords: 12,
		},
		Output: scriptpkg.OutputSpec{
			// Required-class procs the test exercises:
			ExtractEntities:  scriptpkg.ToggleEnabled,
			GenerateMetadata: scriptpkg.ToggleEnabled,
			// Opt-out of every other postprocessor so
			// plan.Postprocessors stays to: entities, metadata,
			// clip_bindings, visual_planning, persistence —
			// (persistence is an unconditional best-effort per
			// buildPostprocessorList and missing-registered
			// in this test, surfacing as a warning only).
			SaveToDB: false,
		},
	}
}

// TestGenerateOneUseCase_TimingsPostprocessMsClonesStageDurations
// pins Issue #3: after Execute, the per-stage timing map in
// result.Timings.PostprocessMs MUST equal — key-for-key — the
// registry's PipelineResult.StageDurations (the canonical
// authoritative measurement). We assert per-stage variance (an
// absolute gap, not a ratio) to distinguish the new wiring from
// the uniform-division loop it replaced.
func TestGenerateOneUseCase_TimingsPostprocessMsClonesStageDurations(t *testing.T) {
	t.Parallel()

	// Engine: stubbed ollama generator (canonical V1 fixture
	// from engine_test.go's defaultFakeResult).
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil) // nil memory gate → fresh path

	// Two Required-class stubs with distinctive sleep budgets.
	// Both registered, both reachable via buildPostprocessorList
	// when OutputSpec.ExtractEntities / GenerateMetadata are
	// true, both populate PostProcessResult fields that satisfy
	// IsEmpty==false so the registry doesn't flag them "empty".
	entitiesProc := &stubPostProcessor{
		name:    "entities",
		sleepMs: 5,
		// Changed=true is the cheapest way to signal
		// "non-empty" without depending on the typed
		// extraction shape (EntityResult element type is
		// out of scope for this test). The registry's
		// IsEmpty() short-circuits on Changed before
		// inspecting the typed fields.
		result: &adapters.PostProcessResult{
			Changed: true,
		},
	}
	metadataProc := &stubPostProcessor{
		name:    "metadata",
		sleepMs: 50,
		// Metadata is a populated-but-tiny VideoMetadata
		// slice; IsEmpty() returns false because
		// len(Metadata) > 0.
		result: &adapters.PostProcessResult{
			Metadata: []scriptpkg.VideoMetadata{{Language: "en", Title: "Anchor"}},
		},
	}

	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(entitiesProc), "entities stub must register")
	require.True(t, ppReg.Register(metadataProc), "metadata stub must register")
	// persistence stub — safety default (July 2026) adds
	// "persistence" to postprocessor list (Required-class),
	// must be registered to pass ValidateRequested.
	persistenceProc := &stubPostProcessor{
		name:   "persistence",
		result: &adapters.PostProcessResult{Changed: true},
	}
	require.True(t, ppReg.Register(persistenceProc), "persistence stub must register")
	ppReg.Freeze()

	// Use case wired with the stubbed engine + my registry.
	// Source registry is nil so source-resolution is a no-op
	// (text-only SourceSpec doesn't need it).
	// Preset is the canonical string alias — empty string
	// means "no preset override" so NormalizeItem leaves the
	// item's existing flags intact.
	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil, // SourceRegistry — text-only plan path skips it
		e,
		ppReg,
		zap.NewNop(),
	)

	item := itemForTimingsTest()

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err, "Execute must succeed for a well-formed text-only item")
	require.NotNil(t, result, "Execute must return a non-nil result")
	require.NotNil(t, result.Timings.PostprocessMs,
		"Issue #3: timings.PostprocessMs must be populated from registry.StageDurations")

	// Required-canonical procs must both be present (Issue #3
	// plumbing surfaces the keys that the registry actually
	// ran).
	assert.Contains(t, result.Timings.PostprocessMs, "entities",
		"Issue #3: entities must be plumbed through to timings.PostprocessMs")
	assert.Contains(t, result.Timings.PostprocessMs, "metadata",
		"Issue #3: metadata must be plumbed through to timings.PostprocessMs")

	// Per-stage variance captured: the registry records
	// `elapsed = time.Since(start)` per processor so the
	// entities→metadata gap ≈ sleep differential.
	entitiesMs := result.Timings.PostprocessMs["entities"]
	metadataMs := result.Timings.PostprocessMs["metadata"]
	t.Logf(
		"Issue #3 timings.PostprocessMs: entities=%dms metadata=%dms gap=%dms",
		entitiesMs, metadataMs, metadataMs-entitiesMs,
	)

	// Lower bounds honour the registry's elapsed windows
	// (each sleep is ≥ requested, scheduler-dependent).
	// Generous lower bound on entities so a slow CI runner
	// resolving time.Sleep(5ms) in 0–6ms doesn't flake the
	// test.
	assert.GreaterOrEqual(t, entitiesMs, int64(1),
		"entities must record ≥1ms (5ms sleep; scheduler-dependent bound, generous lower limit)")
	assert.GreaterOrEqual(t, metadataMs, int64(45),
		"metadata must record ≥45ms (50ms sleep; scheduler-dependent bound)")

	// Discriminant (absolute margin, NOT a ratio): the gap
	// between metadata and entities must be wide enough to
	// distinguish the new maps.Clone wiring from the old
	// uniform-division loop, even on a slow CI runner.
	// The old loop would have produced roughly equal
	// per-stage values (≈13ms each for ~110ms total across
	// 4 procs with entities 5+metadata 50+overhead); Issue #3
	// maps.Clone preserves actual elapsed (≈5ms entities,
	// ≈50ms metadata), so the gap should be approximately
	// 45ms. Required ≥30ms margin to absorb scheduler jitter
	// without false-positive uniform-division readings.
	assert.Greater(t, metadataMs-entitiesMs, int64(30),
		"Issue #3: metadata - entities wall-clock gap must exceed 30ms (per-stage variance captured, not uniform division)")

	// Identity assertion: keys present in timings must be a
	// strict subset of names the registry actually ran. The
	// best-effort unconditional postprocessors
	// (clip_bindings + visual_planning) are NOT registered
	// here; the registry's Run loop only writes
	// result.StageDurations entries for successfully-ran
	// procs, so timings.PostprocessMs MUST NOT include any
	// unregistered name.
	for key := range result.Timings.PostprocessMs {
		assert.Contains(t,
			[]string{"entities", "metadata", "persistence"}, key,
			"Issue #3: timings.PostprocessMs must mirror the registry's registered-and-ran keys; got unexpected %q", key)
	}
}

// ── SCRIPT-T03-USECASE (P0, 2026-07-15) godlike/07 typed-error gate ──

// TestGenerateOneUseCase_LogsAndReturnsTypedError_OnEngineNil pins the
// SCRIPT-T03-USECASE (P0, 2026-07-15) godlike/07 typed-error gate:
// when the usecase hits a phase failure, it MUST log the diagnostic
// context (reason/phase, error) at the boundary AND return a typed
// error that satisfies errors.Is for the canonical sentinel.
//
// Strategy: use observer.New(zap.WarnLevel) to capture log entries,
// construct a usecase with engine=nil (triggers the
// "engine not configured" constructor failure), execute a real item,
// and assert:
//
//	(a) errors.Is(err, scriptpkg.ErrGenerationFailed) is true
//	(b) exactly 1 Warn log entry was emitted with reason="engine_nil"
//	    + the typed error chain
//
// godlike/07 NO_FAKE_AVAILABILITY: the typed error is the canonical
// propagation surface; the log is the canonical diagnostic surface.
// Both surfaces are asserted in this test.
func TestGenerateOneUseCase_LogsAndReturnsTypedError_OnEngineNil(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)

	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil, // SourceRegistry nil
		nil, // Engine nil → triggers ErrGenerationFailed (typed sentinel)
		nil, // ppReg nil
		log,
	)

	item := scriptpkg.GenerationItemV2{
		ID:    "script-t03-usecase-test",
		Title: "SCRIPT-T03-USECASE test",
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: "test",
		},
	}

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.Error(t, err, "Execute with engine=nil must return error")
	require.True(t, errors.Is(err, scriptpkg.ErrGenerationFailed),
		"godlike/07 typed-error gate: errors.Is(err, ErrGenerationFailed) must be true, got err=%v", err)

	require.Equal(t, 1, recorded.Len(),
		"SCRIPT-T03-USECASE: exactly 1 Warn log entry must be emitted (constructor-failure path)")
	entry := recorded.All()[0]
	assert.Equal(t, "generate-one: construction failed", entry.Message,
		"SCRIPT-T03-USECASE: log message must indicate construction failure")
	fields := entry.ContextMap()
	assert.Equal(t, "engine_nil", fields["reason"],
		"SCRIPT-T03-USECASE: log must include the reason 'engine_nil' for the construction failure")
}

// TestGenerateOneUseCase_LogsAndReturnsTypedError_OnValidateFailure
// pins the godlike/07 typed-error gate for a real phase failure
// (validate, not just constructor). The validate phase runs AFTER
// the constructor checks, so the item_id is populated in the log.
//
// Strategy: construct a usecase with a real Engine (stubbed ollama)
// + SourceRegistry nil + nil ppReg. Pass an item with an empty
// Source (no Type set) which trips ValidateItem. Assert:
//
//	(a) errors.Is(err, scriptpkg.ErrPlanInvalid) is true (typed)
//	(b) exactly 1 Warn log entry was emitted with item_id +
//	    phase="validate" + the typed error
func TestGenerateOneUseCase_LogsAndReturnsTypedError_OnValidateFailure(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)

	// Real Engine with stubbed ollama (no memory gate) — won't be
	// invoked because ValidateItem short-circuits BEFORE engine.
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil, // SourceRegistry nil
		e,
		nil, // ppReg nil
		log,
	)

	// Item with empty Source: Type="" — ValidateItem will return
	// an error (the canonical "no source specified" path).
	item := scriptpkg.GenerationItemV2{
		ID:     "validate-fail-item",
		Title:  "Validate Fail Test",
		Source: scriptpkg.SourceSpec{
			// No Type set → ValidateItem rejects.
		},
	}

	_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.Error(t, err, "Execute with empty Source must return error")
	require.True(t, errors.Is(err, scriptpkg.ErrPlanInvalid),
		"godlike/07 typed-error gate: errors.Is(err, ErrPlanInvalid) must be true, got err=%v", err)

	require.Equal(t, 1, recorded.Len(),
		"SCRIPT-T03-USECASE: exactly 1 Warn log entry must be emitted (validate phase)")
	entry := recorded.All()[0]
	assert.Equal(t, "generate-one: phase failed", entry.Message,
		"SCRIPT-T03-USECASE: log message must indicate phase failure")
	fields := entry.ContextMap()
	assert.Equal(t, "validate-fail-item", fields["item_id"],
		"SCRIPT-T03-USECASE: log must include the item_id")
	assert.Equal(t, "validate", fields["phase"],
		"SCRIPT-T03-USECASE: log must include the phase 'validate'")
}

// ── Clip evidence text support (P1) ──

func TestEnforceClipEvidenceTextSupport_AllowedWithinBudget(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "one two three four five",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 10000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	if err := enforceClipEvidenceTextSupport(plan, cfg); err != nil {
		t.Fatalf("expected no error for source_text within clip evidence budget, got %v", err)
	}
}

func TestEnforceClipEvidenceTextSupport_ExceedsBudget(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "one two three four five six seven eight nine ten eleven twelve",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 2000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	err := enforceClipEvidenceTextSupport(plan, cfg)
	require.Error(t, err)
	var pve *scriptpkg.PayloadValidationError
	require.ErrorAs(t, err, &pve)
	assert.Equal(t, "SOURCE_TEXT_EXCEEDS_CLIP_EVIDENCE", pve.Code)
	assert.Equal(t, 12, pve.Extra.ActualWords)
	assert.Equal(t, 5, pve.Extra.MaxWords)
	assert.Equal(t, 2.0, pve.Extra.EvidenceSeconds)
	assert.Equal(t, 2.5, pve.Extra.WordsPerSecond)
}

func TestEnforceClipEvidenceTextSupport_DisabledWhenZero(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "one two three four five six seven eight nine ten eleven twelve",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 1000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 0}
	require.NoError(t, enforceClipEvidenceTextSupport(plan, cfg))
}

func TestEnforceClipEvidenceTextSupport_SumsMultipleClips(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "one two three four five six seven eight nine ten",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 2000},
				"clip-2": {StartMs: 0, EndMs: 2000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	if err := enforceClipEvidenceTextSupport(plan, cfg); err != nil {
		t.Fatalf("expected no error when total clip duration supports source_text, got %v", err)
	}
}

func TestEnforceClipEvidenceTextSupport_IgnoresTextSource(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceText),
		SourceText: "one two three four five six seven eight nine ten eleven twelve",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 1000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	require.NoError(t, enforceClipEvidenceTextSupport(plan, cfg))
}

func TestEnforceClipEvidenceTextSupport_NoSourceText(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		SourceKind: string(scriptpkg.SourceClips),
		SourceText: "",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipDetails: map[string]scriptpkg.ClipDetail{
				"clip-1": {StartMs: 0, EndMs: 1000},
			},
		},
	}
	cfg := adapters.NormalizationConfig{WordsPerSecondClipEvidence: 2.5}
	require.NoError(t, enforceClipEvidenceTextSupport(plan, cfg))
}

// ── Event emission coverage (July 2026) ──

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
	t.Run("voiceover_resolve", func(t *testing.T) {
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
	})

	// ── Sub-test B: Phase 5 engine ──
	// Drives engineErr via the canonical fakeOllamaGen's existing
	// `returnErr error` field (engine_test.go pumps it through
	// GenerateScript → engine.Generate surfaces it). This avoids
	// inventing helpers (PR-ERROR-SURFACING design discipline):
	// reuse the in-package fake rather than shadowing it.
	t.Run("engine", func(t *testing.T) {
		t.Parallel()
		core, recorded := observer.New(zap.WarnLevel)
		log := zap.New(core)

		gen := &fakeOllamaGen{returnErr: errors.New("forced engine error")}
		e := buildTestEngine(gen, nil)

		ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
		// persistence stub (see voiceover_resolve comment).
		ppReg.Register(&stubPostProcessor{
			name:   "persistence",
			result: &adapters.PostProcessResult{Changed: true},
		})
		ppReg.Freeze()

		uc := NewGenerateOneUseCase(
			adapters.NormalizationConfig{},
			nil, e, ppReg, log,
		)

		item := scriptpkg.GenerationItemV2{
			ID:    "umbrella-engine-item",
			Title: "Umbrella Engine",
			Source: scriptpkg.SourceSpec{
				Type:       scriptpkg.SourceText,
				Topic:      "umbrella engine test",
				SourceText: "text body long enough for validator — please.",
			},
		}

		_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
		require.Error(t, err, "engine error path must return error")
		// NEW (post-commit-5): umbrella sentinel reachable.
		require.True(t,
			errors.Is(err, scriptpkg.ErrScriptGenerationFailed),
			"PR-ERROR-SURFACING commit-5: errors.Is(err, ErrScriptGenerationFailed) must be true for engine path, got err=%v", err)
		// Existing phase sentinel still matches.
		require.True(t,
			errors.Is(err, scriptpkg.ErrGenerationFailed),
			"errors.Is(err, ErrGenerationFailed) must remain true for engine path")
		// Typed struct still recoverable (canonical V1 contract).
		var genErr *scriptpkg.GenerationError
		require.True(t,
			errors.As(err, &genErr),
			"errors.As(err, &GenerationError{}) must remain true for engine path")
		require.Equal(t, "umbrella-engine-item", genErr.ItemID)
		require.Equal(t, "engine", genErr.Phase)
		// Inner error preserved.
		require.ErrorContains(t, err, "forced engine error",
			"inner engine error must be in the chain")
		// Diagnostic log emitted by logPhaseError.
		require.Equal(t, 1, recorded.Len())
		fields := recorded.All()[0].ContextMap()
		assert.Equal(t, "engine", fields["phase"])
		assert.Equal(t, "umbrella-engine-item", fields["item_id"])
	})

	// ── Sub-test C: Phase 6 postprocess ──
	t.Run("postprocess", func(t *testing.T) {
		t.Parallel()
		core, recorded := observer.New(zap.WarnLevel)
		log := zap.New(core)

		gen := &fakeOllamaGen{}
		e := buildTestEngine(gen, nil)

		// Register a Required-class postprocessor that errors.
		ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
		ppErrProc := &errPostProcessor{
			name: "entities",
			err:  errors.New("forced postprocess error"),
		}
		require.True(t, ppReg.Register(ppErrProc))
		// persistence stub (see voiceover_resolve comment).
		ppReg.Register(&stubPostProcessor{
			name:   "persistence",
			result: &adapters.PostProcessResult{Changed: true},
		})
		ppReg.Freeze()

		uc := NewGenerateOneUseCase(
			adapters.NormalizationConfig{},
			nil, e, ppReg, log,
		)

		item := scriptpkg.GenerationItemV2{
			ID:    "umbrella-pp-item",
			Title: "Umbrella PP",
			Source: scriptpkg.SourceSpec{
				Type:       scriptpkg.SourceText,
				Topic:      "umbrella pp test",
				SourceText: "text body long enough for validator — please.",
			},
			Output: scriptpkg.OutputSpec{
				ExtractEntities: scriptpkg.ToggleEnabled, // forces "entities" into plan.Postprocessors
			},
		}

		_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
		require.Error(t, err, "postprocess error path must return error")
		// NEW (post-commit-5): umbrella sentinel reachable.
		require.True(t,
			errors.Is(err, scriptpkg.ErrScriptGenerationFailed),
			"PR-ERROR-SURFACING commit-5: errors.Is(err, ErrScriptGenerationFailed) must be true for postprocess path, got err=%v", err)
		// Existing phase sentinel still matches.
		require.True(t,
			errors.Is(err, scriptpkg.ErrPostprocessFailed),
			"errors.Is(err, ErrPostprocessFailed) must remain true for postprocess path")
		// Typed struct still recoverable (canonical V1 contract).
		var ppErrStruct *scriptpkg.PostprocessError
		require.True(t,
			errors.As(err, &ppErrStruct),
			"errors.As(err, &PostprocessError{}) must remain true for postprocess path")
		require.Equal(t, "umbrella-pp-item", ppErrStruct.ItemID)
		require.Equal(t, "registry", ppErrStruct.Processor)
		// Inner error preserved.
		require.ErrorContains(t, err, "forced postprocess error",
			"inner postprocess error must be in the chain")
		// Diagnostic log emitted by logPhaseError.
		require.Equal(t, 1, recorded.Len())
		fields := recorded.All()[0].ContextMap()
		assert.Equal(t, "postprocess", fields["phase"])
		assert.Equal(t, "umbrella-pp-item", fields["item_id"])
	})

	// ── Sub-test D: pre-construction uc=nil path ──
	// PR-ERROR-SURFACING commit-5: route uc=nil through
	// generateOnePreConstructError so the umbrella sentinel
	// ErrScriptGenerationFailed is reachable. Pre-commit-5, the
	// chain only had ErrGenerationFailed → tests could NOT match
	// ErrScriptGenerationFailed.
	t.Run("uc_nil", func(t *testing.T) {
		t.Parallel()
		// Typed-nil pointer — Go method dispatch sees the nil receiver
		// and the FIRST line of Execute (`if uc == nil`) returns before
		// any dereference. No panic.
		var nilUC *GenerateOneUseCase
		require.Nil(t, nilUC) // nil pointer receiver — Execute's first line returns via 'if uc == nil' before any dereference
		item := scriptpkg.GenerationItemV2{ID: "umbrella-ucnil-item"}
		_, err := nilUC.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
		require.Error(t, err, "uc=nil path must return error")
		// NEW (post-commit-5): umbrella sentinel reachable.
		require.True(t,
			errors.Is(err, scriptpkg.ErrScriptGenerationFailed),
			"PR-ERROR-SURFACING commit-5: errors.Is(err, ErrScriptGenerationFailed) must be true for uc=nil path, got err=%v", err)
		// Pre-existing phase sentinel still matches.
		require.True(t,
			errors.Is(err, scriptpkg.ErrGenerationFailed),
			"errors.Is(err, ErrGenerationFailed) must remain true for uc=nil path (pre-commit-5 contract)")
		// Inner error preserved.
		require.ErrorContains(t, err, "use case not constructed",
			"inner 'use case not constructed' must be in the chain")
	})

	// ── Sub-test E: pre-construction engine=nil path ──
	// PR-ERROR-SURFACING commit-5: route engine=nil through
	// preConstructError (method on non-nil uc) so the umbrella
	// sentinel is reachable. Also preserves the existing
	// `recorded.Len() == 1 + reason="engine_nil"` contract from
	// TestGenerateOneUseCase_LogsAndReturnsTypedError_OnEngineNil.
	t.Run("engine_nil", func(t *testing.T) {
		t.Parallel()
		core, recorded := observer.New(zap.WarnLevel)
		log := zap.New(core)

		uc := NewGenerateOneUseCase(
			adapters.NormalizationConfig{},
			nil, // SourceRegistry nil
			nil, // Engine nil → triggers ErrGenerationFailed (typed sentinel)
			nil, // ppReg nil
			log,
		)

		item := scriptpkg.GenerationItemV2{ID: "umbrella-engnil-item"}
		_, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
		require.Error(t, err, "engine=nil path must return error")
		// NEW (post-commit-5): umbrella sentinel reachable.
		require.True(t,
			errors.Is(err, scriptpkg.ErrScriptGenerationFailed),
			"PR-ERROR-SURFACING commit-5: errors.Is(err, ErrScriptGenerationFailed) must be true for engine=nil path, got err=%v", err)
		// Pre-existing phase sentinel still matches (no regression).
		require.True(t,
			errors.Is(err, scriptpkg.ErrGenerationFailed),
			"errors.Is(err, ErrGenerationFailed) must remain true for engine=nil path (pre-commit-5 contract)")
		// Pre-existing log-shape contract preserved (one Warn entry with
		// message="generate-one: construction failed", reason="engine_nil").
		require.Equal(t, 1, recorded.Len())
		entry := recorded.All()[0]
		assert.Equal(t, "generate-one: construction failed", entry.Message)
		fields := entry.ContextMap()
		assert.Equal(t, "engine_nil", fields["reason"])
	})
}
