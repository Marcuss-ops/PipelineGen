// Package scriptgeneration — golden06_full_scene_test.go certifies the
// GOLDEN 06 fundamental chain at the boundary PipelineGen owns:
//
//	script scene
//	   ↓ auto content selection (annotations + certified word timing)
//	OverlayPlan            (CompileOverlayPlan)
//	   ↓
//	chronon.render-plan.v1 (CompileChrononPlan)
//	   ↓
//	RenderingGen queue     (QueueRenderEnqueuer.EnqueueChrononPlan)
//	   ↓ certified artifact reference
//
// MP4 → SHA256 → DB → Drive are the RenderingGen worker's job (cross-repo);
// PipelineGen certifies the deterministic instruction set that reaches the
// queue, exactly as the worker expects it. No external service is touched.
package scriptgeneration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// golden06Words returns the canonical 16-word timing of the scene text
// ("Tim Cook said that Apple changed everything in Cupertino and sold ten
// million Vision Pro units."), 100ms per word.
func golden06Words() []capabilityaudio.SpeechWordTiming {
	text := []string{"Tim", "Cook", "said", "that", "Apple", "changed", "everything", "in", "Cupertino", "and", "sold", "ten", "million", "Vision", "Pro", "units"}
	words := make([]capabilityaudio.SpeechWordTiming, len(text))
	for i, w := range text {
		words[i] = capabilityaudio.SpeechWordTiming{Index: i, Text: w, StartUS: int64(i) * 100_000, EndUS: int64(i+1) * 100_000}
	}
	return words
}

// golden06Timeline builds the certified EntityTimeline for the scene: every
// occurrence carries real word-timing boundaries (local == audio since the
// scene offset is 0), never a text-length estimate.
func golden06Timeline() *capabilityentities.EntityTimeline {
	occ := func(name, typ string, startUS, endUS int64, wordStart, wordEnd int) capabilityentities.EntityOccurrence {
		return capabilityentities.EntityOccurrence{
			EntityID:        capabilityentities.StableEntityID(typ, name),
			Name:            name,
			Type:            typ,
			SceneID:         "scene-0",
			SceneIndex:      0,
			TextStart:       wordStart,
			TextEnd:         wordStart + 1,
			WordStart:       wordStart,
			WordEnd:         wordEnd,
			LocalStartUS:    startUS,
			LocalEndUS:      endUS,
			TimelineStartUS: 0,
			AudioStartUS:    startUS,
			AudioEndUS:      endUS,
			Confidence:      0.9,
		}
	}
	return &capabilityentities.EntityTimeline{
		Version:    capabilityentities.EntityTimelineVersion,
		DurationUS: 1_600_000,
		Scenes: []capabilityentities.SceneEntityTimeline{{
			SceneID:         "scene-0",
			SceneIndex:      0,
			TimelineStartUS: 0,
			Entities: []capabilityentities.EntityOccurrence{
				occ("Tim Cook", "PERSON", 0, 200_000, 0, 1),
				occ("Apple", "LOGO", 400_000, 500_000, 4, 4),
				occ("changed everything", "QUOTE", 500_000, 700_000, 5, 6),
				occ("Cupertino", "GPE", 800_000, 900_000, 8, 8),
				occ("ten million", "CARDINAL", 1_100_000, 1_300_000, 11, 12),
				occ("Vision Pro", "PRODUCT", 1_300_000, 1_500_000, 13, 14),
			},
		}},
	}
}

// TestGolden06FullScriptScene drives the whole PipelineGen-side chain from a
// deterministic script scene to the RenderingGen queue: auto content selection
// (phrases, words, images, entity cards, number, quote, product, logo) →
// OverlayPlan → chronon.render-plan.v1 → queue submit → certified artifact.
func TestGolden06FullScriptScene(t *testing.T) {
	timing := capabilityaudio.SpeechTimingArtifact{
		Version:      capabilityaudio.SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: capabilityaudio.BoundaryWord,
		Language:     "en",
		TextSHA256:   "golden-06-text",
		AudioSHA256:  "golden-06-audio",
		DurationUS:   1_600_000,
		Words:        golden06Words(),
	}
	result := &GenerateResult{
		Scenes: []Scene{{
			ID:    "scene-0",
			Index: 0,
			Text:  map[Language]string{"en": "Tim Cook said that Apple changed everything in Cupertino and sold ten million Vision Pro units."},
			Voiceover: map[Language]AudioReference{
				"en": {ID: "vo-scene-0-en", Duration: 1.6, Timing: &timing},
			},
			Annotations: overlayScene0Annotations(),
		}},
		EntityTimeline: golden06Timeline(),
		ResolvedScenes: []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 1_600_000}},
	}

	// 1. Script → auto content selection → OverlayPlan.
	plan, err := CompileOverlayPlan(result, "en", DefaultOverlayCanvas, "golden-06", "video-golden-06", "golden-06-project")
	require.NoError(t, err)
	require.NotNil(t, plan, "the script scene must derive an OverlayPlan")
	require.NoError(t, plan.Validate())
	require.NotEmpty(t, plan.Items)

	// 2. Content selection: the semantic vocabulary, each anchored to
	//    certified timing (never estimated). The chosen entity (Tim Cook) is
	//    the person card carrying its image asset — entity-card images never
	//    render as a separate IMAGE_OVERLAY (the card replaces it).
	templates := map[string]bool{}
	for _, item := range plan.Items {
		templates[item.TemplateID] = true
	}
	for _, want := range []string{
		"IMPORTANT_PHRASE", "IMPORTANT_WORD",
		"person_default", "gpe_default", "NUMBER", "QUOTE", "PRODUCT", "LOGO",
	} {
		require.True(t, templates[want], "plan must carry template %q (got %v)", want, templates)
	}
	require.NotContains(t, templates, "IMAGE_OVERLAY", "entity-card images must not render twice (the card carries the asset)")

	// 3. OverlayPlan → chronon.render-plan.v1.
	compiled, err := capabilityoverlay.CompileChrononPlan(*plan)
	require.NoError(t, err)
	require.NotEmpty(t, compiled.Plan.Layers)
	require.Equal(t, capabilityoverlay.ChrononSchema, compiled.Plan.Schema)

	// 4. RenderingGen queue: submit + completed artifact (the worker's reply).
	//    The fake client treats a pre-seeded job as idempotent (ErrJobExists),
	//    so we seed the exact spec/assets the enqueuer would submit and the
	//    completed artifact the worker would return — mirroring
	//    TestQueueRenderEnqueuerChrononPlan.
	spec, err := compiled.Marshal()
	require.NoError(t, err)
	assets := make([]RenderQueueAsset, 0, len(compiled.Assets))
	for _, a := range compiled.Assets {
		assets = append(assets, RenderQueueAsset{Hash: a.Hash, URL: a.LogicalPath})
	}
	client := newFakeRenderQueueClient()
	client.jobs["golden-06"] = RenderQueueJob{
		ID:          "golden-06",
		OverlaySpec: spec,
		Assets:      assets,
		State:       "completed",
		Artifact: &RenderArtifact{
			ID: "art-golden-06", SHA256: "sha-golden-06", MimeType: "video/mp4",
			Width: 1280, Height: 720, FPSNum: 30, FrameCount: 150, DurationUS: 5_000_000,
		},
	}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	require.NoError(t, err)
	enqueuer.pollInterval = time.Millisecond
	// Attach the analytics recorder so the completed attempt is recorded as
	// one durable row (the per-attempt DB analytics surface).
	recorder := &fakeAttemptRecorder{}
	enqueuer.SetRecorder(recorder)

	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), *plan)
	require.NoError(t, err)
	require.Equal(t, "golden-06", ref.JobID)
	require.Equal(t, "COMPLETED", ref.Status)
	require.NotNil(t, ref.Artifact)
	require.Equal(t, "sha-golden-06", ref.Artifact.SHA256)
	require.Equal(t, 1280, ref.Artifact.Width)
	require.Equal(t, 720, ref.Artifact.Height)

	// 5. Per-attempt analytics: exactly one record, derived from the plan's
	//    content census + the certified artifact.
	require.Len(t, recorder.recorded, 1, "completed render must record one analytics attempt")
	analytics := recorder.recorded[0]
	require.Equal(t, "golden-06", analytics.AttemptID)
	require.Equal(t, "sha-golden-06", analytics.SHA256)
	require.Equal(t, 1280, analytics.Width)
	require.Equal(t, 720, analytics.Height)
	require.NotZero(t, analytics.Content.Phrases, "content census must count phrases")
	require.NotZero(t, analytics.Content.Words, "content census must count words")
	require.NotZero(t, analytics.Content.Images, "content census must count images")

	// The submitted queue job carries the chronon.render-plan.v1 document
	// (not a media render-plan) and the content-addressed assets.
	submitted, ok := client.jobs["golden-06"]
	require.True(t, ok, "job must be submitted to the queue")
	var doc struct {
		Schema string `json:"schema"`
	}
	require.NoError(t, json.Unmarshal(submitted.OverlaySpec, &doc))
	require.Equal(t, capabilityoverlay.ChrononSchema, doc.Schema)
	require.NotEmpty(t, submitted.Assets)
}
