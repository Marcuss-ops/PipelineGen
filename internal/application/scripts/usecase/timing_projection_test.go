package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestExecute_ProjectsTimingsFromCanonicalStages pins the legacy
// GenerationTimings projection: SourceResolveMs ← source.resolve,
// PlanBuildMs ← script.plan, TotalMs ← the run's canonical clock. A text-only
// item skips source resolution entirely, so SourceResolveMs MUST be 0 (not the
// whole script.prepare duration, which is what the pre-projection code wrote).
func TestExecute_ProjectsTimingsFromCanonicalStages(t *testing.T) {
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-timing", AttemptID: "attempt-timing"})
	ctx := kernobs.WithRun(context.Background(), run)

	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(&stubPostProcessor{name: "entities", result: &adapters.PostProcessResult{Changed: true}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "metadata", result: &adapters.PostProcessResult{Metadata: []scriptpkg.VideoMetadata{{Language: "en", Title: "Anchor"}}}}))
	require.True(t, ppReg.Register(&stubPostProcessor{name: "persistence", result: &adapters.PostProcessResult{Changed: true}}))
	ppReg.Freeze()

	uc := NewGenerateOneUseCase(adapters.NormalizationConfig{}, nil, e, ppReg, zap.NewNop())
	result, err := uc.Execute(ctx, itemForTimingsTest(), scriptpkg.Preset(""), nil)
	require.NoError(t, err, "Execute must succeed for a well-formed text-only item")
	require.NotNil(t, result)

	// SourceResolveMs must project source.resolve (0 for a text-only item),
	// not the whole script.prepare duration.
	assert.Equal(t, int64(0), result.Timings.SourceResolveMs,
		"SourceResolveMs must project source.resolve, not script.prepare")

	// TotalMs comes from the canonical run clock (non-negative).
	assert.GreaterOrEqual(t, result.Timings.TotalMs, int64(0))

	// The canonical stages must all be recorded on the run report.
	stageNames := make(map[string]bool)
	for _, st := range run.Report().Stages {
		stageNames[st.Name] = true
	}
	for _, want := range []string{"script.prepare", "script.normalize", "script.validate", "script.plan", "script.engine", "script.postprocess"} {
		assert.True(t, stageNames[want], "run report missing canonical stage %q", want)
	}
}
