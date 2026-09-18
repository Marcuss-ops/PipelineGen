package processor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// observedTranslator records the peak overlap of Translate calls and can fail
// a specific (language, text) pair, so the voiceover processor's fan-out and
// its partial-failure handling can be measured without any provider.
type observedTranslator struct {
	delay time.Duration
	// failOn keys are "<lang>|<text>".
	failOn map[string]bool

	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	calls       int
}

func (o *observedTranslator) Translate(ctx context.Context, text, target string) (string, error) {
	o.mu.Lock()
	o.inFlight++
	o.calls++
	if o.inFlight > o.maxInFlight {
		o.maxInFlight = o.inFlight
	}
	o.mu.Unlock()

	select {
	case <-ctx.Done():
		o.mu.Lock()
		o.inFlight--
		o.mu.Unlock()
		return "", ctx.Err()
	case <-time.After(o.delay):
	}

	o.mu.Lock()
	o.inFlight--
	o.mu.Unlock()

	if o.failOn[target+"|"+text] {
		return "", errors.New("translator exploded")
	}
	return "[" + target + "] " + text, nil
}

func (o *observedTranslator) peak() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.maxInFlight
}

func voiceoverScenes(n int) scriptpkg.SpecSceneOutput {
	scenes := make([]scriptpkg.SpecScene, n)
	for i := range scenes {
		scenes[i] = scriptpkg.SpecScene{
			ID:    "scene-" + string(rune('0'+i)),
			Index: i,
			Text:  "source scene " + string(rune('0'+i)),
			Kind:  scriptpkg.SceneNarration,
		}
	}
	return scriptpkg.SpecSceneOutput{Version: 1, Scenes: scenes}
}

// TestVoiceoverProcessor_TranslatesLanguagesInParallel pins the removed serial
// nested loop: scene translations of the same language (and of different
// languages) must overlap instead of paying their sum.
func TestVoiceoverProcessor_TranslatesLanguagesInParallel(t *testing.T) {
	translator := &observedTranslator{delay: 40 * time.Millisecond}
	var mu sync.Mutex
	ttsItems := make([]voiceover.GenerateVoiceoverItemCommand, 0, 8)
	stub := &stubItemExecutor{fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
		mu.Lock()
		ttsItems = append(ttsItems, voiceover.GenerateVoiceoverItemCommand{Text: text, Language: voiceover.Language(lang)})
		mu.Unlock()
		return &voiceover.VoiceoverItemResult{
			Status:    voiceover.StatusCompleted,
			Language:  voiceover.Language(lang),
			Filename:  filename,
			LocalPath: "/tmp/" + filename,
		}, nil
	}}

	proc := NewVoiceoverProcessor(stub, zap.NewNop())
	proc.ConfigureMultilingual(map[string]string{"en": "en-US-ChristopherNeural", "de": "de-DE-FlorianMultilingualNeural"}, translator)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:        "parallel-languages",
		Title:     "Parallel Languages",
		Language:  "en",
		Languages: []string{"en", "de"},
	}
	spec := voiceoverScenes(3)

	result, err := proc.Process(context.Background(), plan, adapters.ProcessInput{SpecScene: spec})
	require.NoError(t, err)
	require.NotNil(t, result)

	if translator.peak() < 2 {
		t.Fatalf("peak concurrent scene translations = %d, want >= 2 (translations are serialised)", translator.peak())
	}
	// 3 scenes × 2 languages (the source language needs no translation).
	require.Len(t, result.Voiceovers, 6)
	for _, item := range ttsItems {
		if item.Language == "de" {
			assert.True(t, strings.HasPrefix(item.Text, "[de] "),
				"German voiceover must synthesise the TRANSLATED text, got %q", item.Text)
		}
	}
}

// TestVoiceoverProcessor_PartialTranslationFailureKeepsRestOfLanguage pins the
// per-scene failure isolation: one scene whose translation fails loses only its
// own voiceover, the rest of the language is still produced, and the warning
// names the scene. Previously a single failure discarded the whole language.
func TestVoiceoverProcessor_PartialTranslationFailureKeepsRestOfLanguage(t *testing.T) {
	translator := &observedTranslator{failOn: map[string]bool{"de|source scene 1": true}}
	var mu sync.Mutex
	ttsItems := make([]voiceover.GenerateVoiceoverItemCommand, 0, 8)
	stub := &stubItemExecutor{fn: func(text, lang, filename string) (*voiceover.VoiceoverItemResult, error) {
		mu.Lock()
		ttsItems = append(ttsItems, voiceover.GenerateVoiceoverItemCommand{Text: text, Language: voiceover.Language(lang)})
		mu.Unlock()
		return &voiceover.VoiceoverItemResult{
			Status:    voiceover.StatusCompleted,
			Language:  voiceover.Language(lang),
			Filename:  filename,
			LocalPath: "/tmp/" + filename,
		}, nil
	}}

	proc := NewVoiceoverProcessor(stub, zap.NewNop())
	proc.ConfigureMultilingual(map[string]string{"en": "en-US-ChristopherNeural", "de": "de-DE-FlorianMultilingualNeural"}, translator)

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:        "partial-language",
		Title:     "Partial Language",
		Language:  "en",
		Languages: []string{"en", "de"},
	}
	spec := voiceoverScenes(3)

	result, err := proc.Process(context.Background(), plan, adapters.ProcessInput{SpecScene: spec})
	require.NoError(t, err)
	require.NotNil(t, result)

	germanIndexes := map[int]bool{}
	for _, out := range result.Voiceovers {
		if out.Language == "de" {
			germanIndexes[out.SceneIndex] = true
		}
	}
	assert.Len(t, germanIndexes, 2, "the two translatable German scenes must still get a voiceover")
	assert.False(t, germanIndexes[1], "the scene whose translation failed must not be synthesised")

	for _, item := range ttsItems {
		if item.Language == "de" {
			assert.NotEqual(t, "source scene 1", item.Text,
				"a failed translation must never fall back to the source text")
		}
	}

	require.NotEmpty(t, result.Warnings)
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "language de: scene 1 translation failed") {
			found = true
		}
	}
	assert.True(t, found, "warnings must name the failed language+scene; got %v", result.Warnings)
}
