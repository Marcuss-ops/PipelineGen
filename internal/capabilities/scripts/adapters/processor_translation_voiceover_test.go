package adapters

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type multilingualVoiceRecorder struct {
	items []*voiceover.GenerateVoiceoverItemCommand
}

func (r *multilingualVoiceRecorder) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	r.items = append(r.items, item)
	return &voiceover.VoiceoverItemResult{Status: voiceover.StatusCompleted, Language: item.Language, DriveLink: "https://drive.test/" + item.Filename}, nil
}

type multilingualTextTranslator struct{}

func (multilingualTextTranslator) Translate(_ context.Context, text, target string) (string, error) {
	return fmt.Sprintf("%s [%s]", text, target), nil
}

// Test 1: traduzione riuscita
func TestVoiceoverProcessor_UsesTranslatedTextAndLanguage(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "testo italiano", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "proj-1",
		Title:       "Test Project",
		Language:    "en",
		TranslateTo: "it",
	}

	input := ProcessInput{
		Text:              "testo italiano",
		SpecScene:         *spec,
		EffectiveLanguage: "it",
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Equal(t, int32(1), stub.calls.Load())
}

// Test 2: traduzione fallita
func TestVoiceoverProcessor_SkipsWhenTranslationFailed(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "english text", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "proj-2",
		Title:       "Test Project 2",
		Language:    "en",
		TranslateTo: "it",
	}

	input := ProcessInput{
		Text:              "english text",
		SpecScene:         *spec,
		EffectiveLanguage: "en",
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "skipped", result.Voiceovers[0].Status)
	assert.Equal(t, int32(0), stub.calls.Load())
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "voiceover skipped")
}

// Test 3: nessuna traduzione
func TestVoiceoverProcessor_UsesSourceLanguageWithoutTranslation(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "english text", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "proj-3",
		Title:       "Test Project 3",
		Language:    "en",
		TranslateTo: "",
	}

	input := ProcessInput{
		Text:              "english text",
		SpecScene:         *spec,
		EffectiveLanguage: "en",
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Equal(t, int32(1), stub.calls.Load())
}

// Test 4: stessa lingua
func TestVoiceoverProcessor_AllowsSameSourceAndTargetLanguage(t *testing.T) {
	t.Parallel()

	stub := &stubItemExecutor{}
	proc := NewVoiceoverProcessor(stub, zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "scene-0", Index: 0, Text: "testo italiano", Kind: scriptpkg.SceneNarration},
	}
	spec := &scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "proj-4",
		Title:       "Test Project 4",
		Language:    "it",
		TranslateTo: "it",
	}

	input := ProcessInput{
		Text:              "testo italiano",
		SpecScene:         *spec,
		EffectiveLanguage: "it",
	}

	result, err := proc.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Voiceovers, 1)
	assert.Equal(t, "completed", result.Voiceovers[0].Status)
	assert.Equal(t, int32(1), stub.calls.Load())
}

func TestVoiceoverProcessor_FansOutLanguagesVoicesAndTranslations(t *testing.T) {
	t.Parallel()
	recorder := &multilingualVoiceRecorder{}
	proc := NewVoiceoverProcessor(recorder, zap.NewNop())
	proc.ConfigureMultilingual(map[string]string{
		"it": "fr-FR-RemyMultilingualNeural",
		"en": "en-US-ChristopherNeural",
		"fr": "fr-FR-RemyMultilingualNeural",
	}, multilingualTextTranslator{})

	spec := scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-0", Index: 0, Text: "testo sorgente", Kind: scriptpkg.SceneNarration,
	}}}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID: "multi-voice", Title: "Multi Voice", Language: "it", Languages: []string{"it", "en", "fr"},
		VoiceoverFolderID: "folder-correct",
	}
	result, err := proc.Process(context.Background(), plan, ProcessInput{Text: "testo sorgente", SpecScene: spec})
	require.NoError(t, err)
	require.Len(t, recorder.items, 3)
	require.Len(t, result.Voiceovers, 3)

	voices := map[string]string{}
	for _, item := range recorder.items {
		voices[string(item.Language)] = item.Voice
		require.Equal(t, "folder-correct", item.Destination.FolderID)
	}
	assert.Equal(t, "fr-FR-RemyMultilingualNeural", voices["it"])
	assert.Equal(t, "en-US-ChristopherNeural", voices["en"])
	assert.Equal(t, "fr-FR-RemyMultilingualNeural", voices["fr"])
}
