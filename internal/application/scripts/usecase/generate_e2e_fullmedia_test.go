// Package scripts — generate_e2e_fullmedia_test.go is the E2E pin for
// a generated script that requests both scene images and voiceover.
//
// The test exercises the full GenerateOneUseCase.Execute path with a
// fake engine plus fake image / voiceover services, then asserts that
// the final SpecScene surface carries Drive links and image URLs while
// omitting local paths from the public result.
package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/types"
)

// fakeVoiceoverGen is a voiceover.VoiceoverItemExecutor stub used by
// the E2E fullmedia test.
//
// P0-#3 final closure (July 2026): the legacy VoiceoverService port
// (Generate + GenerateWithDestination) is RETIRED. The stub now
// implements the single canonical Execute method with a typed
// *voiceover.GenerateVoiceoverItemCommand, returning a
// *voiceover.VoiceoverItemResult.
type fakeVoiceoverGen struct {
	calls int
}

func (f *fakeVoiceoverGen) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	f.calls++
	if item == nil {
		return &voiceover.VoiceoverItemResult{
			Status: voiceover.StatusFailed,
			Error:  "nil GenerateVoiceoverItemCommand",
		}, nil
	}
	return &voiceover.VoiceoverItemResult{
		Status:      voiceover.StatusCompleted,
		Language:    item.Language,
		Filename:    item.Filename,
		LocalPath:   "/tmp/" + item.Filename,
		DriveLink:   "https://drive.google.com/file/d/" + item.Filename + "/view",
		DriveFileID: item.Filename,
	}, nil
}

// Compile-time assertion (AGENTS.md Pattern 0): fakeVoiceoverGen must
// structurally satisfy voiceover.VoiceoverItemExecutor.
var _ voiceover.VoiceoverItemExecutor = (*fakeVoiceoverGen)(nil)

func TestGenerateE2E_FullMedia_BindsImagesAndVoiceoverWithoutLocalPaths(t *testing.T) {
	t.Parallel()

	sceneJSON := `{"schema_version":1,"text":"Narrative covering 2 distinct scenes.","specscene":{"version":1,"scenes":[` +
		`{"id":"scene-0","index":0,"text":"Opening narration.","kind":"narration","bindings":{}},` +
		`{"id":"scene-1","index":1,"text":"Closing narration.","kind":"narration","bindings":{}}` +
		`]}}`
	gen := &fakeOllamaGen{
		result: &ollamatypes.GenerationResult{
			Script:      sceneJSON,
			WordCount:   16,
			EstDuration: 4,
			Model:       "llama3:8b",
			Prompt:      "ignored",
		},
	}
	e := buildTestEngine(gen, nil)

	imgSvc := newFakeImageGenSvc("https://drive.google.com/file/d/fake-image-full/view")
	voSvc := &fakeVoiceoverGen{}

	ppReg := adapters.NewPostProcessorRegistry(zap.NewNop())
	require.True(t, ppReg.Register(adapters.NewImageProcessor(imgSvc, zap.NewNop())))
	require.True(t, ppReg.Register(adapters.NewVoiceoverProcessor(voSvc, zap.NewNop())))
	require.True(t, ppReg.Register(&stubPostProcessor{
		name:   "persistence",
		result: &adapters.PostProcessResult{Changed: true},
	}))
	ppReg.Freeze()

	uc := NewGenerateOneUseCase(
		adapters.NormalizationConfig{},
		nil,
		e,
		ppReg,
		zap.NewNop(),
	)

	item := scriptpkg.GenerationItemV2{
		ID:       "e2e-fullmedia-item",
		Title:    "Full Media E2E",
		Language: "en",
		Tone:     "neutral",
		Style:    "cinematic",
		Model:    "llama3:8b",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "full media",
			SourceText: "Narrative covering distinct scenes opening narration closing narration. Check both scene images and voiceover in the final generated script.",
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords: 16,
		},
		Output: scriptpkg.OutputSpec{
			GenerateSceneImages: scriptpkg.ToggleEnabled,
			GenerateVoiceover:   scriptpkg.ToggleEnabled,
			GenerateDocument:    scriptpkg.ToggleDisabled,
			ExtractEntities:     scriptpkg.ToggleDisabled,
			GenerateMetadata:    scriptpkg.ToggleDisabled,
			SaveToDB:            false,
		},
	}

	result, err := uc.Execute(context.Background(), item, scriptpkg.Preset(""), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Output.SpecScene.Scenes, 2)
	require.Greater(t, imgSvc.calls, 0)
	require.Greater(t, voSvc.calls, 0)

	for i, sc := range result.Output.SpecScene.Scenes {
		require.NotNil(t, sc.Bindings.Image, "scene[%d] image binding must be present", i)
		require.NotNil(t, sc.Bindings.Voiceover, "scene[%d] voiceover binding must be present", i)
		assert.Equal(t, "generated", sc.Bindings.Image.Status)
		assert.Equal(t, "completed", sc.Bindings.Voiceover.Status)
		assert.NotEmpty(t, sc.Bindings.Image.URL)
		assert.NotEmpty(t, sc.Bindings.Voiceover.Link)
		assert.Empty(t, sc.Bindings.Image.LocalPath, "scene[%d] image local_path must be stripped", i)
		assert.Empty(t, sc.Bindings.Voiceover.LocalPath, "scene[%d] voiceover local_path must be stripped", i)
	}
}
