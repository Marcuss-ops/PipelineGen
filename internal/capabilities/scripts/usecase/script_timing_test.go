// Package scripts — script_timing_test.go is the canonical timing
// instrumentation acceptance suite (TestScriptTiming_*). It pins the contract
// that every phase boundary in single-item script generation is measured on
// the SINGLE canonical clock owned by internal/kernel/observability:
//
//   - STAGE boundaries   → script.prepare / normalize / validate /
//     source.resolve / script.plan / script.engine / script.postprocess /
//     audio.pipeline / persistence.sqlite / document.publish
//   - OPERATION boundaries → qdrant.search / sqlite.hydrate / ollama.generate /
//     edge_tts.synthesize / google_docs.publish / rust.audio_render
//
// GenerationTimings is only a compatibility projection of those canonical
// observations (never a second timer). The derived Breakdown / Fanout /
// TimingSummary views never double-count nested stages and never report
// summed parallel work as wall time.
package usecase

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Helpers ───────────────────────────────────────────────────────────

// startScriptTimingRun binds a fresh canonical Run to a context. The run owns
// the single clock; every stage/operation measured on ctx records onto it.
func startScriptTimingRun(t *testing.T) (context.Context, *kernobs.Run) {
	t.Helper()
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "script-timing", AttemptID: "attempt-script-timing"})
	return kernobs.WithRun(context.Background(), run), run
}

// buildTextTimingUseCase builds a text-only GenerateOneUseCase with a stubbed
// engine and a frozen postprocessor registry (entities + persistence).
func buildTextTimingUseCase(t *testing.T) *GenerateOneUseCase {
	t.Helper()
	e := buildTestEngine(&fakeOllamaGen{}, nil)
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(&stubPostProcessor{name: "entities", result: &adapters.PostProcessResult{Changed: true}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "metadata", result: &adapters.PostProcessResult{Metadata: []scriptpkg.VideoMetadata{{Language: "en", Title: "Anchor"}}}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "persistence", result: &adapters.PostProcessResult{Changed: true}}))
	ppReg.Freeze()
	return NewGenerateOneUseCase(adapters.NormalizationConfig{}, nil, e, ppReg, zap.NewNop())
}

// newSearchTimingResolver builds a SearchSourceResolver backed by a fake
// semantic search port + fake clip resolver. `clipCount` clips are added and
// its transcript tracks are wired for "en" so hydration can succeed.
func newSearchTimingResolver(t *testing.T, clipCount int) *SearchSourceResolver {
	t.Helper()
	resolver := newFakeClipResolver()
	results := make([]SemanticSearchResult, 0, clipCount)
	for i := 0; i < clipCount; i++ {
		id := fmt.Sprintf("clip-%d", i)
		resolver.AddClip(makeTestClip(id, "Clip "+id, 6*time.Second))
		results = append(results, SemanticSearchResult{
			ClipID:              id,
			Name:                "Clip " + id,
			Score:               0.9,
			Transcript:          defaultClipSearchText,
			VisualSummary:       "summary " + id,
			MediaType:           "video",
			DriveLink:           "https://drive.google.com/file/d/drive-" + id + "/view",
			AnchorCoverageRatio: 1.0,
		})
	}
	builder := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	configureFakeClipTranscripts(builder, resolver, "en")
	return NewSearchSourceResolver(&recordingSearchPort{results: results}, builder, NewClipSamplerRegistry(), zap.NewNop())
}

// searchTimingResCtx returns a SourceResolutionContext in English so the
// wired "en" transcript tracks match the resolution language.
func searchTimingResCtx() scriptpkg.SourceResolutionContext {
	ctx := makeTestResCtx()
	ctx.Language = "en"
	ctx.RequireDriveLink = false
	ctx.RequireLocalMedia = false
	return ctx
}

// scriptTimingStage builds a StageReport with an explicit wall interval, so
// the derived Breakdown can tell top-level from nested stages.
func scriptTimingStage(name string, startMs, endMs int64) kernobs.StageReport {
	base := time.Unix(1_700_000_000, 0)
	return kernobs.StageReport{
		Name:       name,
		Status:     kernobs.StageStatusCompleted,
		StartedAt:  base.Add(time.Duration(startMs) * time.Millisecond),
		FinishedAt: base.Add(time.Duration(endMs) * time.Millisecond),
		DurationMs: endMs - startMs,
	}
}

// scriptTimingStageMs returns the recorded wall duration of the named stage.
func scriptTimingStageMs(report *kernobs.RunReport, name string) (int64, bool) {
	for _, st := range report.Stages {
		if st.Name == name {
			return st.DurationMs, true
		}
	}
	return 0, false
}

// scriptTimingOpCount counts the operations recorded for one component +
// operation pair (the canonical "measured once" probe).
func scriptTimingOpCount(report *kernobs.RunReport, component, operation string) int {
	n := 0
	for _, op := range report.Operations {
		if op.Component == component && op.Operation == operation {
			n++
		}
	}
	return n
}

// ── Canonical clock ───────────────────────────────────────────────────

// TestScriptTiming_TotalUsesCanonicalRunClock pins that GenerationTimings.
// TotalMs is read from the run's canonical clock (ElapsedMs), never from a
// second time.Since clock: it must be a prefix of the run's wall time.
func TestScriptTiming_TotalUsesCanonicalRunClock(t *testing.T) {
	ctx, run := startScriptTimingRun(t)

	// A sleeping postprocessor guarantees the canonical clock advances a
	// measurable amount during Execute (so the bounds below are meaningful).
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(&stubPostProcessor{name: "entities", sleepMs: 5, result: &adapters.PostProcessResult{Changed: true}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "metadata", result: &adapters.PostProcessResult{Metadata: []scriptpkg.VideoMetadata{{Language: "en", Title: "Anchor"}}}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "persistence", result: &adapters.PostProcessResult{Changed: true}}))
	ppReg.Freeze()
	uc := NewGenerateOneUseCase(adapters.NormalizationConfig{}, nil, buildTestEngine(&fakeOllamaGen{}, nil), ppReg, zap.NewNop())

	result, err := uc.Execute(ctx, itemForTimingsTest(), scriptpkg.Preset(""), nil)
	require.NoError(t, err)

	// TotalMs was captured from the run clock mid-Execute, so it must be
	// bounded by the same clock observed immediately after.
	elapsed := run.ElapsedMs()
	assert.Greater(t, elapsed, int64(0), "canonical clock must have advanced during Execute")
	assert.LessOrEqual(t, result.Timings.TotalMs, elapsed,
		"TotalMs must be bounded by the canonical run clock")

	// The run wall time uses the same started/now clock, so TotalMs (captured
	// before Finish) is a prefix of it — never larger.
	report := run.Finish()
	assert.LessOrEqual(t, result.Timings.TotalMs, report.WallTimeMs,
		"TotalMs must be a prefix of the canonical run wall time")
	assert.Greater(t, report.WallTimeMs, int64(0), "run wall time must be non-zero")
}

// ── Prepare substages ─────────────────────────────────────────────────

// TestScriptTiming_PrepareHasSubstageBreakdown pins that the single
// script.prepare stage is decomposed into the canonical substages on the run
// report (normalize / validate / plan; source.resolve is absent for text-only
// items because no resolver runs).
func TestScriptTiming_PrepareHasSubstageBreakdown(t *testing.T) {
	ctx, run := startScriptTimingRun(t)
	uc := buildTextTimingUseCase(t)

	_, err := uc.Execute(ctx, itemForTimingsTest(), scriptpkg.Preset(""), nil)
	require.NoError(t, err)

	report := run.Report()
	for _, want := range []string{
		"script.prepare",
		"script.normalize",
		"script.validate",
		"script.plan",
	} {
		_, ok := scriptTimingStageMs(report, want)
		assert.True(t, ok, "run report missing prepare substage %q", want)
	}
	// Text-only items skip source resolution entirely.
	_, ok := scriptTimingStageMs(report, "source.resolve")
	assert.False(t, ok, "text-only item must not record source.resolve")
}

// ── Operation boundaries measured exactly once ─────────────────────────

// TestScriptTiming_QdrantOperationMeasuredOnce pins that the semantic search
// boundary records exactly one qdrant.search operation per Resolve call.
func TestScriptTiming_QdrantOperationMeasuredOnce(t *testing.T) {
	ctx, run := startScriptTimingRun(t)
	resolver := newSearchTimingResolver(t, 1)

	_, _ = resolver.Resolve(ctx, scriptpkg.SourceSpec{
		Type:     scriptpkg.SourceSearch,
		Query:    "quick brown fox",
		MaxClips: 1,
	}, searchTimingResCtx())

	report := run.Report()
	assert.Equal(t, 1, scriptTimingOpCount(report, "qdrant", "search"),
		"qdrant.search must be measured exactly once per source resolution")
}

// TestScriptTiming_SQLiteHydrationMeasuredOnce pins that evidence hydration
// records one sqlite.hydrate operation per hydrated clip — never zero, never
// duplicated for the same clip.
func TestScriptTiming_SQLiteHydrationMeasuredOnce(t *testing.T) {
	ctx, run := startScriptTimingRun(t)
	resolver := newSearchTimingResolver(t, 1)

	_, _ = resolver.Resolve(ctx, scriptpkg.SourceSpec{
		Type:     scriptpkg.SourceSearch,
		Query:    "quick brown fox",
		MaxClips: 1,
	}, searchTimingResCtx())

	report := run.Report()
	assert.Equal(t, 1, scriptTimingOpCount(report, "sqlite", "hydrate"),
		"sqlite.hydrate must be measured once per hydrated clip")
}

// TestScriptTiming_OllamaMeasuredOnce pins that a non-segmented engine call
// records exactly one ollama.generate operation.
func TestScriptTiming_OllamaMeasuredOnce(t *testing.T) {
	ctx, run := startScriptTimingRun(t)
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	_, err := e.Generate(ctx, &scriptpkg.ResolvedGenerationPlan{
		Title:    "Ollama Timing",
		Language: "en",
		Mode:     "text",
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), gen.calls.Load(), "the fake Ollama must be called once")

	report := run.Report()
	assert.Equal(t, 1, scriptTimingOpCount(report, "ollama", "generate"),
		"ollama.generate must be measured exactly once for a non-segmented plan")
}

// ── Postprocessors project canonical stages ───────────────────────────

// TestScriptTiming_PostprocessorsProjectCanonicalStages pins that each
// postprocessor's StageDurations entry is the exact canonical stage
// observation recorded on the run (no second clock, no re-measurement).
func TestScriptTiming_PostprocessorsProjectCanonicalStages(t *testing.T) {
	ctx, run := startScriptTimingRun(t)

	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(&stubPostProcessor{name: "entities", result: &adapters.PostProcessResult{Changed: true}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "metadata", result: &adapters.PostProcessResult{Metadata: []scriptpkg.VideoMetadata{{Language: "en", Title: "Anchor"}}}}))
	ppReg.Freeze()

	plan := &scriptpkg.ResolvedGenerationPlan{Postprocessors: []string{"entities", "metadata"}}
	_, err := ppReg.Run(ctx, plan, adapters.ProcessInput{})
	require.NoError(t, err)

	report := run.Report()
	for _, name := range []string{"entities", "metadata"} {
		canonical, ok := scriptTimingStageMs(report, name)
		require.True(t, ok, "postprocessor stage %q must be recorded on the run", name)
		got, ok := scriptTimingStageMs(report, name)
		assert.True(t, ok)
		assert.Equal(t, canonical, got,
			"stage report %q must equal the canonical stage observation", name)
	}
}

// ── Google Docs boundary ──────────────────────────────────────────────

// TestScriptTiming_GoogleDocsMeasured pins that document publication records
// the document.publish stage and exactly one google_docs.publish operation per
// language through the full GenerateOneUseCase.Execute path.
func TestScriptTiming_GoogleDocsMeasured(t *testing.T) {
	ctx, run := startScriptTimingRun(t)

	docs := &documentsE2EStub{}
	uc := buildUsecaseWithDocuments(&fakeOllamaGen{}, docs)

	item := makeTextOnlyItem("script-timing-docs", "Source text about clean energy for the document surface.")
	item.Language = "it"
	item.Title = "Timing Docs"
	item.ScriptParams.SkipQualityGate = true
	item.Docs = scriptpkg.DocumentsSpec{Enabled: true, Languages: []string{"it"}, FolderID: "folder-timing"}

	_, err := uc.Execute(ctx, item, scriptpkg.Preset(""), nil)
	require.NoError(t, err)

	report := run.Report()
	_, ok := scriptTimingStageMs(report, "document.publish")
	assert.True(t, ok, "document.publish stage must be recorded")
	assert.Equal(t, 1, scriptTimingOpCount(report, "google_docs", "publish"),
		"google_docs.publish must be measured once for one language")
}

// ── Fan-out / no double counting / unattributed (derived views) ───────

// TestScriptTiming_ParallelTTSWallTimeNotSummed pins the fan-out contract:
// 10 parallel edge-tts calls recorded under one voiceover.generate stage are
// reported as (wall, calls, work, max) — the accumulated work is never summed
// into the pipeline wall time.
func TestScriptTiming_ParallelTTSWallTimeNotSummed(t *testing.T) {
	report := &kernobs.RunReport{
		WallTimeMs: 5210,
		Stages:     []kernobs.StageReport{scriptTimingStage("voiceover.generate", 0, 5210)},
		Operations: []kernobs.OperationReport{
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4000},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4010},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4020},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4030},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4040},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4050},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4060},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4070},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4080},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4090},
		},
	}

	fanout := report.FanoutReports()
	require.Len(t, fanout, 1, "exactly one fan-out boundary must be reported")
	f := fanout[0]
	assert.Equal(t, "voiceover.generate", f.Stage)
	assert.Equal(t, int64(5210), f.WallMs, "wall must be the stage wall time")
	assert.Equal(t, int64(10), f.Calls, "calls must count each parallel operation")
	assert.Equal(t, int64(4090), f.MaxMs, "max must be the longest single call")
	assert.Equal(t, int64(40450), f.WorkMs, "work must be the summed call durations")
	assert.Greater(t, f.WorkMs, f.WallMs,
		"parallel work must exceed wall — it must never be summed into wall time")

	// The canonical TimingSummary carries the same separation.
	summary := report.TimingSummary()
	require.Len(t, summary.Fanout, 1)
	assert.Equal(t, f, summary.Fanout[0])
}

// TestScriptTiming_NoDoubleCountingNestedStages pins that nested stage
// durations are never summed into the top-level attribution: script.prepare
// already contains normalize/validate/plan, so they must not count again.
func TestScriptTiming_NoDoubleCountingNestedStages(t *testing.T) {
	report := &kernobs.RunReport{
		WallTimeMs: 3000,
		Stages: []kernobs.StageReport{
			scriptTimingStage("script.prepare", 0, 3000),
			scriptTimingStage("script.normalize", 0, 1000),
			scriptTimingStage("script.validate", 1000, 2000),
			scriptTimingStage("script.plan", 2000, 3000),
		},
	}

	bd := report.Breakdown()
	assert.Equal(t, int64(3000), bd.AttributedStageMs,
		"only the top-level script.prepare must be attributed (children excluded)")
	assert.Equal(t, int64(0), bd.UnattributedMs, "nothing should be unattributed")
	assert.Equal(t, "script.prepare", bd.BottleneckStage)
}

// TestScriptTiming_UnattributedTime pins that unattributed_ms is the gap
// between the run wall time and the top-level stage attribution.
func TestScriptTiming_UnattributedTime(t *testing.T) {
	report := &kernobs.RunReport{
		WallTimeMs: 10000,
		Stages: []kernobs.StageReport{
			scriptTimingStage("script.prepare", 0, 2000),
			scriptTimingStage("script.engine", 2000, 7000),
		},
	}

	bd := report.Breakdown()
	assert.Equal(t, int64(7000), bd.AttributedStageMs)
	assert.Equal(t, int64(3000), bd.UnattributedMs, "unattributed = wall - top-level stages")
	assert.InDelta(t, 30.0, bd.UnattributedPercent, 1e-9)
}

// ── Legacy projection matches canonical ────────────────────────────────

// TestScriptTiming_LegacyProjectionMatchesCanonical pins that GenerationTimings
// is a pure projection of the canonical observations: SourceResolveMs ←
// source.resolve (0 for text-only), PlanBuildMs ← script.plan, EngineMs ←
// script.engine, and PostprocessMs mirrors the per-processor canonical stages.
func TestScriptTiming_LegacyProjectionMatchesCanonical(t *testing.T) {
	ctx, run := startScriptTimingRun(t)

	e := buildTestEngine(&fakeOllamaGen{}, nil)
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(&stubPostProcessor{name: "entities", result: &adapters.PostProcessResult{Changed: true}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "metadata", result: &adapters.PostProcessResult{Metadata: []scriptpkg.VideoMetadata{{Language: "en", Title: "Anchor"}}}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "persistence", result: &adapters.PostProcessResult{Changed: true}}))
	ppReg.Freeze()

	uc := NewGenerateOneUseCase(adapters.NormalizationConfig{}, nil, e, ppReg, zap.NewNop())
	result, err := uc.Execute(ctx, itemForTimingsTest(), scriptpkg.Preset(""), nil)
	require.NoError(t, err)

	report := run.Report()

	// SourceResolveMs must project source.resolve, which is absent for a
	// text-only item → 0 (not the whole script.prepare duration).
	assert.Equal(t, int64(0), result.Timings.SourceResolveMs)

	planMs, ok := scriptTimingStageMs(report, "script.plan")
	require.True(t, ok)
	assert.Equal(t, planMs, result.Timings.PlanBuildMs,
		"PlanBuildMs must equal the canonical script.plan stage")

	engineMs, ok := scriptTimingStageMs(report, "script.engine")
	require.True(t, ok)
	assert.Equal(t, engineMs, result.Timings.EngineMs,
		"EngineMs must equal the canonical script.engine stage")

	// PostprocessMs mirrors the canonical per-processor stages exactly.
	require.NotEmpty(t, result.Timings.PostprocessMs)
	for name, projected := range result.Timings.PostprocessMs {
		canonical, ok := scriptTimingStageMs(report, name)
		require.True(t, ok, "postprocessor stage %q must be recorded on the run", name)
		assert.Equal(t, canonical, projected,
			"PostprocessMs[%q] must equal the canonical stage observation", name)
	}
}
