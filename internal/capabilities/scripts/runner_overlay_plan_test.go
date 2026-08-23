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
	"errors"
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
				Image: &scriptpkg.EntityImageBinding{
					Status: "bound", AssetID: "tim-cook-photo",
					PreviewURL: "https://cdn.example.com/tim-cook.jpg",
					// The content address is what promotes the binding into the
					// EntityMediaIndex, so the person card carries the asset.
					SHA256: "aa11bb22cc33dd44ee55ff66778899aabbccddeeff00112233445566778899aabb",
				},
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
//	    PERSON + image   "Tim Cook"            f(0.0–0.2s) → Text(entity_card) with tim-cook-photo asset
//	    LOCATION         "Cupertino"           f(0.8–0.9s) → Text(entity_card)
//	    NUMBER           "ten million"         f(1.1–1.3s) → Text(number)
//	    QUOTE            "changed everything"  f(0.5–0.7s) → Text(quote)
//	    PRODUCT          "Vision Pro" + image  f(1.3–1.5s) → Image(contain)
//	    LOGO             "Apple" + image       f(0.4–0.5s) → Image(contain)
//
//	scene-1 "Growth matters more than ever."
//	    IMPORTANT_PHRASE "Growth matters"      f(1.6–1.8s) → Text(title_centered)
//	    IMPORTANT_WORD   "Growth"              f(1.6–1.7s) → Text(kinetic_word)
func TestRunner_OverlayPlanAllSemanticEntities(t *testing.T) {
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
	runner.SetOverlayCanvas(GoldenOverlayCanvas)

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

	// The verbose report is intentionally emitted from the certified plan so
	// operators can audit the exact preset and render key selected for every
	// item, rather than only the allowed preset families.
	for _, item := range res.OverlayPlan.Items {
		assets := make([]string, 0, len(item.AssetRefs))
		for _, asset := range item.AssetRefs {
			assets = append(assets, asset.AssetID)
		}
		t.Logf("OVERLAY_PLAN_TABLE id=%s entity_id=%s kind=%s timing=%d+%dus template=%s preset=%s asset=%v render_key=%s text=%q", item.ID, item.EntityID, item.Kind, item.StartUS, item.DurationUS, item.TemplateID, item.PresetID, assets, item.RenderKey, item.Text)
	}

	// ── The full semantic vocabulary ────────────────────────────────
	byID := map[string]capabilityoverlay.OverlayItem{}
	templates := map[string]bool{}
	for _, item := range res.OverlayPlan.Items {
		byID[item.ID] = item
		templates[item.TemplateID] = true
	}
	for _, want := range []string{
		"IMPORTANT_PHRASE", "IMPORTANT_WORD",
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

	// The chosen entity IS the card with its image asset: Tim Cook's photo
	// renders on the person_default card (resolved via the canonical id →
	// EntityMediaResolver path), never as a duplicate IMAGE_OVERLAY.
	person := byID["overlay-scene-0-tim-cook"]
	require.Equal(t, "person_default", person.TemplateID)
	require.Equal(t, "Tim Cook", person.Text)
	require.Equal(t, int64(0), person.StartMs)
	require.Equal(t, int64(200), person.EndMs)
	require.Len(t, person.AssetRefs, 1)
	require.Equal(t, "tim-cook-photo", person.AssetRefs[0].AssetID)
	require.Equal(t, "https://cdn.example.com/tim-cook.jpg", person.AssetRefs[0].URL)
	require.Equal(t, "person:tim-cook", person.EntityRef.CanonicalEntityID)
	require.NotContains(t, templates, "IMAGE_OVERLAY", "entity-card images must not render twice (the card carries the asset)")

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
	// Preset-driven layers carry NO type (Chronon derives it from
	// supported_layer); preset-less primitives (PRODUCT / LOGO) still carry
	// their image/video/color type.
	for _, layer := range compiled.Plan.Layers {
		if layer.Type == "" {
			require.NotEmpty(t, layer.Preset, "layer %q must carry a preset when its type is Chronon-derived", layer.ID)
			continue
		}
		require.Contains(t, []string{"image", "video", "color"}, layer.Type, "layer %q must terminate in a canonical primitive", layer.ID)
	}
	require.Equal(t, "", layerByID["scene-0-phrase-changed-everything"].Type)
	require.Contains(t, []string{"fast_fade_through", "clean_slide_up", "slide_lateral", "phrase_word_reveal", "undertext_pop"}, layerByID["scene-0-phrase-changed-everything"].Preset)
	require.Equal(t, "", layerByID["scene-0-keyword-apple"].Type)
	require.Contains(t, []string{"snap_scale", "fast_fade_through", "phrase_word_reveal"}, layerByID["scene-0-keyword-apple"].Preset)
	require.Equal(t, "", layerByID["overlay-scene-0-tim-cook"].Type)
	require.Contains(t, []string{"name_glow_typewriter", "name_glow_slide", "name_glow_pop"}, layerByID["overlay-scene-0-tim-cook"].Preset)
	require.NotEmpty(t, layerByID["overlay-scene-0-tim-cook"].Asset, "the chosen entity card must carry its resolved image asset")
	require.Equal(t, "", layerByID["overlay-scene-0-cupertino"].Type)
	require.Equal(t, "", layerByID["scene-0-number-ten-million"].Type)
	require.Contains(t, []string{"snap_scale", "fast_fade_through", "phrase_word_reveal"}, layerByID["scene-0-number-ten-million"].Preset)
	require.Equal(t, "", layerByID["scene-0-quote-changed-everything"].Type)
	require.Contains(t, []string{"fast_fade_through", "clean_slide_up", "slide_lateral", "phrase_word_reveal", "undertext_pop"}, layerByID["scene-0-quote-changed-everything"].Preset)
	require.Equal(t, "image", layerByID["scene-0-product-vision-pro"].Type)
	require.Equal(t, "image", layerByID["scene-0-logo-apple-logo"].Type)
	// The font is Chronon-owned (VisualPresetRegistry font_asset); PipelineGen
	// text layers carry no font/font_size.
	require.Equal(t, "", layerByID["scene-0-phrase-changed-everything"].Font)
}

// TestRunner_OverlayIntents_PersistedBeforePlanEnqueue certifies the
// pre-timing entity→template binding: with the canonical registry wired via
// SetOverlayRegistry, the durable result carries one OverlayIntent per
// entity occurrence — created immediately after extraction, BEFORE the audio
// phase compiles the timed OverlayPlan — with its template_id already
// resolved through the single registry. Every intent is PENDING (no timing
// invented) and its template_id matches the final timed plan item, proving
// the template choice is persisted before any render job is enqueued.
func TestRunner_OverlayIntents_PersistedBeforePlanEnqueue(t *testing.T) {
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
	// The canonical registry is wired the same way the composition root does.
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	runner.SetOverlayCanvas(GoldenOverlayCanvas)

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-intent-cert"

	runID := "run-overlay-intents-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res)

	// ── Pre-timing intents exist on the durable result, before the plan ──
	require.NotEmpty(t, res.OverlayIntents, "overlay intents must be created immediately after extraction")
	templateByEntity := map[string]string{}
	for _, intent := range res.OverlayIntents {
		require.Equal(t, capabilityoverlay.TimingStatePending, intent.TimingState, "intent %q must be pre-timing (PENDING)", intent.IntentID)
		require.NoError(t, intent.Validate())
		if intent.Entity.CanonicalName != "" {
			templateByEntity[intent.Entity.CanonicalName] = intent.TemplateID
		}
	}
	// The template_id is resolved through the single registry at intent
	// creation time (before TTS / before the timed OverlayPlan): the same
	// entity type always binds to the same canonical template.
	want := map[string]string{
		"Tim Cook":           "person_default",
		"Apple":              "LOGO",
		"Cupertino":          "gpe_default",
		"changed everything": "quote",
		"ten million":        "NUMBER",
		"Vision Pro":         "PRODUCT",
	}
	for entity, tmpl := range want {
		require.Equal(t, tmpl, templateByEntity[entity], "entity %q must bind to the canonical template", entity)
	}

	// The timed OverlayPlan (the render job input) still projects from the
	// same certified surfaces; the persisted template choice is the one the
	// plan resolves through the registry.
	require.NotNil(t, res.OverlayPlan, "the timed overlay plan must also project")
	require.NoError(t, res.OverlayPlan.Validate())
}

// fakeOverlayPrepareEnqueuer records every PrepareRequest the runner submits.
type fakeOverlayPrepareEnqueuer struct {
	reqs    []capabilityoverlay.PrepareRequest
	failErr error
}

func (f *fakeOverlayPrepareEnqueuer) EnqueuePrepare(_ context.Context, req capabilityoverlay.PrepareRequest) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.reqs = append(f.reqs, req)
	return nil
}

// TestRunner_OverlayPrepare_EnqueuedBeforeTTS certifies the overlay.prepare
// parallel-start contract: with the prepare enqueuer wired, the runner
// persists the pre-timing OverlayIntents and submits overlay.prepare
// immediately after entity extraction — before the voiceover/TTS phase — so
// template resolution and asset prefetch run in parallel with audio
// synthesis. The submitted request carries the PENDING intents with their
// resolved template_id and the canonical canvas.
func TestRunner_OverlayPrepare_EnqueuedBeforeTTS(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{
		{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
			Annotations: overlayScene0Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
	})
	docPub := newStubDocumentPublisher()
	prepEnq := &fakeOverlayPrepareEnqueuer{}
	runner := NewRunner(repo, textGen, newStubTranslator(), &entityTimelineVoiceoverGenerator{}, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetOverlayCanvas(GoldenOverlayCanvas)
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	runner.SetOverlayPrepareEnqueuer(prepEnq)

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-prepare-cert"

	runID := "run-overlay-prepare-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	// The prepare job was submitted exactly once with the pre-timing intents.
	require.Len(t, prepEnq.reqs, 1, "overlay.prepare must be enqueued once")
	prep := prepEnq.reqs[0]
	require.Equal(t, runID, prep.PlanID)
	require.Equal(t, runID, prep.VideoID)
	require.Equal(t, GoldenOverlayCanvas.Width, prep.Width)
	require.Equal(t, GoldenOverlayCanvas.Height, prep.Height)
	require.Equal(t, GoldenOverlayCanvas.FPSNum, prep.FPSNum)
	require.Equal(t, GoldenOverlayCanvas.FPSDen, prep.FPSDen)
	require.NoError(t, prep.Validate())

	want := map[string]string{
		"Tim Cook":           "person_default",
		"Apple":              "LOGO",
		"Cupertino":          "gpe_default",
		"changed everything": "quote",
		"ten million":        "NUMBER",
		"Vision Pro":         "PRODUCT",
	}
	seen := 0
	for _, intent := range prep.Intents {
		require.Equal(t, capabilityoverlay.TimingStatePending, intent.TimingState, "prepare must carry PENDING intents")
		if tmpl, ok := want[intent.Entity.CanonicalName]; ok {
			require.Equal(t, tmpl, intent.TemplateID, "entity %q must bind to the canonical template", intent.Entity.CanonicalName)
			seen++
		}
	}
	require.Equal(t, len(want), seen, "all entity intents must be present in the prepare job")

	// The same intents are persisted on the durable result (persisted before
	// the enqueue): the prepare request and the run payload agree.
	require.NotEmpty(t, final.Result.OverlayIntents, "intents must be persisted on the durable result")
	require.Len(t, final.Result.OverlayIntents, len(prep.Intents))
}

// TestRunner_OverlayPrepare_EnqueueErrorFailsClosed pins the fail-closed
// contract: a non-nil prepare enqueuer that errors fails the run — an
// unavailable prepare backend is never a silent no-op.
func TestRunner_OverlayPrepare_EnqueueErrorFailsClosed(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{
		{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
			Annotations: overlayScene0Annotations(),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
	})
	docPub := newStubDocumentPublisher()
	runner := NewRunner(repo, textGen, newStubTranslator(), &entityTimelineVoiceoverGenerator{}, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})
	runner.SetOverlayCanvas(GoldenOverlayCanvas)
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	runner.SetOverlayPrepareEnqueuer(&fakeOverlayPrepareEnqueuer{failErr: errors.New("renderinggen queue down")})

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "overlay-prepare-fail"

	runID := "run-overlay-prepare-fail"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusFailed, final.Status, "prepare enqueue failure must fail the run")
}

// TestCompileOverlayPlan_NilOrSurfacelessIsNoOp certifies the no-op contract:
// a nil result, or a result without any certified timing surface, derives no
// plan (nil, not an error) — the same legitimate no-op the phrase and entity
// projections implement.
func TestCompileOverlayPlan_NilOrSurfacelessIsNoOp(t *testing.T) {
	plan, err := CompileOverlayPlan(nil, "en", GoldenOverlayCanvas, "plan-1", "video-1", "")
	require.NoError(t, err)
	require.Nil(t, plan)

	// A result whose scenes carry NO voiceover timing contributes nothing.
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Tim Cook speaks."}, Annotations: overlayScene0Annotations()},
	}}
	plan, err = CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-1", "video-1", "")
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
	plan, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-1", "video-1", "")
	require.NoError(t, err)
	require.NotNil(t, plan, "the spoken keyword must still produce a plan")
	require.Len(t, plan.Items, 1, "unspoken phrase skipped, spoken keyword kept")
	require.Equal(t, "IMPORTANT_WORD", plan.Items[0].TemplateID)
	require.Equal(t, "Cook", plan.Items[0].Text)
	require.Equal(t, int64(100), plan.Items[0].StartMs)
	require.Equal(t, int64(200), plan.Items[0].EndMs)
}

// TestCompileOverlayPlan_ChosenEntityCardCarriesResolvedAsset certifies the
// canonical-id connection end-to-end: the chosen entity (the scene-relevant
// one with a certified occurrence) BECOMES the entity card that carries its
// image asset — resolved through the canonical_entity_id → EntityMediaResolver
// path and attached as AssetRefs + EntityRef.CanonicalEntityID. An off-scene
// entity with a bound image ("Tesla" when the scene is about Tim Cook) is
// skipped — no card, no image — and no generic IMAGE_OVERLAY is ever emitted
// for an entity-card kind (the card replaces it).
func TestCompileOverlayPlan_ChosenEntityCardCarriesResolvedAsset(t *testing.T) {
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
				PrimaryEntities: []scriptpkg.AnnotatedEntity{
					{
						ID: "e-tim", CanonicalName: "Tim Cook", Type: "PERSON", Confidence: 0.98,
						CanonicalEntityID: "person:tim-cook",
						Image: &scriptpkg.EntityImageBinding{
							Status: "resolved", AssetID: "tim-cook-photo",
							PreviewURL: "https://cdn.example.com/tim-cook.jpg",
							SHA256:     "aa11bb22cc33dd44ee55ff66778899aabbccddeeff00112233445566778899aabb",
						},
					},
					{
						ID: "e-tesla", CanonicalName: "Tesla", Type: "ORG", Confidence: 0.9,
						CanonicalEntityID: "org:tesla",
						Image: &scriptpkg.EntityImageBinding{
							Status: "resolved", AssetID: "tesla-logo",
							PreviewURL: "https://cdn.example.com/tesla.png",
							SHA256:     "bb22cc33dd44ee55ff66778899aabbccddeeff00112233445566778899aabbcc",
						},
					},
				},
			},
		}},
		ResolvedScenes: []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 300_000}},
		EntityTimeline: &capabilityentities.EntityTimeline{
			Version: capabilityentities.EntityTimelineVersion, Language: "en", DurationUS: 300_000,
			Scenes: []capabilityentities.SceneEntityTimeline{{
				SceneID: "scene-0", SceneIndex: 0, TimelineStartUS: 0,
				Entities: []capabilityentities.EntityOccurrence{{
					EntityID: capabilityentities.StableEntityID("PERSON", "Tim Cook"),
					Name:     "Tim Cook", Type: "PERSON", SceneID: "scene-0", SceneIndex: 0,
					TextStart: 0, TextEnd: 8, WordStart: 0, WordEnd: 1,
					LocalStartUS: 0, LocalEndUS: 200_000,
					TimelineStartUS: 0, AudioStartUS: 0, AudioEndUS: 200_000,
					Confidence: 0.98,
				}},
			}},
		},
	}

	plan, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-1", "video-1", "")
	require.NoError(t, err)
	require.NotNil(t, plan, "the spoken entity must still produce a plan")
	byID := map[string]capabilityoverlay.OverlayItem{}
	for _, item := range plan.Items {
		byID[item.ID] = item
		require.NotEqual(t, "IMAGE_OVERLAY", item.TemplateID, "entity-card kinds must never emit a generic IMAGE_OVERLAY")
	}
	card, ok := byID["overlay-scene-0-tim-cook"]
	require.True(t, ok, "the chosen entity (Tim Cook) must become the entity card")
	require.Equal(t, "person_default", card.TemplateID)
	require.Len(t, card.AssetRefs, 1, "the chosen entity card must carry its resolved image asset")
	require.Equal(t, "tim-cook-photo", card.AssetRefs[0].AssetID)
	require.Equal(t, "https://cdn.example.com/tim-cook.jpg", card.AssetRefs[0].URL)
	require.Equal(t, "person:tim-cook", card.EntityRef.CanonicalEntityID, "the card must join on the resolver's canonical id")
	for id := range byID {
		require.NotContains(t, id, "tesla", "off-scene entity (Tesla) must never be selected, never rendered")
	}
}

// TestCompileOverlayPlan_RequiresPlanAndVideoID certifies the identity
// contract: a plan without plan_id/video_id fails closed — the queue job id
// IS the plan id, so it can never be empty.
func TestCompileOverlayPlan_RequiresPlanAndVideoID(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Tim Cook speaks."}, Annotations: overlayScene0Annotations()},
	}}
	_, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "", "video-1", "")
	require.Error(t, err)
	_, err = CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-1", "", "")
	require.Error(t, err)
}
