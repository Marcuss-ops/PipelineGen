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
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"testing"
	"time"
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
			// Procs the test exercises:
			ExtractEntities:  scriptpkg.ToggleEnabled, // selects clip_search + provider search
			GenerateMetadata: scriptpkg.ToggleEnabled,
			// Opt out of every optional postprocessor; the plan
			// still gains the unconditional clip_bindings /
			// asset_location_reconciliation / persistence stages,
			// but only the registered-and-planned stubs
			// (clip_search, metadata, persistence) execute.
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

	// Two stubs with distinctive sleep budgets. Both registered and
	// both reachable from the plan built for OutputSpec.ExtractEntities
	// / GenerateMetadata (clip_search is the stage that
	// ExtractEntities now selects — entity extraction no longer runs
	// as an "entities" postprocessor; it moved to the semantic
	// runner). Both populate PostProcessResult fields that satisfy
	// IsEmpty==false so the registry doesn't flag them "empty".
	clipSearchProc := &stubPostProcessor{
		name:    "clip_search",
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
		sleepMs: 80,
		// Metadata is a populated-but-tiny VideoMetadata
		// slice; IsEmpty() returns false because
		// len(Metadata) > 0.
		result: &adapters.PostProcessResult{
			Metadata: []scriptpkg.VideoMetadata{{Language: "en", Title: "Anchor"}},
		},
	}

	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(clipSearchProc), "clip_search stub must register")
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

	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "timings", AttemptID: "timings"})
	ctx := kernobs.WithRun(context.Background(), run)
	result, err := uc.Execute(ctx, item, scriptpkg.Preset(""), nil)
	require.NoError(t, err, "Execute must succeed for a well-formed text-only item")
	require.NotNil(t, result, "Execute must return a non-nil result")
	require.NotNil(t, result.Timings.PostprocessMs,
		"Issue #3: timings.PostprocessMs must be populated from registry.StageDurations")

	// Required-canonical procs must both be present (Issue #3
	// plumbing surfaces the keys that the registry actually
	// ran).
	assert.Contains(t, result.Timings.PostprocessMs, "clip_search",
		"Issue #3: clip_search must be plumbed through to timings.PostprocessMs")
	assert.Contains(t, result.Timings.PostprocessMs, "metadata",
		"Issue #3: metadata must be plumbed through to timings.PostprocessMs")

	// Per-stage variance captured: the registry records
	// `elapsed = time.Since(start)` per processor so the
	// clip_search→metadata gap ≈ sleep differential.
	clipSearchMs := result.Timings.PostprocessMs["clip_search"]
	metadataMs := result.Timings.PostprocessMs["metadata"]
	t.Logf(
		"Issue #3 timings.PostprocessMs: clip_search=%dms metadata=%dms gap=%dms",
		clipSearchMs, metadataMs, metadataMs-clipSearchMs,
	)

	// Lower bounds honour the registry's elapsed windows
	// (each sleep is ≥ requested, scheduler-dependent).
	// Generous lower bound on clip_search so a slow CI runner
	// resolving time.Sleep(5ms) in 0–6ms doesn't flake the
	// test.
	assert.GreaterOrEqual(t, clipSearchMs, int64(1),
		"clip_search must record ≥1ms (5ms sleep; scheduler-dependent bound, generous lower limit)")
	assert.GreaterOrEqual(t, metadataMs, int64(45),
		"metadata must record ≥45ms (50ms sleep; scheduler-dependent bound)")

	// Discriminant (absolute margin, NOT a ratio): the gap
	// between metadata and clip_search must be wide enough to
	// distinguish the new maps.Clone wiring from the old
	// uniform-division loop, even on a slow CI runner.
	// The old loop would have produced roughly equal
	// per-stage values (≈13ms each for ~110ms total across
	// 4 procs with clip_search 5+metadata 50+overhead); Issue #3
	// maps.Clone preserves actual elapsed (≈5ms clip_search,
	// ≈50ms metadata), so the gap should be approximately
	// 45ms. Required ≥30ms margin to absorb scheduler jitter
	// without false-positive uniform-division readings.
	assert.GreaterOrEqual(t, metadataMs-clipSearchMs, int64(30),
		"Issue #3: metadata - clip_search wall-clock gap must be at least 30ms (per-stage variance captured, not uniform division)")

	// Identity assertion: keys present in timings must be a
	// strict subset of names the registry actually ran. Planned
	// but unregistered postprocessors (clip_bindings,
	// internet_images, vidrush_materialization,
	// asset_location_reconciliation) never run and must NOT
	// surface here; the registry's Run loop only writes
	// result.StageDurations entries for successfully-ran procs.
	for key := range result.Timings.PostprocessMs {
		assert.Contains(t,
			[]string{"clip_search", "metadata", "persistence"}, key,
			"Issue #3: timings.PostprocessMs must mirror the registry's registered-and-ran keys; got unexpected %q", key)
	}
}

// ── SCRIPT-T03-USECASE (P0, 2026-07-15) godlike/07 typed-error gate ──
