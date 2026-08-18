package scriptgeneration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// entityTimelineVoiceoverGenerator returns a voiceover whose AudioReference
// carries the canonical word-level timing artifact (100ms per
// whitespace-delimited word) so the runner can project the entity timeline
// from REAL word boundaries.
type entityTimelineVoiceoverGenerator struct {
	mu sync.Mutex
}

func (g *entityTimelineVoiceoverGenerator) Generate(_ context.Context, input VoiceoverInput) (AudioReference, error) {
	words := strings.Fields(input.Text)
	boundaries := make([]capabilityaudio.SpeechWordTiming, len(words))
	for i, w := range words {
		boundaries[i] = capabilityaudio.SpeechWordTiming{
			Index:   i,
			Text:    w,
			StartUS: int64(i) * 100_000,
			EndUS:   int64(i+1) * 100_000,
		}
	}
	return AudioReference{
		ID:       "vo-" + input.SceneID + "-en",
		URL:      "https://drive.google.com/file/d/vo-" + input.SceneID + "-en",
		FilePath: "/tmp/vo-" + input.SceneID + "-en.mp3",
		Duration: float64(len(words)) * 0.1,
		Timing: &capabilityaudio.SpeechTimingArtifact{
			Version:      capabilityaudio.SpeechTimingVersion,
			Provider:     "edge_tts",
			BoundaryMode: capabilityaudio.BoundaryWord,
			Language:     string(input.Language),
			TextSHA256:   "text-hash-" + input.SceneID,
			AudioSHA256:  "audio-hash-" + input.SceneID,
			DurationUS:   int64(len(words)) * 100_000,
			Words:        boundaries,
		},
	}, nil
}

func entityAnnotationsForScene(text string) *scriptpkg.SceneAnnotations {
	return &scriptpkg.SceneAnnotations{
		Version:  1,
		Language: "en",
		Status:   "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "entity-tom-hanks", CanonicalName: "Tom Hanks", Type: "PERSON", Confidence: 0.98},
			{ID: "entity-los-angeles", CanonicalName: "Los Angeles", Type: "GPE", Confidence: 0.9},
		},
	}
}

// TestRunner_EntityTimelineDerivedFromRealWordTiming certifies the durable
// runner wiring: after a complete COMBINED_TIMELINE run, GenerateResult
// carries the EntityTimeline SSOT — every occurrence anchored to the real
// voiceover word timing (never a text-length estimate) and mapped onto the
// final combined timeline via the scene's canonical offset.
//
// Two narration scenes:
//
//	scene-0 (offset 0s)      "Tom Hanks visited Los Angeles last summer."
//	scene-1 (offset 0.700s)  "Tom Hanks loves Los Angeles and its beaches."
//
// word boundaries are 100ms per word, so:
//
//	scene-0 "Tom Hanks"    local 0.000–0.200 → global 0.000–0.200s
//	scene-0 "Los Angeles"  local 0.300–0.500 → global 0.300–0.500s
//	scene-1 "Tom Hanks"    local 0.000–0.200 → global 0.700–0.900s
//	scene-1 "Los Angeles"  local 0.300–0.500 → global 1.000–1.200s
func TestRunner_EntityTimelineDerivedFromRealWordTiming(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator([]Scene{
		{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Tom Hanks visited Los Angeles last summer."},
			Annotations: entityAnnotationsForScene("Tom Hanks visited Los Angeles last summer."),
			Audio:       capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
		},
		{
			ID: "scene-1", Index: 1,
			Text:        map[Language]string{"en": "Tom Hanks loves Los Angeles and its beaches."},
			Annotations: entityAnnotationsForScene("Tom Hanks loves Los Angeles and its beaches."),
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
	// Google Docs is ALWAYS enabled on the certified entity-timing path: the
	// published document is the durable surface that carries the per-scene
	// entity annotations (SpecScene JSON), so the doc and the EntityTimeline
	// SSOT describe the same entities.
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "entity-timeline-cert"

	runID := "run-entity-timeline-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "run must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res)
	require.NotNil(t, res.CanonicalTimeline, "canonical timeline must exist")
	require.NotNil(t, res.EntityTimeline, "entity timeline must be projected")
	require.NoError(t, res.EntityTimeline.Validate())

	require.Len(t, res.EntityTimeline.Scenes, 2, "one scene timeline per annotated scene")
	require.Equal(t, "en", res.EntityTimeline.Language)
	require.Equal(t, res.CanonicalTimeline.DurationUS, res.EntityTimeline.DurationUS)

	// scene-0: offset 0s, voiceover "vo-scene-0-en".
	scene0 := res.EntityTimeline.Scenes[0]
	require.Equal(t, "scene-0", scene0.SceneID)
	require.Equal(t, "vo-scene-0-en", scene0.VoiceoverAssetID)
	require.Equal(t, int64(0), scene0.TimelineStartUS)
	require.Equal(t, int64(0), scene0.TimelineStartUS, "scene-0 must start the master")

	tom0 := occurrenceByID(t, scene0, capabilityentities.StableEntityID("PERSON", "Tom Hanks"))
	require.Equal(t, int64(0), tom0.LocalStartUS)
	require.Equal(t, int64(200_000), tom0.LocalEndUS)
	require.Equal(t, int64(0), tom0.AudioStartUS)
	require.Equal(t, int64(200_000), tom0.AudioEndUS)

	la0 := occurrenceByID(t, scene0, capabilityentities.StableEntityID("GPE", "Los Angeles"))
	require.Equal(t, int64(300_000), la0.LocalStartUS, "Los Angeles starts at word 3")
	require.Equal(t, int64(500_000), la0.LocalEndUS)
	require.Equal(t, int64(300_000), la0.AudioStartUS)
	require.Equal(t, int64(500_000), la0.AudioEndUS)

	// scene-1: offset = scene-0 voiceover duration (7 words × 100ms).
	scene1 := res.EntityTimeline.Scenes[1]
	require.Equal(t, int64(700_000), scene1.TimelineStartUS, "scene-1 offset must equal the scene-0 voiceover duration")
	tom1 := occurrenceByID(t, scene1, capabilityentities.StableEntityID("PERSON", "Tom Hanks"))
	require.Equal(t, int64(700_000), tom1.AudioStartUS)
	require.Equal(t, int64(900_000), tom1.AudioEndUS)
	la1 := occurrenceByID(t, scene1, capabilityentities.StableEntityID("GPE", "Los Angeles"))
	require.Equal(t, int64(1_000_000), la1.AudioStartUS)
	require.Equal(t, int64(1_200_000), la1.AudioEndUS)

	// Every occurrence is grounded in the scene text (rune span) and stays
	// inside the certified final audio: the canonical timeline IS the
	// certified master duration (final_audio within the encoder tolerance).
	for i, scene := range res.EntityTimeline.Scenes {
		text := res.Scenes[i].Text["en"]
		runes := []rune(text)
		for _, o := range scene.Entities {
			require.Equal(t, o.Name, string(runes[o.TextStart:o.TextEnd]), "scene %s occurrence must be grounded in the text", scene.SceneID)
			require.LessOrEqual(t, o.AudioEndUS, res.EntityTimeline.DurationUS)
			require.LessOrEqual(t, o.AudioEndUS, res.CanonicalTimeline.DurationUS, "occurrence must end inside the certified final audio")
		}
	}

	// GOOGLE DOCS TRUE — the certified entity-timing run publishes the Google
	// Doc (docs.enabled=true) and the document's SpecScene JSON carries the
	// same per-scene entity annotations the EntityTimeline was projected
	// from, so the doc surface and the timing SSOT never diverge.
	doc, ok := res.Documents["en"]
	require.True(t, ok, "a Google Doc must be published when docs.enabled=true")
	require.NotEmpty(t, doc.ID, "document id must be present")
	require.NotEmpty(t, doc.Link, "document link must be present")
	require.Len(t, docPub.records, 1, "exactly one document must be upserted (en)")
	require.Equal(t, 2, res.DocumentSceneCounts["en"], "document must project both scenes")
	content := docPub.records[0].Content
	require.Contains(t, content, "<h2>SpecScene JSON</h2>", "document must embed the machine SpecScene surface")
	require.Contains(t, content, "entity-tom-hanks", "document SpecScene must carry the PERSON annotation")
	require.Contains(t, content, "entity-los-angeles", "document SpecScene must carry the GPE annotation")
	require.Contains(t, content, "Tom Hanks", "document must carry the entity names")
	require.Contains(t, content, "Los Angeles", "document must carry the entity names")

	// The persisted SSOT feeds the overlay resolver: every occurrence gets
	// an entity_card starting exactly when the entity is spoken.
	plan, err := capabilityentities.ResolveEntityOverlayPlan(*res.EntityTimeline, "plan-run-001", "video-run-001", "", 1280, 720, 30)
	require.NoError(t, err)
	require.Len(t, plan.Items, 4, "four entity occurrences → four entity cards")
	item := overlayItemByID(t, plan, "overlay-scene-1-tom-hanks")
	require.Equal(t, string(capabilityoverlay.KindEntityCard), item.Kind)
	require.Equal(t, "person_default", item.TemplateID)
	require.Equal(t, int64(700), item.StartMs, "scene-1 Tom Hanks card starts at 0.700s")

	compiled, err := capabilityoverlay.CompileChrononPlan(plan)
	require.NoError(t, err)
	require.Len(t, compiled.Plan.Layers, 4)
}

func occurrenceByID(t *testing.T, scene capabilityentities.SceneEntityTimeline, entityID string) capabilityentities.EntityOccurrence {
	t.Helper()
	for _, o := range scene.Entities {
		if o.EntityID == entityID {
			return o
		}
	}
	t.Fatalf("occurrence %q not found in scene %s", entityID, scene.SceneID)
	return capabilityentities.EntityOccurrence{}
}

func overlayItemByID(t *testing.T, plan capabilityoverlay.OverlayPlan, id string) capabilityoverlay.OverlayItem {
	t.Helper()
	for _, item := range plan.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("overlay item %q not found", id)
	return capabilityoverlay.OverlayItem{}
}
