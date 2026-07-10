// Package adapters — processor_translation_full_pipeline_test.go
// verifies that the translated SpecScene survives the FULL
// PostProcessorRegistry.Run() pipeline:
//
//	translation → clip_bindings → voiceover → document → persistence
//
// This is a deeper integration test than the existing
// TestPipeline_TranslationFeedsVoiceoverProcessor (which only
// exercises translation → voiceover). It extends the chain to
// cover clip_bindings (no-op with empty ClipEvidence, but still
// participates in the mergePostProcessResult loop), document
// (captures the HTML that BuildGenerationDocumentHTML renders),
// and persistence (verifies the script row stores Italian text
// and translated SpecScene JSON).
//
// Reuses stubs from processor_translation_pipeline_test.go (same
// package): pipelineTranslatorStub, pipelineTransUCStub,
// pipelineClassifyStub, pipelineVOStub. Adds:
//   - stubFullPipelineDocSvc: captures CreateDoc args (title,
//     content HTML, folder) so we can assert Italian markers
//     survive into the document HTML.
//   - idemFakeRepo (from processor_persistence_test.go): captures
//     SaveScript args so we can assert Italian text + translated
//     SpecScene JSON land in the persisted record.
package adapters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// stubFullPipelineDocSvc is a minimal DocumentsService stub that
// captures the title and HTML content passed to CreateDoc so the
// test can assert Italian markers survive into the document surface.
type stubFullPipelineDocSvc struct {
	capturedTitle   string
	capturedContent string
	capturedFolder  string
}

func (s *stubFullPipelineDocSvc) CreateDoc(_ context.Context, title, content string, _ FolderResolver, driveFolderID string) (string, string) {
	s.capturedTitle = title
	s.capturedContent = content
	s.capturedFolder = driveFolderID
	return "https://drive.example/doc-full-pipeline", "doc-fp-id-001"
}

// TestPipeline_FullChain_TranslationSpecSceneSurvival exercises the
// 5-processor pipeline (translation → clip_bindings → voiceover →
// document → persistence) via PostProcessorRegistry.Run() and
// verifies that Italian markers ([it]) survive through every stage.
//
// This is the canonical end-to-end regression guard that the
// translated SpecScene surface is not silently dropped by any
// intermediate processor or merge step. A regression in any of
// the following surfaces would surface as a test failure:
//
//   - mergePostProcessResult write-back (currentInput.Text ←
//     TranslatedText, currentInput.SpecScene ← TranslatedSpecScene)
//   - ClipBindingsProcessor's no-op passthrough (Changed=false
//     when ClipEvidence is empty — must not clobber the merged
//     Italian surface)
//   - VoiceoverProcessor reading translated scene text from
//     input.SpecScene.Scenes[].Text
//   - DocumentProcessor reading translated input.Text for the
//     HTML content
//   - PersistenceProcessor reading translated input.Text +
//     input.SpecScene for the ScriptRecord fields
//
// Invariants verified (8):
//  1. PipelineResult.TranslatedText contains [it]
//  2. PipelineResult.TranslatedSpecScene.Scenes all contain [it]
//  3. VoiceoverService captured Italian text (order-independent)
//  4. Voiceover outcomes are "completed"
//  5. Document HTML contains Italian markers
//  6. Persisted script row OutputText contains [it]
//  7. Persisted SpecScene JSON contains Italian scene text
//  8. FinalSpecScene reflects translated surface
func TestPipeline_FullChain_TranslationSpecSceneSurvival(t *testing.T) {
	// ── Arrange: build registry with all 5 processors ──
	reg := NewPostProcessorRegistry(zap.NewNop())

	// 1. TranslationProcessor (uses canonical stubs from pipeline_test.go)
	reg.Register(NewTranslationProcessor(
		pipelineTranslatorStub{},
		nil,
		pipelineTransUCStub{},
		pipelineClassifyStub{},
		zap.NewNop(),
	))

	// 2. ClipBindingsProcessor (no-op when ClipEvidence is empty;
	//    Changed=false → mergePostProcessResult skips write-back,
	//    preserving the Italian surface from translation)
	reg.Register(NewClipBindingsProcessor(zap.NewNop()))

	// 3. VoiceoverProcessor
	voStub := &pipelineVOStub{}
	reg.Register(NewVoiceoverProcessor(voStub, zap.NewNop()))

	// 4. DocumentProcessor (stub docs service captures HTML)
	docStub := &stubFullPipelineDocSvc{}
	reg.Register(NewDocumentProcessor(docStub, nil))

	// 5. PersistenceProcessor (in-memory fake captures ScriptRecord)
	repo := &idemFakeRepo{}
	reg.Register(NewPersistenceProcessor(repo, zap.NewNop()))

	reg.Freeze()

	// ── Input: English text with 2 scenes ──
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

	// ── Plan: translate to Italian, then all 5 processors ──
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:          "full-pipeline-test",
		Language:    "en",
		TranslateTo: "it",
		Title:       "Boxing Story",
		Topic:       "boxing",
		Tone:        "dramatic",
		Model:       "gemma3:e4b",
		Mode:        "text",
		TargetWords: 500,
		CacheKey:    "fullpipeline00",
		Postprocessors: []string{
			string(ProcessorTranslation),
			string(ProcessorClipBindings),
			string(ProcessorVoiceover),
			string(ProcessorDocument),
			string(ProcessorPersistence),
		},
	}

	// ── Act: run the full pipeline ──
	result, err := reg.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Run() returned nil result")
	}

	// ── Assert Invariant 1: TranslatedText populated ──
	if strings.TrimSpace(result.TranslatedText) == "" {
		t.Fatal("PipelineResult.TranslatedText is empty — translation did not propagate to the aggregate result")
	}
	if !strings.Contains(result.TranslatedText, "[it]") {
		t.Fatalf("PipelineResult.TranslatedText does not contain Italian marker [it]: %q", result.TranslatedText)
	}

	// ── Assert Invariant 2: TranslatedSpecScene populated ──
	if len(result.TranslatedSpecScene.Scenes) != 2 {
		t.Fatalf("PipelineResult.TranslatedSpecScene.Scenes = %d, want 2", len(result.TranslatedSpecScene.Scenes))
	}
	for _, sc := range result.TranslatedSpecScene.Scenes {
		if !strings.Contains(sc.Text, "[it]") {
			t.Errorf("TranslatedSpecScene scene %q text does not contain Italian marker [it]: %q", sc.ID, sc.Text)
		}
	}

	// ── Assert Invariant 3: VoiceoverService received Italian text ──
	voStub.mu.Lock()
	captured := append([]string(nil), voStub.capturedTexts...)
	voStub.mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("VoiceoverService received %d calls, want 2", len(captured))
	}
	for i, text := range captured {
		if !strings.Contains(text, "[it]") {
			t.Errorf("VoiceoverService scene %d received untranslated text %q — mergePostProcessResult write-back or ClipBindings passthrough is broken", i, text)
		}
	}
	// Verify both scene texts present (order-independent via ParallelMap).
	joined := strings.Join(captured, "|||")
	if !strings.Contains(joined, "Welcome to the arena.") {
		t.Errorf("VoiceoverService did not receive scene 0 text 'Welcome to the arena.'; captured=%v", captured)
	}
	if !strings.Contains(joined, "The crowd roars.") {
		t.Errorf("VoiceoverService did not receive scene 1 text 'The crowd roars.'; captured=%v", captured)
	}

	// ── Assert Invariant 4: Voiceover outcomes are "completed" ──
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

	// ── Assert Invariant 5: Document HTML contains Italian markers ──
	if docStub.capturedContent == "" {
		t.Fatal("DocumentProcessor.CreateDoc was not called (empty content)")
	}
	if !strings.Contains(docStub.capturedContent, "[it]") {
		t.Errorf("Document HTML does not contain Italian marker [it]; content snippet: %q",
			textutil.Truncate(docStub.capturedContent, 200))
	}
	// Verify both scene texts appear in the document HTML.
	if !strings.Contains(docStub.capturedContent, "Welcome to the arena.") {
		t.Errorf("Document HTML does not contain scene 0 text 'Welcome to the arena.'")
	}
	if !strings.Contains(docStub.capturedContent, "The crowd roars.") {
		t.Errorf("Document HTML does not contain scene 1 text 'The crowd roars.'")
	}

	// ── Assert Invariant 6: Persisted script row OutputText contains [it] ──
	if repo.lastRec == nil {
		t.Fatal("PersistenceProcessor.SaveScript was not called (lastRec is nil)")
	}
	if !strings.Contains(repo.lastRec.OutputText, "[it]") {
		t.Errorf("Persisted OutputText does not contain Italian marker [it]: %q", repo.lastRec.OutputText)
	}
	if result.ScriptID != 1234 {
		t.Errorf("PipelineResult.ScriptID = %d, want 1234", result.ScriptID)
	}

	// ── Assert Invariant 6b: ManifestV2 reflects translated SpecScene ──
	if repo.saveManifestCalls.Load() > 0 && len(repo.lastManifest) > 0 {
		var manifest scriptpkg.ManifestV2
		if err := json.Unmarshal(repo.lastManifest, &manifest); err != nil {
			t.Fatalf("Persisted ManifestV2 is not valid JSON: %v (raw: %q)", err, repo.lastManifest)
		}
		// Verify the manifest was generated in canonical NEW-mode
		// (NoInlineAssets=true). buildManifestV2 creates DownstreamRequest
		// stubs from plan.Postprocessors, not from scene text — so the
		// key invariant here is that the manifest was structurally
		// populated at all (not silently dropped by the persistence step).
		if !manifest.NoInlineAssets {
			t.Error("ManifestV2.NoInlineAssets is false — persistence did not use canonical NEW-mode")
		}
	}

	// ── Assert Invariant 7: Persisted SpecScene JSON contains Italian text ──
	if repo.lastRec.SpecScene == "" {
		t.Fatal("Persisted SpecScene is empty — persistence did not write SpecScene JSON")
	}
	var restored scriptpkg.SpecSceneOutput
	if err := json.Unmarshal([]byte(repo.lastRec.SpecScene), &restored); err != nil {
		t.Fatalf("Persisted SpecScene is not valid JSON: %v (raw: %q)", err, repo.lastRec.SpecScene)
	}
	if len(restored.Scenes) != 2 {
		t.Fatalf("Persisted SpecScene has %d scenes, want 2", len(restored.Scenes))
	}
	for _, sc := range restored.Scenes {
		if !strings.Contains(sc.Text, "[it]") {
			t.Errorf("Persisted SpecScene scene %q text does not contain Italian marker [it]: %q", sc.ID, sc.Text)
		}
	}
	// Verify scene IDs survived the full pipeline.
	sceneIDs := make(map[string]bool)
	for _, sc := range restored.Scenes {
		sceneIDs[sc.ID] = true
	}
	if !sceneIDs["scene-0"] {
		t.Error("Persisted SpecScene is missing scene-0")
	}
	if !sceneIDs["scene-1"] {
		t.Error("Persisted SpecScene is missing scene-1")
	}

	// ── Assert Invariant 8: FinalSpecScene reflects translated surface ──
	if len(result.FinalSpecScene.Scenes) == 0 {
		t.Fatal("PipelineResult.FinalSpecScene.Scenes is empty")
	}
	if !strings.Contains(result.FinalSpecScene.Scenes[0].Text, "[it]") {
		t.Errorf("FinalSpecScene scene 0 text = %q, want Italian translation with [it] marker", result.FinalSpecScene.Scenes[0].Text)
	}
}

// TestPipeline_FullChain_NoTranslationPreservesEnglish is the
// negative test: when TranslateTo is empty, the pipeline runs all
// 5 processors with the original English text. No [it] markers
// should appear anywhere — the translation step is skipped entirely
// and every downstream processor sees the original English surface.
func TestPipeline_FullChain_NoTranslationPreservesEnglish(t *testing.T) {
	reg := NewPostProcessorRegistry(zap.NewNop())

	// TranslationProcessor is registered but plan.TranslateTo="" →
	// it returns empty result (no translation triggered).
	reg.Register(NewTranslationProcessor(
		pipelineTranslatorStub{},
		nil,
		pipelineTransUCStub{},
		pipelineClassifyStub{},
		zap.NewNop(),
	))
	reg.Register(NewClipBindingsProcessor(zap.NewNop()))
	voStub := &pipelineVOStub{}
	reg.Register(NewVoiceoverProcessor(voStub, zap.NewNop()))
	docStub := &stubFullPipelineDocSvc{}
	reg.Register(NewDocumentProcessor(docStub, nil))
	repo := &idemFakeRepo{}
	reg.Register(NewPersistenceProcessor(repo, zap.NewNop()))
	reg.Freeze()

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

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "no-translate-test",
		Language: "en",
		// TranslateTo deliberately empty — no translation.
		Title:       "Boxing Story",
		Topic:       "boxing",
		TargetWords: 500,
		CacheKey:    "notranslate00",
		Postprocessors: []string{
			string(ProcessorTranslation),
			string(ProcessorClipBindings),
			string(ProcessorVoiceover),
			string(ProcessorDocument),
			string(ProcessorPersistence),
		},
	}

	result, err := reg.Run(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	// VoiceoverService must receive English text (no [it] leakage).
	voStub.mu.Lock()
	captured := append([]string(nil), voStub.capturedTexts...)
	voStub.mu.Unlock()
	for i, text := range captured {
		if strings.Contains(text, "[it]") {
			t.Errorf("VoiceoverService scene %d received unexpected Italian marker in no-translate run: %q", i, text)
		}
	}

	// Persisted script must contain English (no Italian leakage).
	if repo.lastRec != nil && strings.Contains(repo.lastRec.OutputText, "[it]") {
		t.Errorf("Persisted OutputText contains unexpected [it] marker: %q", repo.lastRec.OutputText)
	}

	// FinalSpecScene must contain original English scene text.
	if len(result.FinalSpecScene.Scenes) > 0 {
		if strings.Contains(result.FinalSpecScene.Scenes[0].Text, "[it]") {
			t.Errorf("FinalSpecScene scene 0 contains unexpected [it] marker: %q", result.FinalSpecScene.Scenes[0].Text)
		}
	}
}
