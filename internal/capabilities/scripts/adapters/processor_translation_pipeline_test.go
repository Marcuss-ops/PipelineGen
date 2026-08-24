package adapters

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// ── Stubs for the translation→voiceover pipeline integration test ──

// pipelineTranslatorStub prefixes text with the target language tag
// so the test can verify the VoiceoverProcessor receives translated
// (not original) text.
type pipelineTranslatorStub struct{}

func (pipelineTranslatorStub) Translate(_ context.Context, text, lang string) (string, error) {
	return "[" + lang + "] " + text, nil
}

// pipelineTransUCStub delegates to the ScriptTranslator to translate
// model.Text and each scene.Text, producing a fully-translated
// *ModelScriptOutputV1. This is the canonical behavior of the real
// usecase.NewTranslationUseCaseAdapter that wraps TranslateScriptSpec.
type pipelineTransUCStub struct{}

func (pipelineTransUCStub) TranslateScriptSpec(
	_ context.Context,
	in *scriptpkg.ModelScriptOutputV1,
	_ *scriptpkg.ClipEvidence,
	targetLang string,
	translator ports.ScriptTranslator,
) (*scriptpkg.ModelScriptOutputV1, []string, error) {
	translated, err := translator.Translate(context.Background(), in.Text, targetLang)
	if err != nil {
		return nil, nil, err
	}
	out := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: in.SchemaVersion,
		Text:          translated,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: in.SpecScene.Version,
			Scenes:  make([]scriptpkg.SpecScene, len(in.SpecScene.Scenes)),
		},
	}
	for i, sc := range in.SpecScene.Scenes {
		sceneCopy := sc
		sceneCopy.Text, _ = translator.Translate(context.Background(), sc.Text, targetLang)
		out.SpecScene.Scenes[i] = sceneCopy
	}
	return out, nil, nil
}

// pipelineVOStub records the text passed to Execute so the test can
// assert it received Italian (not English) text. Mutex-protected
// because RunVoiceoverSceneFanout uses concurrent.ParallelMap.
//
// P0-#3 final closure (July 2026): the legacy VoiceoverService port
// (Generate + GenerateWithDestination) is RETIRED. The stub now
// implements the single canonical Execute method.
type pipelineVOStub struct {
	mu            sync.Mutex
	capturedTexts []string
}

func (s *pipelineVOStub) capture(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.capturedTexts = append(s.capturedTexts, text)
}

func (s *pipelineVOStub) Execute(_ context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	if item == nil {
		return &voiceover.VoiceoverItemResult{
			Status: voiceover.StatusFailed,
			Error:  "nil GenerateVoiceoverItemCommand",
		}, nil
	}
	s.capture(item.Text)
	return &voiceover.VoiceoverItemResult{
		Status:    voiceover.StatusCompleted,
		Language:  item.Language,
		Filename:  item.Filename,
		DriveLink: "https://drive.example/vo.mp3",
		LocalPath: "/tmp/vo.mp3",
	}, nil
}

// Compile-time assertion (AGENTS.md Pattern 0): pipelineVOStub must
// structurally satisfy voiceover.VoiceoverItemExecutor.
var _ voiceover.VoiceoverItemExecutor = (*pipelineVOStub)(nil)

// pipelineClassifyStub always returns ReasonUnknown (matches the noop
// classifier behavior; sufficient for the pipeline integration test).
type pipelineClassifyStub struct{}

func (pipelineClassifyStub) ClassifyReason(_ string) ports.TranslationWarningReason {
	return ports.ReasonUnknown
}

// TestPipeline_TranslationFeedsVoiceoverProcessor exercises the full
// translation→voiceover chain in a single test via the
// PostProcessorRegistry.Run surface. This is the canonical regression
// guard for Bug 2 (mergePostProcessResult write-back): before the fix,
// VoiceoverProcessor received the original English text instead of the
// translated Italian text because mergePostProcessResult did NOT write
// TranslatedText/TranslatedSpecScene back to currentInput.
//
// The test verifies 5 invariants:
//  1. TranslatedText is populated on PipelineResult
//  2. TranslatedSpecScene.Scenes is populated
//  3. VoiceoverProcessor receives Italian (not English) text
//  4. Voiceover outcomes are "completed"
//  5. FinalSpecScene reflects the translated surface
func TestPipeline_TranslationFeedsVoiceoverProcessor(t *testing.T) {
	// Build registry with TranslationProcessor + VoiceoverProcessor.
	reg := NewPostProcessorRegistry(zap.NewNop())
	reg.Register(NewTranslationProcessor(
		pipelineTranslatorStub{},
		nil, // metrics → noop
		pipelineTransUCStub{},
		pipelineClassifyStub{},
		zap.NewNop(),
	))
	voStub := &pipelineVOStub{}
	reg.Register(NewVoiceoverProcessor(voStub, zap.NewNop()))
	reg.Freeze()

	// Input: English text with 2 scenes.
	input := ProcessInput{
		Text: "The fighter walks into the arena.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{ID: "scene-0", Index: 0, Kind: "intro", Text: "Welcome to the arena."},
				{ID: "scene-1", Index: 1, Kind: "clip", Text: "The crowd roars."},
			},
		},
	}

	// Plan: translate to Italian, run translation then voiceover.
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "test-pipeline",
		Language:    "en",
		TranslateTo: "it",
		Title:       "Boxing Story",
		Postprocessors: []string{
			string(ProcessorTranslation),
			string(ProcessorVoiceover),
		},
	}

	// Run the pipeline.
	result, err := reg.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Run() returned nil result")
	}

	// ── Invariant 1: TranslatedText populated ──
	if strings.TrimSpace(result.TranslatedText) == "" {
		t.Fatal("PipelineResult.TranslatedText is empty — translation did not propagate to the aggregate result")
	}
	if !strings.Contains(result.TranslatedText, "[it]") {
		t.Fatalf("PipelineResult.TranslatedText does not contain Italian marker [it]: %q", result.TranslatedText)
	}

	// ── Invariant 2: TranslatedSpecScene populated ──
	if len(result.TranslatedSpecScene.Scenes) != 2 {
		t.Fatalf("PipelineResult.TranslatedSpecScene.Scenes = %d, want 2", len(result.TranslatedSpecScene.Scenes))
	}
	for _, sc := range result.TranslatedSpecScene.Scenes {
		if !strings.Contains(sc.Text, "[it]") {
			t.Fatalf("TranslatedSpecScene scene %q does not contain Italian marker [it]: %q", sc.ID, sc.Text)
		}
	}

	// ── Invariant 3: VoiceoverProcessor received Italian text ──
	// This is the critical Bug 2 regression guard: before the
	// mergePostProcessResult write-back fix, the voiceover processor
	// received the original English text ("Welcome to the arena.")
	// instead of the translated Italian text ("[it] Welcome to the arena.").
	//
	// Note: RunVoiceoverSceneFanout uses concurrent.ParallelMap, so
	// the captured texts may arrive in any order. We verify set
	// membership rather than positional order.
	voStub.mu.Lock()
	captured := append([]string(nil), voStub.capturedTexts...)
	voStub.mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("VoiceoverService received %d calls, want 2", len(captured))
	}
	for i, text := range captured {
		if !strings.Contains(text, "[it]") {
			t.Errorf("VoiceoverService scene %d received untranslated text %q — mergePostProcessResult write-back is broken", i, text)
		}
	}
	// Verify both scene texts are present (order-independent).
	joined := strings.Join(captured, "|||")
	if !strings.Contains(joined, "Welcome to the arena.") {
		t.Errorf("VoiceoverService did not receive scene 0 text 'Welcome to the arena.'; captured=%v", captured)
	}
	if !strings.Contains(joined, "The crowd roars.") {
		t.Errorf("VoiceoverService did not receive scene 1 text 'The crowd roars.'; captured=%v", captured)
	}
	// ── Invariant 4: Voiceover outcomes are "completed" ──
	if len(result.Voiceovers) != 2 {
		t.Fatalf("PipelineResult.Voiceovers = %d, want 2", len(result.Voiceovers))
	}
	for _, v := range result.Voiceovers {
		if v.Status != "completed" {
			t.Errorf("Voiceover scene %d status = %q, want %q", v.SceneIndex, v.Status, "completed")
		}
		if v.Link == "" {
			t.Errorf("Voiceover scene %d has empty Link", v.SceneIndex)
		}
	}

	// ── Invariant 5: FinalSpecScene reflects translated surface ──
	if len(result.FinalSpecScene.Scenes) == 0 {
		t.Fatal("PipelineResult.FinalSpecScene.Scenes is empty")
	}
	if !strings.Contains(result.FinalSpecScene.Scenes[0].Text, "[it]") {
		t.Errorf("FinalSpecScene scene 0 text = %q, want Italian translation", result.FinalSpecScene.Scenes[0].Text)
	}
}
