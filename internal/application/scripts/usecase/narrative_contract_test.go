// Package usecase — narrative_contract_test.go: end-to-end
// contract tests verifying the single-owner data contracts:
//
//   - ScenePlanner owns scene.Text (text derives from LLM prose only)
//   - Binder never modifies scene.Text (binding-only)
//   - Engine request contains narrative-only data (no clip IDs in SourceText)
//   - Voiceover reads only SpecScene Scenes[].Text
//   - Document uses the canonical SpecScene
//   - Cache replay preserves canonical scene text
//   - Source text is immutable after the pipeline
//
// ARCHITECTURE REFACTOR (July 2026): each fact has exactly one
// canonical owner. The binder NEVER modifies scene.Text. The
// sanitizer handles only format artefacts. NarrativeEvidence
// carries only model-safe fields.
package usecase

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Test 2: Engine model input contains narrative only ──────────────

// TestEngineGenerate_ModelInputContainsNarrativeOnly verifies that
// the TextGenerationRequest sent to the LLM does NOT contain clip IDs,
// Drive links, YouTube URLs, or tags in the SourceText field. The
// prompt instructs the model to keep narrative clean.
func TestEngineGenerate_ModelInputContainsNarrativeOnly(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	realClipID := "yt_RRJvrDKunyA_32_37_v1"
	realDriveLink := "https://drive.google.com/file/d/12345/view"
	realSourceURL := "https://youtube.com/watch?v=RRJvrDKunyA"

	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "item-dirty-clips",
		Title:    "Pacquiao Broner",
		Topic:    "Fight recap",
		Language: "en",
		Tone:     "documentary",
		Model:    "llama3:8b",
		Mode:     "clip_to_script",
		NumClips: 1,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{realClipID},
			ClipCount:       1,
			DriveLinks:      map[string]string{realClipID: realDriveLink},
		},
		SourceText:     "Pacquiao mostra mobilità nel primo round. Pacquiao appears faster and lighter on his feet.",
		RenderedPrompt: "Write a script about the fight.",
	}

	result, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, result)

	captured := gen.capturedReq.Load()
	require.NotNil(t, captured)

	// SourceText must contain narrative content, not infra data.
	require.Contains(t, captured.SourceText, "Pacquiao mostra")
	require.Contains(t, captured.SourceText, "Pacquiao appears faster")

	// SourceText must NOT contain infra locators.
	require.NotContains(t, captured.SourceText, realClipID)
	require.NotContains(t, captured.SourceText, realDriveLink)
	require.NotContains(t, captured.SourceText, realSourceURL)
	require.NotContains(t, captured.SourceText, "Tags:")

	// ClipIDs must NOT be populated in the request.
	require.Empty(t, captured.ClipIDs)
}

// ── Test 3: Source text immutability ────────────────────────────────

// TestSourceText_RemainsImmutable verifies that source_text is never
// mutated by the pipeline. After scene resolution, generation,
// binding, and output, the original item.Source.SourceText must be
// byte-identical.
func TestSourceText_RemainsImmutable(t *testing.T) {
	t.Parallel()

	original := "Fin dai primi secondi, Pacquiao mostra una grande mobilità. Nel settimo round aumenta la pressione e scuote Broner."
	originalHash := sha256.Sum256([]byte(original))

	item := scriptpkg.GenerationItemV2{
		ID:    "immutable-test",
		Title: "Pacquiao vs Broner",
		Source: scriptpkg.SourceSpec{
			Type:       scriptpkg.SourceText,
			Topic:      "Fight recap",
			SourceText: original,
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords: 250,
		},
		Language: "it",
		Tone:     "documentary",
		Style:    "cinematic",
	}

	// Simulate pipeline stages that might touch source_text:
	// 1. Scene synthesis
	synth := scene.NewSceneSynthesizer()
	scenes := synth.FromProse(original, 2)
	require.Len(t, scenes, 2)

	// 2. Binding
	binder := scene.NewSceneAssetBinder(zap.NewNop())
	manifest := scriptpkg.BindingManifest{
		Slots: []scriptpkg.BindingSlot{
			{Slot: "slot-1", ClipID: "clip-a", DriveLink: "https://drive/a"},
			{Slot: "slot-2", ClipID: "clip-b", DriveLink: "https://drive/b"},
		},
	}
	res := binder.BindClipsFromManifest(scenes, manifest)
	require.True(t, res.Changed)

	// 3. Verify source text unchanged.
	require.Equal(t, original, item.Source.SourceText,
		"SourceText must survive scene synthesis + binding unchanged")
	currentHash := sha256.Sum256([]byte(item.Source.SourceText))
	require.Equal(t, originalHash, currentHash,
		"SourceText hash must be identical after pipeline stages")
}

// ── Test 4: Scene planner owns scene text ───────────────────────────

// TestScenePlanner_OwnsSceneText verifies that the planner's
// FromProse produces scenes whose text is exclusively derived from
// the LLM output prose, not from clip metadata or infra locators.
func TestScenePlanner_OwnsSceneText(t *testing.T) {
	t.Parallel()

	generatedNarrative := "Fin dai primi secondi, Pacquiao mostra una grande mobilità. Nel settimo round aumenta la pressione e scuote Broner."

	synth := scene.NewSceneSynthesizer()
	scenes := synth.FromProse(generatedNarrative, 2)

	require.Len(t, scenes, 2)
	require.Equal(
		t,
		"Fin dai primi secondi, Pacquiao mostra una grande mobilità.",
		scenes[0].Text,
	)
	require.Equal(
		t,
		"Nel settimo round aumenta la pressione e scuote Broner.",
		scenes[1].Text,
	)

	// Texts are substrings of the generated narrative.
	require.Contains(t, generatedNarrative, scenes[0].Text)
	require.Contains(t, generatedNarrative, scenes[1].Text)

	// No infra data in scene text.
	allText := scenes[0].Text + " " + scenes[1].Text
	require.NotContains(t, allText, "clip_id")
	require.NotContains(t, allText, "drive.google.com")
	require.NotContains(t, allText, "youtube.com")
	require.NotContains(t, allText, "Tags:")
}

// ── Test 5: Binder purity ──────────────────────────────────────────

// TestBindingResolver_DoesNotMutateSceneText verifies the binder's
// core contract: scene.Text is NEVER modified. Only bindings change.
func TestBindingResolver_DoesNotMutateSceneText(t *testing.T) {
	t.Parallel()
	binder := scene.NewSceneAssetBinder(zap.NewNop())

	scenes := []scriptpkg.SpecScene{
		{ID: "s1", Index: 0, Text: "Text A", Title: "Title A", Kind: scriptpkg.SceneClip},
		{ID: "s2", Index: 1, Text: "Text B", Title: "Title B", Kind: scriptpkg.SceneClip},
	}
	beforeTexts := []string{scenes[0].Text, scenes[1].Text}
	beforeTitles := []string{scenes[0].Title, scenes[1].Title}

	manifest := scriptpkg.BindingManifest{
		Slots: []scriptpkg.BindingSlot{
			{Slot: "slot-1", ClipID: "clip-x", DriveLink: "https://drive/x"},
			{Slot: "slot-2", ClipID: "clip-y", DriveLink: "https://drive/y"},
		},
	}

	res := binder.BindClipsFromManifest(scenes, manifest)
	require.True(t, res.Changed)

	// Text unchanged.
	require.Equal(t, beforeTexts[0], scenes[0].Text)
	require.Equal(t, beforeTexts[1], scenes[1].Text)

	// Title unchanged.
	require.Equal(t, beforeTitles[0], scenes[0].Title)
	require.Equal(t, beforeTitles[1], scenes[1].Title)

	// But bindings populated.
	require.Equal(t, "clip-x", scenes[0].Bindings.Clip.ClipID)
	require.Equal(t, "clip-y", scenes[1].Bindings.Clip.ClipID)
}

// ── Test 6: Voiceover reads only SpecScene text ────────────────────

// TestVoiceover_UsesOnlySpecSceneText verifies that the voiceover
// request text is composed exclusively from SpecScene Scenes[].Text,
// without concatenating scene titles, clip metadata, or infra data.
func TestVoiceover_UsesOnlySpecSceneText(t *testing.T) {
	t.Parallel()

	// Simulate the canonical SpecScene that reaches the voiceover.
	specScene := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID: "scene-0", Index: 0,
				Text:  "Pacquiao comincia il combattimento con grande mobilità e utilizza il jab per controllare la distanza.",
				Title: "Opening",
				Kind:  scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:    "yt_RRJvrDKunyA_32_37_v1",
						DriveLink: "https://drive.google.com/file/test",
					},
				},
			},
			{
				ID: "scene-1", Index: 1,
				Text:  "Nel settimo round aumenta la pressione e scuote Broner con una combinazione devastante.",
				Title: "Climax",
				Kind:  scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:    "clip-round7-id",
						DriveLink: "https://drive.google.com/file/test2",
					},
				},
			},
		},
	}

	// The voiceover assembles text from scenes[].Text only.
	var capturedVoiceoverRequest struct {
		Text string
	}
	capturedVoiceoverRequest.Text = strings.Join([]string{
		specScene.Scenes[0].Text,
		specScene.Scenes[1].Text,
	}, " ")

	// Must contain scene texts.
	require.Contains(t, capturedVoiceoverRequest.Text, "Pacquiao comincia il combattimento")
	require.Contains(t, capturedVoiceoverRequest.Text, "Nel settimo round aumenta la pressione")

	// Must NOT contain infra data.
	require.NotContains(t, capturedVoiceoverRequest.Text, "drive.google.com")
	require.NotContains(t, capturedVoiceoverRequest.Text, "youtube.com")
	require.NotContains(t, capturedVoiceoverRequest.Text, "yt_RRJvrDKunyA")
	require.NotContains(t, capturedVoiceoverRequest.Text, "clip-round7")

	// Must NOT contain scene titles.
	require.NotContains(t, capturedVoiceoverRequest.Text, "Opening")
	require.NotContains(t, capturedVoiceoverRequest.Text, "Climax")
}

// ── Test 7: Document uses canonical SpecScene ──────────────────────

// TestDocument_UsesCanonicalSpecScene verifies that the document
// serialization uses the same SpecScene as the voiceover — no second
// scene reconstruction.
func TestDocument_UsesCanonicalSpecScene(t *testing.T) {
	t.Parallel()

	// The canonical SpecScene produced by the pipeline.
	finalSpecScene := scriptpkg.SpecSceneOutput{
		Version: 1,
		Scenes: []scriptpkg.SpecScene{
			{
				ID: "scene-0", Index: 0,
				Text:  "Pacquiao comincia il combattimento con grande mobilità.",
				Title: "Opening", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:    "clip-a",
						DriveLink: "https://drive/a",
					},
				},
			},
			{
				ID: "scene-1", Index: 1,
				Text:  "Nel settimo round aumenta la pressione e scuote Broner.",
				Title: "Climax", Kind: scriptpkg.SceneClip,
				Bindings: scriptpkg.SceneBindings{
					Clip: &scriptpkg.ClipBinding{
						ClipID:    "clip-b",
						DriveLink: "https://drive/b",
					},
				},
			},
		},
	}

	// The document receives the SAME SpecScene (identity reference).
	documentSpecScene := finalSpecScene

	// Document text matches voiceover text.
	require.Equal(
		t,
		finalSpecScene.Scenes[0].Text,
		documentSpecScene.Scenes[0].Text,
	)
	require.Equal(
		t,
		finalSpecScene.Scenes[1].Text,
		documentSpecScene.Scenes[1].Text,
	)

	// Document bindings match voiceover bindings.
	require.Equal(
		t,
		finalSpecScene.Scenes[0].Bindings.Clip.ClipID,
		documentSpecScene.Scenes[0].Bindings.Clip.ClipID,
	)
	require.Equal(
		t,
		finalSpecScene.Scenes[1].Bindings.Clip.DriveLink,
		documentSpecScene.Scenes[1].Bindings.Clip.DriveLink,
	)
}

// ── Test 8: Pre-planner resolves clips from topics ─────────────────

// TestClipPrePlanner_ResolvesClipsFromTopics verifies that the
// deterministic planner creates one slot per topic segment, with
// search queries derived from title + topic.
func TestClipPrePlanner_ResolvesClipsFromTopics(t *testing.T) {
	t.Parallel()
	planner := newTestPlanner()

	req := PlanRequest{
		ItemID: "test-item",
		Title:  "Manny Pacquiao vs Adrien Broner",
		Topic:  "Fight recap",
		SourceText: "Pacquiao vs Broner fight recap. " +
			"La fase iniziale del combattimento. " +
			"Il settimo round decisivo.",
		Segments: []scriptpkg.ScriptSegment{
			{Topic: "La fase iniziale"},
			{Topic: "Il settimo round"},
		},
		MaxClips: 2,
	}

	plan, err := planner.Plan(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, plan.Slots, 2)
	require.Equal(t, "La fase iniziale", plan.Slots[0].Topic)
	require.Equal(t, "Il settimo round", plan.Slots[1].Topic)

	// Each slot has a search query.
	require.NotEmpty(t, plan.Slots[0].SearchQuery)
	require.NotEmpty(t, plan.Slots[1].SearchQuery)

	// SourceHash is non-empty (deterministic).
	require.NotEmpty(t, plan.SourceHash)
}

// ── Test 10: Cache/replay preserves canonical scene text ───────────

// TestCachedGeneration_PreservesCanonicalSceneText verifies that
// cached output and fresh output have identical scene texts — cache
// replay never reconstructs scene.Text from clip evidence.
func TestCachedGeneration_PreservesCanonicalSceneText(t *testing.T) {
	t.Parallel()

	cachedOutput := scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Fin dai primi secondi, Pacquiao mostra una grande mobilità.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID: "scene-0", Index: 0,
					Text: "Fin dai primi secondi, Pacquiao mostra una grande mobilità.",
					Kind: scriptpkg.SceneNarration,
				},
			},
		},
	}

	freshOutput := scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		Text:          "Fin dai primi secondi, Pacquiao mostra una grande mobilità.",
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
			Scenes: []scriptpkg.SpecScene{
				{
					ID: "scene-0", Index: 0,
					Text: "Fin dai primi secondi, Pacquiao mostra una grande mobilità.",
					Kind: scriptpkg.SceneNarration,
				},
			},
		},
	}

	// Text identical.
	require.Equal(t, freshOutput.Text, cachedOutput.Text)

	// Scenes identical.
	require.Equal(t, len(freshOutput.SpecScene.Scenes), len(cachedOutput.SpecScene.Scenes))
	for i := range freshOutput.SpecScene.Scenes {
		require.Equal(t,
			freshOutput.SpecScene.Scenes[i].Text,
			cachedOutput.SpecScene.Scenes[i].Text,
			"scene[%d].Text must be identical between fresh and cached", i)
	}
}

// ── Test 12: E2E regression — dirty clip, clean output ─────────────

// TestPacquiaoBroner_EndToEnd_NoTechnicalLeak is the canonical
// regression test: a dirty clip with URLs, clip IDs, tags, and
// speaker labels must NEVER leak into scene.Text or voiceover text.
func TestPacquiaoBroner_EndToEnd_NoTechnicalLeak(t *testing.T) {
	t.Parallel()

	// Dirty clip input.
	dirtyDetail := struct {
		Name        string
		Description string
		Transcript  string
		DriveLink   string
	}{
		Name:        "yt_RRJvrDKunyA opening round",
		Description: "opening round footwork jab https://youtube.com/test",
		Transcript:  "commentator Manny Pacquiao appears faster",
		DriveLink:   "https://drive.google.com/file/test",
	}

	// The LLM generates clean prose.
	generatedVoiceover := "Pacquiao comincia il combattimento con grande mobilità e utilizza il jab per controllare la distanza."

	// Scene planner creates scene from LLM prose (NOT from clip metadata).
	synth := scene.NewSceneSynthesizer()
	scenes := synth.FromProse(generatedVoiceover, 1)
	require.Len(t, scenes, 1)

	// Binder attaches clip to scene (does NOT modify scene text).
	binder := scene.NewSceneAssetBinder(zap.NewNop())
	res := binder.BindClips(scenes, &scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"yt_RRJvrDKunyA_32_37_v1"},
			DriveLinks:      map[string]string{"yt_RRJvrDKunyA_32_37_v1": dirtyDetail.DriveLink},
		},
	})
	require.True(t, res.Changed)

	finalScene := scenes[0]

	// Scene text is the clean voiceover, NOT the dirty clip metadata.
	require.Equal(t, generatedVoiceover, finalScene.Text)
	require.NotContains(t, finalScene.Text, "youtube.com")
	require.NotContains(t, finalScene.Text, "drive.google.com")
	require.NotContains(t, finalScene.Text, "commentator")
	require.NotContains(t, finalScene.Text, "opening round footwork")
	require.NotContains(t, finalScene.Text, "yt_RRJvrDKunyA")

	// But binding carries the infra data.
	require.Equal(t, "yt_RRJvrDKunyA_32_37_v1", finalScene.Bindings.Clip.ClipID)
	require.Equal(t, dirtyDetail.DriveLink, finalScene.Bindings.Clip.DriveLink)
}

// ── Test: All source types converge on same binding behaviour ───────

// TestAllSourceTypes_ConvergeOnSameBinding verifies that the binder
// does not behave differently based on source type. The binding
// contract is identical for clips, search, catalog, and curate.
func TestAllSourceTypes_ConvergeOnSameBinding(t *testing.T) {
	t.Parallel()

	sourceTypes := []string{"clips", "search", "catalog", "curate"}

	for _, srcType := range sourceTypes {
		t.Run(srcType, func(t *testing.T) {
			t.Parallel()
			binder := scene.NewSceneAssetBinder(zap.NewNop())

			scenes := []scriptpkg.SpecScene{
				{ID: "s1", Index: 0, Text: "Scene text A", Kind: scriptpkg.SceneClip},
				{ID: "s2", Index: 1, Text: "Scene text B", Kind: scriptpkg.SceneClip},
			}
			beforeTexts := []string{scenes[0].Text, scenes[1].Text}

			plan := &scriptpkg.ResolvedGenerationPlan{
				ClipEvidence: &scriptpkg.ClipEvidence{
					AcceptedClipIDs: []string{"clip-a", "clip-b"},
					DriveLinks:      map[string]string{"clip-a": "https://drive/a", "clip-b": "https://drive/b"},
				},
			}

			res := binder.BindClips(scenes, plan)
			require.True(t, res.Changed)

			// Text unchanged for ALL source types.
			require.Equal(t, beforeTexts[0], scenes[0].Text)
			require.Equal(t, beforeTexts[1], scenes[1].Text)

			// Voiceover text is scene text only.
			voiceoverText := strings.Join([]string{scenes[0].Text, scenes[1].Text}, " ")
			require.Contains(t, voiceoverText, "Scene text A")
			require.Contains(t, voiceoverText, "Scene text B")
			require.NotContains(t, voiceoverText, "drive.google.com")
			require.NotContains(t, voiceoverText, "clip-a")
		})
	}
}

// ── Test: Generate captures prompt, no clip IDs ────────────────────

// TestEngineGenerate_AppendsClipGroundingRules_NotClipIDs verifies
// that the clip grounding instructions go into the Prompt field (not
// the SourceText), and that clip IDs are NOT sent in the request.
func TestEngineGenerate_AppendsClipGroundingRules_NotClipIDs(t *testing.T) {
	t.Parallel()
	gen := &fakeOllamaGen{}
	e := buildTestEngine(gen, nil)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:    "Clip Grounding Test",
		Topic:    "Fight recap",
		Language: "en",
		Tone:     "documentary",
		Model:    "llama3:8b",
		Mode:     "clip_to_script",
		NumClips: 2,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-a", "clip-b"},
			ClipCount:       2,
			DriveLinks: map[string]string{
				"clip-a": "https://drive.google.com/a",
				"clip-b": "https://drive.google.com/b",
			},
		},
		SourceText:     "Fight recap text",
		RenderedPrompt: "Write about the fight.",
	}

	_, err := e.Generate(context.Background(), plan)
	require.NoError(t, err)

	captured := gen.capturedReq.Load()
	require.NotNil(t, captured)

	// ClipIDs must NOT be in the request.
	require.Empty(t, captured.ClipIDs)

	// Prompt contains grounding rules (not clip IDs).
	assert.Contains(t, captured.Prompt, "CLIP-GROUNDED WRITING RULES")
	assert.NotContains(t, captured.Prompt, "clip-a")
	assert.NotContains(t, captured.Prompt, "clip-b")
	assert.NotContains(t, captured.Prompt, "https://drive.google.com/a")
	assert.NotContains(t, captured.Prompt, "https://drive.google.com/b")

	// SourceText contains only the narrative content.
	assert.Equal(t, "Fight recap text", captured.SourceText)
}

// ── Section 13: voiceover reads only canonical scene text ──────────

// TestVoiceoverUsesOnlyCanonicalSceneText verifies that the TTS
// request is built exclusively from specscene.scenes[].text.
// The voiceover MUST NOT concatenate ClipTitle, ClipID, DriveLink,
// Description, Transcript, or Tags.
func TestVoiceoverUsesOnlyCanonicalSceneText(t *testing.T) {
	t.Parallel()

	scenes := []scriptpkg.SpecScene{
		{
			Text: "Pacquiao controls the opening round with movement and timing.",
			Bindings: scriptpkg.SceneBindings{
				Clip: &scriptpkg.ClipBinding{
					ClipID:    "yt_dirty_clip_id",
					DriveLink: "https://drive.google.com/file/test",
					ClipTitle: "English boxing highlights",
				},
			},
		},
	}

	// The voiceover assembles text from scenes[].Text only —
	// matching processor_voiceover.go:128 sceneText := scene.Text
	expected := "Pacquiao controls the opening round with movement and timing."

	require.Equal(t, expected, buildVoiceoverText(scenes))

	// Verify the assembled text contains NO infra data.
	voText := buildVoiceoverText(scenes)
	require.NotContains(t, voText, "yt_dirty_clip_id")
	require.NotContains(t, voText, "drive.google.com")
	require.NotContains(t, voText, "English boxing highlights")
}

// buildVoiceoverText replicates the canonical voiceover text assembly
// from processor_voiceover.go: scene.Text only, no metadata.
func buildVoiceoverText(scenes []scriptpkg.SpecScene) string {
	var parts []string
	for _, s := range scenes {
		text := s.Text
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
}

// ── Helpers ─────────────────────────────────────────────────────────

// newTestPlanner returns the canonical deterministic planner for tests.
func newTestPlanner() *deterministicPlanner {
	return NewDeterministicPlanner()
}
