// Package scripts — generation_engine_test.go exercises the
// GenerationEngineRunner in isolation.
package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestGenerationEngineRunner_Generate_Success verifies that the
// runner invokes the engine, emits tracker events, and returns a
// populated GeneratedDraft.
func TestGenerationEngineRunner_Generate_Success(t *testing.T) {
	t.Parallel()

	gen := &fakeOllamaGen{}
	engine := buildTestEngine(gen, nil)
	runner := NewGenerationEngineRunner(engine)

	item := scriptpkg.GenerationItemV2{ID: "runner-success"}
	plan := scriptpkg.ResolvedGenerationPlan{ID: "runner-success", Title: "Runner Success"}

	var events []string
	tracker := NewProgressTracker(nil, item.ID)
	tracker.SetEventFn(func(eventType, _ string, _ map[string]any) {
		events = append(events, eventType)
	})

	draft, err := runner.Generate(context.Background(), item, plan, tracker)
	require.NoError(t, err)
	require.NotNil(t, draft)
	require.NotNil(t, draft.EngineResult)
	assert.GreaterOrEqual(t, draft.EngineMs, int64(0))
	assert.Equal(t, int32(1), gen.calls.Load())

	wantEvents := []string{"script.generated", "scenes.created"}
	assert.Equal(t, wantEvents, events)
}

// TestGenerationEngineRunner_Generate_Error verifies that the runner
// returns a typed GenerationError when the engine fails.
func TestGenerationEngineRunner_Generate_Error(t *testing.T) {
	t.Parallel()

	forcedErr := errors.New("forced engine error")
	gen := &fakeOllamaGen{returnErr: forcedErr}
	engine := buildTestEngine(gen, nil)
	runner := NewGenerationEngineRunner(engine)

	item := scriptpkg.GenerationItemV2{ID: "runner-error"}
	plan := scriptpkg.ResolvedGenerationPlan{ID: "runner-error", Title: "Runner Error"}

	_, err := runner.Generate(context.Background(), item, plan, nil)
	require.Error(t, err)

	var genErr *scriptpkg.GenerationError
	require.True(t, errors.As(err, &genErr))
	assert.Equal(t, "runner-error", genErr.ItemID)
	assert.Equal(t, "engine", genErr.Phase)
	assert.ErrorIs(t, err, scriptpkg.ErrGenerationFailed)
}

// TestGenerationEngineRunner_Generate_NilRunner verifies that the
// runner returns a typed GenerationError when called on a nil runner.
func TestGenerationEngineRunner_Generate_NilRunner(t *testing.T) {
	t.Parallel()

	runner := NewGenerationEngineRunner(nil)
	item := scriptpkg.GenerationItemV2{ID: "runner-nil"}
	plan := scriptpkg.ResolvedGenerationPlan{ID: "runner-nil"}

	_, err := runner.Generate(context.Background(), item, plan, nil)
	require.Error(t, err)

	var genErr *scriptpkg.GenerationError
	require.True(t, errors.As(err, &genErr))
	assert.Equal(t, "runner-nil", genErr.ItemID)
	assert.Equal(t, "engine", genErr.Phase)
}

// TestGenerationEngineRunner_NewWithNilEngine verifies that the
// constructor returns nil when given a nil engine, preserving the
// pre-construction error behavior of GenerateOneUseCase.
func TestGenerationEngineRunner_NewWithNilEngine(t *testing.T) {
	t.Parallel()
	runner := NewGenerationEngineRunner(nil)
	require.Nil(t, runner)
}
