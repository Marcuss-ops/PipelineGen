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

func umbrellaCoverageEngine(t *testing.T) {
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
}

func umbrellaCoveragePostprocess(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	log := zap.New(core)

	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	// Register a Required-class postprocessor that errors.
	// clip_search is the stage ExtractEntities selects (entity
	// extraction no longer runs as a legacy "entities"
	// postprocessor stage), so it is the processor guaranteed
	// to execute during the postprocess phase for this item.
	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	ppErrProc := &errPostProcessor{
		name: "clip_search",
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
			ExtractEntities: scriptpkg.ToggleEnabled, // forces clip_search (et al.) into plan.Postprocessors
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
}

func umbrellaCoverageUCNil(t *testing.T) {
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
}

func umbrellaCoverageEngineNil(t *testing.T) {
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
}
