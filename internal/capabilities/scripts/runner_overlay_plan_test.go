// Package scriptgeneration — runner_overlay_plan_test.go certifies the
// production overlay derivation on REAL jobs: after a complete
// COMBINED_TIMELINE run, GenerateResult carries the semantic OverlayPlan
// covering the full nine-template vocabulary (IMPORTANT_PHRASE,
// IMPORTANT_WORD, IMAGE_OVERLAY, PERSON, NUMBER, QUOTE, LOCATION, PRODUCT,
// LOGO), every item anchored to the real voiceover word timing (never a
// text-length estimate), and every template terminating in one of the four
// canonical primitives (Text / Image / Video / Shape) when compiled to
// chronon.render-plan.v1.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// overlayScene0Annotations returns the semantic surface of scene-0, whose
// text (16 words, 100ms each) is:
//
//	"Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."
//
// word index: Tim(0) Cook(1) said(2) that(3) Apple(4) changed(5) everything(6)
// in(7) Cupertino(8) and(9) sold(10) ten(11) million(12) Vision(13) Pro(14)
// units(15)  →  global spans: Tim Cook 0.0–0.2s, Apple 0.4–0.5s,
// changed everything 0.5–0.7s, Cupertino 0.8–0.9s, ten million 1.1–1.3s,
// Vision Pro 1.3–1.5s.
func overlayScene0Annotations() *scriptpkg.SceneAnnotations {
	return &scriptpkg.SceneAnnotations{
		Version:  1,
		Language: "en",
		Status:   "completed",
		ImportantPhrases: []scriptpkg.AnnotationSpan{
			{Text: "changed everything", Score: 0.8},
		},
		ImportantWords: []scriptpkg.AnnotationSpan{
			{Text: "Apple", Score: 1.0},
			{Text: "Cupertino", Score: 0.5},
		},
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{
				ID: "entity-tim-cook", CanonicalName: "Tim Cook", Type: "PERSON", Confidence: 0.98,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "tim-cook-photo", PreviewURL: "https://cdn.example.com/tim-cook.jpg"},
			},
			{
				ID: "entity-apple", CanonicalName: "Apple", Type: "LOGO", Confidence: 0.97,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "apple-logo", PreviewURL: "https://cdn.example.com/apple-logo.png"},
			},
			{ID: "entity-cupertino", CanonicalName: "Cupertino", Type: "GPE", Confidence: 0.9},
		},
		SecondaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-change-everything", CanonicalName: "changed everything", Type: "QUOTE", Confidence: 0.85},
			{ID: "entity-ten-million", CanonicalName: "ten million", Type: "CARDINAL", Confidence: 0.9},
			{
				ID: "entity-vision-pro", CanonicalName: "Vision Pro", Type: "PRODUCT", Confidence: 0.95,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "vision-pro", PreviewURL: "https://cdn.example.com/vision-pro.png"},
			},
		},
	}
}

func overlayScene1Annotations() *scriptpkg.SceneAnnotations {
	return &scriptpkg.SceneAnnotations{
		Version:  1,
		Language: "en",
		Status:   "completed",
		ImportantPhrases: []scriptpkg.AnnotationSpan{
			{Text: "Growth matters", Score: 0.8},
		},
		ImportantWords: []scriptpkg.AnnotationSpan{
			{Text: "Growth", Score: 1.0},
		},
	}
}

// TestRunner_OverlayPlanAllNineSemanticEntities certifies the durable runner
// wiring: after a complete COMBINED_TIMELINE run with real word timing, the
// result carries the semantic OverlayPlan with the full nine-template
// vocabulary, each item timestamped from the certified word timing and each
// template compiling to a canonical primitive (Text / Image / Video / Shape).
//
// Scene-0 offset 0s (16 words × 100ms = 1.6s), scene-1 offset 1.6s
// (5 words × 100ms = 0.5s):
//
//	scene-0 "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."
//	    IMPORTANT_PHRASE "changed everything"  f(0.5–0.7s) → Text(title_centered)
//	    IMPORTANT_WORD   "Apple"               f(0.4–0.5s) → Text(kinetic_word)
//	    IMPORTANT_WORD   "Cupertino"           f(0.8–0.9s) → Text(kinetic_word)
//	    IMAGE_OVERLAY    tim-cook-photo        f(0.0–0.2s) → Image(contain)
//	    PERSON           "Tim Cook"            f(0.0–0.2s) → Text(entity_card)
//	    LOCATION         "Cupertino"           f(0.8–0.9s) → Text(entity_card)
//	    NUMBER           "ten million"         f(1.1–1.3s) → Text(number)
//	    QUOTE            "changed everything"  f(0.5–0.7s) → Text(quote)
//	    PRODUCT          "Vision Pro" + image  f(1.3–1.5s) → Image(contain)
//	    LOGO             "Apple" + image       f(0.4–0.5s) → Image(contain)
//
//	scene-1 "Growth matters more than ever."
//	    IMPORTANT_PHRASE "Growth matters"      f(1.6–1.8s) → Text(title_centered)
//	    IMPORTANT_WORD   "Growth"              f(1.6–1.7s) → Text(kinetic_word)
func TestRunner_OverlayPlanAllNineSemanticEntities(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{
		{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
			Annotations: overlayScene0Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
		{
			ID: "scene-1", Index: 1,
			Text:        map[Language]string{"en": "Growth matters more than ever."},
			Annotations: overlayScene1Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
	})
	docPub := newStubDocumentPublisher()
	runner := NewRunner(repo, textGen, newStubTranslator(), &entityTimelineVoiceoverGenerator{}, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-cert"

	runID := "run-overlay-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res)
	require.NotNil(t, res.OverlayPlan, "overlay plan must be projected on a real timed run")
	require.NoError(t, res.OverlayPlan.Validate())

	// ── The full nine-template vocabulary ───────────────────────────
	byID := map[string]capabilityoverlay.OverlayItem{}
	templates := map[string]bool{}
	for _, item := range res.OverlayPlan.Items {
		byID[item.ID] = item
		templates[item.TemplateID] = true
	}
	for _, want := range []string{
		"IMPORTANT_PHRASE", "IMPORTANT_WORD", "IMAGE_OVERLAY",
		"person_default", "gpe_default", "NUMBER", "QUOTE", "PRODUCT", "LOGO",
	} {
		require.True(t, templates[want], "plan must carry template %q (got %v)", want, templates)
	}

	// ── Certified timing (real word boundaries, ms floor/ceil) ──────
	phrase := byID["scene-0-phrase-changed-everything"]
	require.Equal(t, "IMPORTANT_PHRASE", phrase.TemplateID)
	require.Equal(t, int64(500), phrase.StartMs, "phrase starts at word 5")
	require.Equal(t, int64(700), phrase.EndMs, "phrase ends at word 7")

	keyword := byID["scene-0-keyword-apple"]
	require.Equal(t, "IMPORTANT_WORD", keyword.TemplateID)
	require.Equal(t, int64(400), keyword.StartMs, "Apple is word 4")

	image := byID["scene-0-image-tim-cook-photo"]
	require.Equal(t, "IMAGE_OVERLAY", image.TemplateID)
	require.Equal(t, int64(0), image.StartMs)
	require.Equal(t, int64(200), image.EndMs)
	require.Len(t, image.AssetRefs, 1)
	require.Equal(t, "tim-cook-photo", image.AssetRefs[0].AssetID)
	require.Equal(t, "https://cdn.example.com/tim-cook.jpg", image.AssetRefs[0].URL)

	person := byID["overlay-scene-0-tim-cook"]
	require.Equal(t, "person_default", person.TemplateID)
	require.Equal(t, "Tim Cook", person.Text)
	require.Equal(t, int64(0), person.StartMs)
	require.Equal(t, int64(200), person.EndMs)

	location := byID["overlay-scene-0-cupertino"]
	require.Equal(t, "gpe_default", location.TemplateID)
	require.Equal(t, int64(800), location.StartMs)
	require.Equal(t, int64(900), location.EndMs)

	number := byID["scene-0-number-ten-million"]
	require.Equal(t, "NUMBER", number.TemplateID)
	require.Equal(t, "ten million", number.Text)
	require.Equal(t, int64(1100), number.StartMs)
	require.Equal(t, int64(1300), number.EndMs)

	quote := byID["scene-0-quote-changed-everything"]
	require.Equal(t, "QUOTE", quote.TemplateID)
	require.Equal(t, "changed everything", quote.Text)
	require.Equal(t, int64(500), quote.StartMs)
	require.Equal(t, int64(700), quote.EndMs)

	product := byID["scene-0-product-vision-pro"]
	require.Equal(t, "PRODUCT", product.TemplateID)
	require.Equal(t, int64(1300), product.StartMs)
	require.Equal(t, int64(1500), product.EndMs)
	require.Len(t, product.AssetRefs, 1)
	require.Equal(t, "vision-pro", product.AssetRefs[0].AssetID)

	logo := byID["scene-0-logo-apple-logo"]
	require.Equal(t, "LOGO", logo.TemplateID)
	require.Equal(t, int64(400), logo.StartMs)
	require.Equal(t, int64(500), logo.EndMs)
	require.Len(t, logo.AssetRefs, 1)
	require.Equal(t, "apple-logo", logo.AssetRefs[0].AssetID)

	// Multi-scene: scene-1 (offset 1.6s) contributes its own phrase/word.
	scene1Phrase := byID["scene-1-phrase-growth-matters"]
	require.Equal(t, int64(1600), scene1Phrase.StartMs, "scene-1 phrase starts at the scene offset")
	require.Equal(t, int64(1800), scene1Phrase.EndMs)

	// No entity is rendered twice: "changed everything", "ten million",
	// "Vision Pro" and "Apple" must NOT also appear as concept cards.
	require.NotContains(t, templates, "concept_default", "planner-owned entities must not be duplicated as concept cards")

	// ── Compilation into canonical primitives ───────────────────────
	compiled, err := capabilityoverlay.CompileChrononPlan(*res.OverlayPlan)
	require.NoError(t, err)
	layerByID := map[string]capabilityoverlay.ChrononLayer{}
	for _, layer := range compiled.Plan.Layers {
		layerByID[layer.ID] = layer
	}
	// Every layer type is one of the four canonical primitives' Chronon
	// spellings (text / image / video / color).
	for _, layer := range compiled.Plan.Layers {
		require.Contains(t, []string{"text", "image", "video", "color"}, layer.Type, "layer %q must terminate in a canonical primitive", layer.ID)
	}
	require.Equal(t, "text", layerByID["scene-0-phrase-changed-everything"].Type)
	require.Equal(t, "title_centered", layerByID["scene-0-phrase-changed-everything"].Preset)
	require.Equal(t, "text", layerByID["scene-0-keyword-apple"].Type)
	require.Equal(t, "kinetic_word", layerByID["scene-0-keyword-apple"].Preset)
	require.Equal(t, "image", layerByID["scene-0-image-tim-cook-photo"].Type)
	require.Equal(t, "contain", layerByID["scene-0-image-tim-cook-photo"].Fit)
	require.Equal(t, "text", layerByID["overlay-scene-0-tim-cook"].Type)
	require.Equal(t, "entity_card", layerByID["overlay-scene-0-tim-cook"].Preset)
	require.Equal(t, "text", layerByID["overlay-scene-0-cupertino"].Type)
	require.Equal(t, "text", layerByID["scene-0-number-ten-million"].Type)
	require.Equal(t, "number", layerByID["scene-0-number-ten-million"].Preset)
	require.Equal(t, "text", layerByID["scene-0-quote-changed-everything"].Type)
	require.Equal(t, "quote", layerByID["scene-0-quote-changed-everything"].Preset)
	require.Equal(t, "image", layerByID["scene-0-product-vision-pro"].Type)
	require.Equal(t, "image", layerByID["scene-0-logo-apple-logo"].Type)
	// The canonical font rides along with every text layer.
	require.Equal(t, capabilityoverlay.CanonicalTextFontPath, layerByID["scene-0-phrase-changed-everything"].Font)
}

// TestCompileOverlayPlan_NilOrSurfacelessIsNoOp certifies the no-op contract:
// a nil result, or a result without any certified timing surface, derives no
// plan (nil, not an error) — the same legitimate no-op the phrase and entity
// projections implement.
func TestCompileOverlayPlan_NilOrSurfacelessIsNoOp(t *testing.T) {
	plan, err := CompileOverlayPlan(nil, "en", DefaultOverlayCanvas, "plan-1", "video-1", "")
	require.NoError(t, err)
	require.Nil(t, plan)

	// A result whose scenes carry NO voiceover timing contributes nothing.
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Tim Cook speaks."}, Annotations: overlayScene0Annotations()},
	}}
	plan, err = CompileOverlayPlan(result, "en", DefaultOverlayCanvas, "plan-1", "video-1", "")
	require.NoError(t, err)
	require.Nil(t, plan)
}

// TestCompileOverlayPlan_UnspokenPhraseSkipped certifies that a phrase which
// the voiceover did NOT speak verbatim is skipped (never timestamped), while
// the words that ARE spoken still project. Timing here is the real word
// boundary artifact of "Tim Cook speaks." (3 words × 100ms).
func TestCompileOverlayPlan_UnspokenPhraseSkipped(t *testing.T) {
	words := []capabilityaudio.SpeechWordTiming{
		{Index: 0, Text: "Tim", StartUS: 0, EndUS: 100_000},
		{Index: 1, Text: "Cook", StartUS: 100_000, EndUS: 200_000},
		{Index: 2, Text: "speaks", StartUS: 200_000, EndUS: 300_000},
	}
	timing := capabilityaudio.SpeechTimingArtifact{
		Version: capabilityaudio.SpeechTimingVersion, Provider: "edge_tts",
		BoundaryMode: capabilityaudio.BoundaryWord, Language: "en",
		TextSHA256: "text-hash", AudioSHA256: "audio-hash",
		DurationUS: 300_000, Words: words,
	}
	result := &GenerateResult{
		Scenes: []Scene{{
			ID: "scene-0", Index: 0,
			Text: map[Language]string{"en": "Tim Cook speaks."},
			Voiceover: map[Language]AudioReference{
				"en": {ID: "vo-scene-0-en", Duration: 0.3, Timing: &timing},
			},
			Annotations: &scriptpkg.SceneAnnotations{
				Version: 1, Language: "en", Status: "completed",
				ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "never spoken anywhere", Score: 0.8}},
				ImportantWords:   []scriptpkg.AnnotationSpan{{Text: "Cook", Score: 1.0}},
			},
		}},
		ResolvedScenes: []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 300_000}},
	}
	plan, err := CompileOverlayPlan(result, "en", DefaultOverlayCanvas, "plan-1", "video-1", "")
	require.NoError(t, err)
	require.NotNil(t, plan, "the spoken keyword must still produce a plan")
	require.Len(t, plan.Items, 1, "unspoken phrase skipped, spoken keyword kept")
	require.Equal(t, "IMPORTANT_WORD", plan.Items[0].TemplateID)
	require.Equal(t, "Cook", plan.Items[0].Text)
	require.Equal(t, int64(100), plan.Items[0].StartMs)
	require.Equal(t, int64(200), plan.Items[0].EndMs)
}

// TestOverlaySceneInput_SelectsOnlySceneRelevantImages certifies GOLDEN 03's
// semantic scene-entity selection: an entity image is projected as an
// IMAGE_OVERLAY candidate only when the entity has a certified occurrence in
// the scene. An off-scene entity with a bound image ("Tesla" when the scene
// is about Tim Cook) is skipped — never selected, never rendered.
func TestOverlaySceneInput_SelectsOnlySceneRelevantImages(t *testing.T) {
	scene := Scene{
		ID: "scene-0", Index: 0,
		Annotations: &scriptpkg.SceneAnnotations{
			Version: 1, Language: "en", Status: "completed",
			PrimaryEntities: []scriptpkg.AnnotatedEntity{
				{ID: "e-tim", CanonicalName: "Tim Cook", Type: "PERSON", Confidence: 0.98,
					Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "tim-cook", PreviewURL: "https://cdn.example.com/tim-cook.jpg"}},
				{ID: "e-tesla", CanonicalName: "Tesla", Type: "ORG", Confidence: 0.9,
					Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "tesla", PreviewURL: "https://cdn.example.com/tesla.png"}},
			},
		},
	}
	occurrences := []capabilityentities.EntityOccurrence{
		{EntityID: capabilityentities.SafeEntityID("Tim Cook"), AudioStartUS: 0, AudioEndUS: 200_000},
	}

	out, err := overlaySceneInput(scene, capabilityaudio.SpeechTimingArtifact{}, 0, occurrences)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, out.Images, 1, "only the scene-relevant entity image must be selected")
	require.Equal(t, "tim-cook", out.Images[0].AssetID)
	for _, img := range out.Images {
		require.NotEqual(t, "tesla", img.AssetID, "off-scene entity image must never be selected")
	}
}

// TestCompileOverlayPlan_RequiresPlanAndVideoID certifies the identity
// contract: a plan without plan_id/video_id fails closed — the queue job id
// IS the plan id, so it can never be empty.
func TestCompileOverlayPlan_RequiresPlanAndVideoID(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Tim Cook speaks."}, Annotations: overlayScene0Annotations()},
	}}
	_, err := CompileOverlayPlan(result, "en", DefaultOverlayCanvas, "", "video-1", "")
	require.Error(t, err)
	_, err = CompileOverlayPlan(result, "en", DefaultOverlayCanvas, "plan-1", "", "")
	require.Error(t, err)
}
