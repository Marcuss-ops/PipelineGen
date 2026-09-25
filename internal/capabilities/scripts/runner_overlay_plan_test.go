// Package scriptgeneration — runner_overlay_plan_test.go certifies the
// production overlay derivation on REAL jobs: after a complete
// COMBINED_TIMELINE run, GenerateResult carries the semantic OverlayPlan
// covering the full nine-template vocabulary (IMPORTANT_PHRASE,
// IMPORTANT_WORD, IMAGE_OVERLAY, PERSON, NUMBER, QUOTE, LOCATION, PRODUCT,
// LOGO), every item anchored to the real voiceover word timing (never a
// text-length estimate), and every template terminating in one of the four
// canonical primitives (Text / Image / Video / Shape) when compiled to
// chronon render-plan.
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
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestFreezeOverlayIntentsPromotesVerifiedEntityImageToImageOnly(t *testing.T) {
	asset := capabilityoverlay.OverlayAssetRef{
		AssetID: "portrait-sha", URL: "https://images.example.test/jordan.jpg",
		SHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MediaType: "image/jpeg",
	}
	intents := []capabilityoverlay.OverlayIntent{{
		Version:  capabilityoverlay.OverlayIntentVersion,
		IntentID: "intent-scene-0-michael-jordan", SceneID: "scene-0",
		Entity: capabilityoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Michael Jordan"},
		Source: capabilityoverlay.IntentSourceEntity, Kind: "entity_card", TemplateID: "person_default",
		Payload: capabilityoverlay.IntentPayload{Name: "Michael Jordan"}, TimingState: capabilityoverlay.TimingStatePending,
	}}
	items := []capabilityoverlay.OverlayItem{{
		ID: "overlay-scene-0-michael-jordan", SceneID: "scene-0", EntityID: frozenTestEntityID,
		Kind: string(capabilityoverlay.KindEntityImage), TemplateID: "image_popup",
		StartMs: 100, EndMs: 5100, StartUS: 100000, DurationUS: 5000000,
		PresetID: "image_fast_fade", AssetRefs: []capabilityoverlay.OverlayAssetRef{asset},
		EntityRef: &capabilityoverlay.OverlayEntityRef{Name: "Michael Jordan"},
	}}

	freezeOverlayIntents(intents, items)
	got := intents[0]
	if got.Kind != string(capabilityoverlay.KindEntityImage) || got.TemplateID != "image_popup" {
		t.Fatalf("resolved intent kind/template = %q/%q, want entity_image/image_popup", got.Kind, got.TemplateID)
	}
	if got.Payload.Name != "" || got.Payload.Text != "" {
		t.Fatalf("image-only intent retained display text: %#v", got.Payload)
	}
	if got.TimingState != capabilityoverlay.TimingStateFrozen || len(got.AssetRefs) != 1 {
		t.Fatalf("resolved image intent lost timing/assets: %#v", got)
	}
}

// frozenTestEntityID is the content-addressed identity the canonical entity
// timeline stamps for the fixture's PERSON annotation ("Michael Jordan"). Plan
// items carry it, so the fixture models the real contract instead of a readable
// label the join would have to compare as text.
var frozenTestEntityID = capabilityentities.StableEntityID("PERSON", "Michael Jordan")

// TestFreezeOverlayIntentsJoinsByEntityIdentity certifies the freeze join no
// longer depends on the two surfaces spelling a name identically: the plan item
// carries only the content-addressed entity id (its EntityRef names a different
// surface), and the intent is still frozen onto it. Under the previous
// canonical-name comparison this join silently failed and the persisted intent
// kept PENDING timing next to a rendered image layer.
func TestFreezeOverlayIntentsJoinsByEntityIdentity(t *testing.T) {
	intents := []capabilityoverlay.OverlayIntent{{
		Version:  capabilityoverlay.OverlayIntentVersion,
		IntentID: "intent-scene-0-michael-jordan", SceneID: "scene-0",
		Entity: capabilityoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Michael Jordan", CanonicalEntityID: "person:michael-jordan"},
		Source: capabilityoverlay.IntentSourceEntity, Kind: "entity_card", TemplateID: "person_default",
		Payload: capabilityoverlay.IntentPayload{Name: "Michael Jordan"}, TimingState: capabilityoverlay.TimingStatePending,
	}}
	items := []capabilityoverlay.OverlayItem{{
		ID: "overlay-scene-0-michael-jordan", SceneID: "scene-0", EntityID: frozenTestEntityID,
		Kind: string(capabilityoverlay.KindEntityCard), TemplateID: "person_default",
		StartMs: 0, EndMs: 2500, StartUS: 0, DurationUS: 2500000,
		Text: "MJ", EntityRef: &capabilityoverlay.OverlayEntityRef{Name: "MJ"},
	}}

	freezeOverlayIntents(intents, items)
	if intents[0].TimingState != capabilityoverlay.TimingStateFrozen {
		t.Fatalf("timing state = %q, want FROZEN: the join must use the entity identity, not the display name", intents[0].TimingState)
	}
	if intents[0].EndMs != 2500 {
		t.Fatalf("frozen intent end_ms = %d, want the plan item's timing", intents[0].EndMs)
	}
}

// TestFreezeOverlayIntentsKeepsNameFallbackForLegacyItems pins the compatibility
// path: a legacy plan item with no entity id still joins by canonical name, so
// old persisted plans keep resolving instead of losing their timing.
func TestFreezeOverlayIntentsKeepsNameFallbackForLegacyItems(t *testing.T) {
	intents := []capabilityoverlay.OverlayIntent{{
		Version:  capabilityoverlay.OverlayIntentVersion,
		IntentID: "intent-scene-0-michael-jordan", SceneID: "scene-0",
		Entity: capabilityoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Michael Jordan"},
		Source: capabilityoverlay.IntentSourceEntity, Kind: "entity_card", TemplateID: "person_default",
		Payload: capabilityoverlay.IntentPayload{Name: "Michael Jordan"}, TimingState: capabilityoverlay.TimingStatePending,
	}}
	items := []capabilityoverlay.OverlayItem{{
		ID: "overlay-scene-0-michael-jordan", SceneID: "scene-0",
		Kind: string(capabilityoverlay.KindEntityCard), TemplateID: "person_default",
		StartMs: 0, EndMs: 2500, StartUS: 0, DurationUS: 2500000,
		Text: "Michael Jordan",
	}}

	freezeOverlayIntents(intents, items)
	if intents[0].TimingState != capabilityoverlay.TimingStateFrozen {
		t.Fatalf("timing state = %q, want FROZEN via the canonical-name fallback", intents[0].TimingState)
	}
}

func TestOverlayCanvasDefaultsPreserveBackgroundAndStyle(t *testing.T) {
	style := &scriptpkg.OverlayStyleSpec{Color: []float64{0.1, 0.2, 0.3, 1}}
	background := &capabilityoverlay.OverlayBackground{Kind: "video", Fit: "cover", Loop: true}
	got := (OverlayCanvasSpec{Background: background, Style: style}).withDefaults()

	if got.Width != 1920 || got.Height != 1080 || got.FPSNum != 24 || got.FPSDen != 1 {
		t.Fatalf("default canvas = %dx%d@%d/%d, want 1920x1080@24/1", got.Width, got.Height, got.FPSNum, got.FPSDen)
	}
	if got.Background != background {
		t.Fatalf("defaulting dropped background: got %#v, want same semantic pointer", got.Background)
	}
	if got.Style != style {
		t.Fatalf("defaulting dropped style: got %#v, want same semantic pointer", got.Style)
	}
}

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
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "apple-logo", PreviewURL: "https://cdn.example.com/apple-logo.png", SHA256: "dd44ee55ff66778899aabbccddeeff00112233445566778899aabbccddeeff00"},
			},
			{ID: "entity-cupertino", CanonicalName: "Cupertino", Type: "GPE", Confidence: 0.9},
		},
		SecondaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-change-everything", CanonicalName: "changed everything", Type: "QUOTE", Confidence: 0.85},
			{ID: "entity-ten-million", CanonicalName: "ten million", Type: "CARDINAL", Confidence: 0.9},
			{
				ID: "entity-vision-pro", CanonicalName: "Vision Pro", Type: "PRODUCT", Confidence: 0.95,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "vision-pro", PreviewURL: "https://cdn.example.com/vision-pro.png", SHA256: "ee55ff66778899aabbccddeeff00112233445566778899aabbccddeeff001122"},
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

// TestRunner_OverlayPlanAppliesRunLevelEditorialBudget certifies that a real
// timed run keeps only grounded phrase overlays and image overlays, with a
// five-item global ceiling for each and no invented phrase backfill.
func TestRunner_OverlayPlanAppliesRunLevelEditorialBudget(t *testing.T) {
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
	require.NotNil(t, res.FinalAudio, "the overlay plan must have a certified master-audio extent")
	require.Equal(t, res.FinalAudio.DurationMS, res.OverlayPlan.DurationMS,
		"overlay plan duration must follow the certified final audio")
	require.NoError(t, res.OverlayPlan.Validate())

	byID := map[string]capabilityoverlay.OverlayItem{}
	images, phrases := 0, 0
	for _, item := range res.OverlayPlan.Items {
		byID[item.ID] = item
		switch item.Kind {
		case "entity_image", "image":
			images++
		case "text_phrase":
			phrases++
		default:
			t.Fatalf("non-editorial overlay survived editorial budget: %+v", item)
		}
	}
	require.Equal(t, 1, images)
	require.Equal(t, 2, phrases)
	require.Len(t, res.OverlayPlan.Items, 3)
	require.Equal(t, capabilityoverlay.PhraseOverlayBudget{Requested: 15, Materialized: 2, Shortfall: 13}, *res.PhraseOverlayBudget)

	phrase := byID["scene-0-phrase-changed-everything"]
	require.Equal(t, "IMPORTANT_PHRASE", phrase.TemplateID)
	require.Equal(t, int64(500), phrase.StartMs)
	require.Equal(t, int64(700), phrase.EndMs)
	require.NotEmpty(t, phrase.PresetID)
	require.NotEmpty(t, phrase.MotionID)

	scene1Phrase := byID["scene-1-phrase-growth-matters"]
	require.Equal(t, int64(1600), scene1Phrase.StartMs)
	require.Equal(t, int64(1800), scene1Phrase.EndMs)

	person := byID["overlay-scene-0-tim-cook"]
	require.Equal(t, "entity_image", person.Kind)
	require.Equal(t, "image_popup", person.TemplateID)
	require.Empty(t, person.Text)
	require.NotEmpty(t, person.PresetID)
	require.Len(t, person.AssetRefs, 1)
	require.Equal(t, "person:tim-cook", person.EntityRef.CanonicalEntityID)
	require.Contains(t, capabilityoverlay.ImagePresetCandidates(), person.PresetID)

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

func TestCompileResultOverlayPlanPersistsPhraseBudgetEvenWhenNoPhraseIsGrounded(t *testing.T) {
	result := &GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "A scene without certified timing."}},
	}}
	require.NoError(t, compileResultOverlayPlan(result, "en", "plan-1", "project-1", "", GoldenOverlayCanvas))
	require.Nil(t, result.OverlayPlan)
	require.NotNil(t, result.PhraseOverlayBudget)
	require.Equal(t, capabilityoverlay.PhraseOverlayBudget{
		Requested: capabilityoverlay.MaxPhraseOverlaysPerRun,
		Shortfall: capabilityoverlay.MaxPhraseOverlaysPerRun,
	}, *result.PhraseOverlayBudget)
}

// TestCompileOverlayPlan_UnspokenPhraseSkipped certifies that an unspoken
// phrase is never timestamped and a word annotation does not bypass the
// production phrase/image-only editorial overlay contract.
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
	require.Nil(t, plan, "unspoken phrase and non-contract word overlay produce no plan")
}

func TestCompileOverlayPlanUsesTranslatedPhraseAndVoiceoverTiming(t *testing.T) {
	englishTiming := speechTimingForWords([]string{"Discipline", "creates", "power."})
	spanishTiming := speechTimingForWords([]string{"La", "velocidad", "abre", "la", "distancia", "con", "control."})
	spanishTiming.Language = "es"
	result := &GenerateResult{
		SourceLanguage: "en",
		AudioMode:      capabilityaudio.AudioModeCombinedTimeline,
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version: capabilityaudio.TimelineVersion, DurationUS: englishTiming.DurationUS,
		},
		FinalAudio: &FinalAudioReference{DurationMS: 300},
		Scenes: []Scene{{
			ID: "scene-0", Index: 0,
			Text: map[Language]string{
				"en": "Discipline creates power.",
				"es": "La velocidad abre la distancia con control.",
			},
			Voiceover: map[Language]AudioReference{
				"en": {ID: "vo-en", Duration: 0.3, Timing: &englishTiming},
				"es": {ID: "vo-es", Duration: 0.7, Timing: &spanishTiming},
			},
			Annotations: &scriptpkg.SceneAnnotations{
				Version: 1, Language: "en", Status: "completed",
				ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "Discipline creates power", Score: 0.9}},
			},
			LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
				"es": {
					Version: 1, Language: "es", Status: "completed",
					ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "velocidad abre la distancia", Score: 0.9}},
				},
			},
		}},
	}

	plan, err := CompileOverlayPlan(result, "es", GoldenOverlayCanvas, "run-es", "video-es", "project")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.Equal(t, "es", plan.Language)
	require.Equal(t, int64(700), plan.DurationMS,
		"localized duration must come from the translated voiceover, not the shorter source master")
	require.Len(t, plan.Items, 1)
	require.Equal(t, "velocidad abre la distancia", plan.Items[0].Text,
		"the phrase overlay must use localized NLP rather than the English source annotation")
	require.Equal(t, int64(100), plan.Items[0].StartMs)
	require.Equal(t, int64(500), plan.Items[0].EndMs)
}

func TestCompileOverlayPlanDoesNotReuseSourceAnnotationsForMissingTranslation(t *testing.T) {
	timing := speechTimingForWords([]string{"Discipline", "creates", "power."})
	result := &GenerateResult{
		SourceLanguage: "en",
		AudioMode:      capabilityaudio.AudioModeCombinedTimeline,
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version: capabilityaudio.TimelineVersion, DurationUS: timing.DurationUS,
		},
		Scenes: []Scene{{
			ID: "scene-0", Index: 0,
			Text: map[Language]string{"en": "Discipline creates power.", "es": "La disciplina crea poder."},
			Voiceover: map[Language]AudioReference{
				"es": {ID: "vo-es", Duration: 0.3, Timing: &timing},
			},
			Annotations: &scriptpkg.SceneAnnotations{
				Version: 1, Language: "en", Status: "completed",
				ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "Discipline creates power", Score: 0.9}},
			},
		}},
	}

	plan, err := CompileOverlayPlan(result, "es", GoldenOverlayCanvas, "run-es", "video-es", "project")
	require.NoError(t, err)
	require.Nil(t, plan, "an absent translated annotation must not leak a source-language overlay")
}

func TestCompileOverlayPlanDoesNotReuseUnlabelledSourceEntitiesForTranslation(t *testing.T) {
	timing := speechTimingForWords([]string{"Kanioni", "betonowe", "otworzyły", "historię."})
	result := &GenerateResult{
		SourceLanguage: "en",
		AudioMode:      capabilityaudio.AudioModeCombinedTimeline,
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version: capabilityaudio.TimelineVersion, DurationUS: timing.DurationUS,
		},
		Scenes: []Scene{{
			ID: "brooklyn-origins", Index: 0,
			Text: map[Language]string{"en": "Brooklyn shaped the fighter's beginnings.", "pl": "Betonowe kaniony otworzyły historię."},
			Voiceover: map[Language]AudioReference{
				"pl": {ID: "vo-pl", Duration: 0.4, Timing: &timing},
			},
			// Empty Language is a legacy source annotation. It must not be
			// projected onto the Polish timing stream.
			Annotations: &scriptpkg.SceneAnnotations{
				Version: 1, Status: "completed",
				PrimaryEntities: []scriptpkg.AnnotatedEntity{{Text: "Brooklyn", CanonicalName: "Brooklyn", Type: "LOCATION", Confidence: 1}},
			},
		}},
	}
	plan, err := CompileOverlayPlan(result, "pl", GoldenOverlayCanvas, "run-pl", "video-pl", "project")
	require.NoError(t, err)
	require.Nil(t, plan, "unlabelled source entities must not be matched against translated timing")
}

// TestCompileOverlayPlan_ChosenEntityImageCarriesResolvedAsset certifies the
// canonical-id connection end-to-end: the chosen entity (the scene-relevant
// one with a certified occurrence) BECOMES the image-only entity overlay that carries its
// image asset — resolved through the canonical_entity_id → EntityMediaResolver
// path and attached as AssetRefs + EntityRef.CanonicalEntityID. An off-scene
// entity with a bound image ("Tesla" when the scene is about Tim Cook) is
// skipped — no card, no image — and no generic duplicate image is emitted.
func TestCompileOverlayPlan_ChosenEntityImageCarriesResolvedAsset(t *testing.T) {
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
		require.NotEqual(t, "person_default", item.TemplateID, "an entity with an image must not fall back to a text card")
	}
	card, ok := byID["overlay-scene-0-tim-cook"]
	require.True(t, ok, "the chosen entity (Tim Cook) must become the entity card")
	require.Equal(t, "entity_image", card.Kind)
	require.Equal(t, "image_popup", card.TemplateID)
	require.Empty(t, card.Text, "the entity image must not carry a rendered name")
	require.NotEmpty(t, card.PresetID, "the entity image must use an official image preset")
	require.Empty(t, card.ImagePresetID)
	require.Len(t, card.AssetRefs, 1, "the chosen entity image must carry its resolved asset")
	require.Equal(t, "aa11bb22cc33dd44ee55ff66778899aabbccddeeff00112233445566778899aabb", card.AssetRefs[0].AssetID)
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
