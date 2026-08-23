// Package scriptgeneration — certification_3scene_vertical_slice_test.go is
// the executable 3-scene vertical-slice certification:
//
//	SCRIPT (3 scenes) → automatic entity extraction (VidRush barrier)
//	  → OverlayIntent (PENDING) → overlay.prepare (before TTS)
//	  → Edge TTS + word timing (same synthesis stream)
//	  → CanonicalTimeline frozen → overlay.render (frozen plan)
//	  → Rust final_audio.m4a → EditingTimelineV1
//
// It certifies the full gate matrix in one deterministic run:
//
//	AUTO ENTITIES        3/3   (each scene carries a grounded per-scene EntityResult)
//	OVERLAY INTENTS      3/3   (one PENDING intent per scene, single registry)
//	PREPARE BEFORE TTS   3/3   (prepare enqueued with 3 pre-timing intents)
//	OVERLAY RENDER       3/3   (frozen plan carries 3 microsecond-timed items)
//	FINAL AUDIO          1     (one certified final_audio.m4a)
//	EDITING TIMELINE     1     (one EditingTimelineV1 projection)
//	TIMING MISMATCH      0     (overlay spans == canonical entity timeline)
package scriptgeneration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// cert3SceneAudioRenderer certifies the combined master against the canonical
// plan: DurationUS == plan.DurationUS (perfect deterministic render), so the
// editing timeline can project a non-nil audio reference whose duration
// matches the canonical timeline exactly.
type cert3SceneAudioRenderer struct{ calls int }

func (r *cert3SceneAudioRenderer) Render(_ context.Context, plan capabilityaudio.CompiledAudioPlan, _ capabilityaudio.ResolvedAudioAssets) (FinalAudioReference, AudioPipelineMetrics, error) {
	r.calls++
	return FinalAudioReference{
		AssetID:              "final-audio-3scene",
		Path:                 "/tmp/final_audio.m4a",
		Container:            "m4a",
		AudioContractVersion: capabilityaudio.AudioContractVersion,
		AudioPlanVersion:     plan.Version,
		PlanSHA256:           plan.PlanSHA256,
		FinalAudioSHA256:     strings.Repeat("a", 64),
		Codec:                plan.Output.Codec,
		Profile:              plan.Output.Profile,
		SampleRate:           plan.Output.SampleRate,
		Channels:             plan.Output.Channels,
		ChannelLayout:        plan.Output.ChannelLayout,
		Bitrate:              128000,
		DurationUS:           plan.DurationUS,
		DurationMS:           plan.DurationUS / 1000,
		StartPTS:             0,
		SizeBytes:            1,
		FinalMix:             true,
		CopyEligible:         true,
	}, AudioPipelineMetrics{AudioDurationMS: plan.DurationUS / 1000}, nil
}

// cert3SceneRenderEnqueuer stands in for the RenderingGen queue: it records
// the frozen OverlayPlan the runner submits to overlay.render and returns a
// certified artifact carrying the probed contract facts (prores/mov,
// zero-audio overlay) plus its Drive publication identity. The real ffprobe
// validation and Drive upload live in the render worker
// (TestOverlayEndToEnd_PlanRenderPublishPersist); here the runner consumes the
// immutable certified reference exactly as the queue returns it.
type cert3SceneRenderEnqueuer struct {
	mu    sync.Mutex
	plans []capabilityoverlay.OverlayPlan
}

func (e *cert3SceneRenderEnqueuer) EnqueueChrononPlan(_ context.Context, plan capabilityoverlay.OverlayPlan) (RenderReference, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.plans = append(e.plans, plan)
	return RenderReference{
		JobID:  plan.PlanID,
		Status: "COMPLETED",
		Artifact: &RenderArtifact{
			ID:          "overlay-artifact-001",
			Kind:        "overlay",
			SHA256:      "overlay-sha256-certified",
			MimeType:    "video/quicktime",
			SizeBytes:   1024000,
			Width:       1280,
			Height:      720,
			FPSNum:      30,
			FPSDen:      1,
			Codec:       "prores",
			DriveFileID: "drive-overlay-001",
			DriveLink:   "https://drive.google.com/file/d/drive-overlay-001/view",
		},
	}, nil
}

func (e *cert3SceneRenderEnqueuer) lastPlan() capabilityoverlay.OverlayPlan {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.plans) == 0 {
		return capabilityoverlay.OverlayPlan{}
	}
	return e.plans[len(e.plans)-1]
}

// cert3SceneBarrier returns the fenced per-scene entity extraction result for
// the three narration scenes (one spoken PERSON per scene, each grounded
// verbatim in its scene text).
func cert3SceneBarrier() VidRushBarrier {
	return barrierFunc(func(_ context.Context, _ string) ([]scriptpkg.VidRushSegmentResult, error) {
		return []scriptpkg.VidRushSegmentResult{
			{SceneID: "scene-0", Position: 0, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
				{Value: "Tim Cook", Type: "PERSON", Confidence: 0.98},
			}}},
			{SceneID: "scene-1", Position: 1, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
				{Value: "Margot Robbie", Type: "PERSON", Confidence: 0.97},
			}}},
			{SceneID: "scene-2", Position: 2, Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
				{Value: "Tom Hanks", Type: "PERSON", Confidence: 0.96},
			}}},
		}, nil
	})
}

func cert3SceneTexts() []Scene {
	return []Scene{
		{
			ID: "scene-0", Index: 0,
			Text:         map[Language]string{"en": "Tim Cook leads."},
			Audio:        capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
			AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}},
		},
		{
			ID: "scene-1", Index: 1,
			Text:         map[Language]string{"en": "Margot Robbie acts."},
			Audio:        capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
			AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}},
		},
		{
			ID: "scene-2", Index: 2,
			Text:         map[Language]string{"en": "Tom Hanks narrates."},
			Audio:        capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover},
			AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioVoiceover}},
		},
	}
}

// TestCertification_ThreeSceneVerticalSlice runs the full entity → overlay →
// audio → editing-timeline vertical slice over exactly 3 real scenes and
// certifies every gate. The only stubs are the VidRush barrier (entity
// extraction), the voiceover generator (Edge word timing), the combined audio
// renderer (Rust final mix), the prepare enqueuer, and the render enqueuer —
// every projection, planner, timeline and timeline-validated invariant is
// production code.
func TestCertification_ThreeSceneVerticalSlice(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(cert3SceneTexts())
	docPub := newStubDocumentPublisher()
	prepEnq := &fakeOverlayPrepareEnqueuer{}
	renderEnq := &cert3SceneRenderEnqueuer{}
	audioRenderer := &cert3SceneAudioRenderer{}

	runner := NewRunner(repo, textGen, newStubTranslator(), &entityTimelineVoiceoverGenerator{}, docPub, canonicalTestDocumentRenderer{})
	runner.SetScriptDocsFolderID("test-docs-folder")
	runner.SetCombinedAudioRenderer(audioRenderer)
	runner.SetOverlayRegistry(capabilityoverlay.DefaultChrononOverlayRegistry)
	runner.SetOverlayPrepareEnqueuer(prepEnq)
	runner.SetOverlayRenderEnqueuer(renderEnq)
	runner.SetVidRushBarrier(cert3SceneBarrier())

	req := defaultTestRequest()
	req.Audio = capabilityaudio.AudioModeCombinedTimeline
	req.Source.Type = SourceText
	req.SourceLanguage = "en"
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.Project = "3scene-cert"

	runID := "run-3scene-vertical-slice"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status, "vertical slice must complete: %s", final.ErrorMessage)

	res := final.Result
	require.NotNil(t, res)
	require.Len(t, res.Scenes, 3, "all 3 scenes must survive")

	// ── GATE 1: AUTO ENTITIES 3/3 ──────────────────────────────────
	// Each scene carries its own grounded per-scene EntityResult (the same
	// model as the document aggregate) with entity_overlay_required=true.
	for i, scene := range res.Scenes {
		require.NotNil(t, scene.Entities, "scene %d must carry its per-scene EntityResult", i)
		require.Len(t, scene.Entities.Persons, 1, "scene %d must carry exactly one extracted PERSON", i)
		require.True(t, scene.EntityOverlayRequired, "scene %d with an entity must set entity_overlay_required", i)
	}

	// ── GATE 2: OVERLAY INTENTS 3/3 ────────────────────────────────
	// One PENDING intent per scene, template resolved through the single
	// registry (PERSON → person_default).
	require.Len(t, res.OverlayIntents, 3, "one intent per scene")
	scenesWithIntents := map[string]bool{}
	for _, intent := range res.OverlayIntents {
		require.Equal(t, capabilityoverlay.TimingStatePending, intent.TimingState, "intent %q must be pre-timing (PENDING)", intent.IntentID)
		require.Equal(t, "person_default", intent.TemplateID, "PERSON must resolve to person_default via the single registry")
		scenesWithIntents[intent.SceneID] = true
	}
	for _, id := range []string{"scene-0", "scene-1", "scene-2"} {
		require.True(t, scenesWithIntents[id], "scene %s must have an intent", id)
	}

	// ── GATE 3: PREPARE BEFORE TTS 3/3 ─────────────────────────────
	// overlay.prepare was enqueued exactly once with the 3 PENDING intents —
	// it ran from entity extraction alone, never waiting for timing/audio.
	require.Len(t, prepEnq.reqs, 1, "overlay.prepare must be enqueued once")
	prep := prepEnq.reqs[0]
	require.Equal(t, runID, prep.PlanID)
	require.NoError(t, prep.Validate())
	require.Len(t, prep.Intents, 3, "prepare must carry one intent per scene")
	for _, intent := range prep.Intents {
		require.Equal(t, capabilityoverlay.TimingStatePending, intent.TimingState, "prepare intents must be pre-timing")
	}

	// ── GATE 4: OVERLAY RENDER 3/3 (frozen plan) ──────────────────
	// The render enqueuer received the frozen OverlayPlan with 3
	// microsecond-timed items — one per entity — after CanonicalTimeline was
	// frozen. Every item carries certified integer microsecond timing.
	frozenPlan := renderEnq.lastPlan()
	require.NotEmpty(t, frozenPlan.PlanID, "overlay.render must be enqueued with the frozen plan")
	require.NoError(t, frozenPlan.Validate())
	require.Len(t, frozenPlan.Items, 3, "frozen plan must carry 3 overlay items")
	require.Equal(t, capabilityoverlay.DefaultOverlayContractV1.ID, frozenPlan.MediaContract, "plan must resolve the overlay media contract")
	for _, item := range frozenPlan.Items {
		require.Greater(t, item.DurationUS, int64(0), "item %q must carry certified microsecond timing", item.ID)
		require.Equal(t, "person_default", item.TemplateID, "item %q must use the resolved template", item.ID)
	}

	// The render reference is persisted with the certified artifact identity
	// (probed facts + Drive publication, consumed verbatim by the editing
	// timeline). The real ffprobe + Drive upload are certified in the render
	// worker (TestOverlayEndToEnd_PlanRenderPublishPersist).
	require.NotNil(t, res.OverlayRender, "render reference must be persisted")
	require.NotNil(t, res.OverlayRender.Artifact, "render reference must carry the certified artifact")
	require.Equal(t, "overlay-sha256-certified", res.OverlayRender.Artifact.SHA256)
	require.Equal(t, "https://drive.google.com/file/d/drive-overlay-001/view", res.OverlayRender.Artifact.DriveLink)

	// ── GATE 5: FINAL AUDIO 1 ──────────────────────────────────────
	require.NotNil(t, res.FinalAudio, "final_audio.m4a must be certified")
	require.True(t, res.FinalAudio.FinalMix, "final audio must be the certified master mix")
	require.NotEmpty(t, res.FinalAudio.FinalAudioSHA256, "final audio must carry its integrity hash")
	require.Equal(t, 1, audioRenderer.calls, "master audio must be rendered exactly once")
	require.NotNil(t, res.CanonicalTimeline, "canonical timeline must be persisted")

	// ── GATE 6: EDITING TIMELINE 1 ─────────────────────────────────
	require.NotNil(t, res.EditingTimeline, "one EditingTimelineV1 must be projected")
	et := res.EditingTimeline
	require.NoError(t, et.Validate())
	require.Equal(t, EditingTimelineVersion, et.Version)
	require.Equal(t, EditingTimebase, et.Timebase)
	require.Len(t, et.Scenes, 3, "editing timeline must project all 3 scenes")
	require.Len(t, et.Overlays, 3, "editing timeline must project all 3 overlay spans")
	require.Equal(t, res.FinalAudio.FinalAudioSHA256, et.Audio.SHA256, "editing timeline audio SHA must equal the certified final audio")

	// ── GATE 7: TIMING MISMATCH 0 ──────────────────────────────────
	// Every overlay span is projected from the canonical entity timeline
	// (microsecond word boundaries), never a second independent calculation.
	require.NotNil(t, res.EntityTimeline, "entity timeline must be projected for timing grounding")
	type occKey struct{ scene, name string }
	canonicalOcc := map[occKey]struct{ startUS, endUS int64 }{}
	for _, scene := range res.EntityTimeline.Scenes {
		for _, occ := range scene.Entities {
			canonicalOcc[occKey{scene.SceneID, occ.Name}] = struct{ startUS, endUS int64 }{occ.AudioStartUS, occ.AudioEndUS}
		}
	}
	for _, ov := range et.Overlays {
		require.Greater(t, ov.EndUS, ov.StartUS, "overlay %q must have a valid time range", ov.ArtifactID)
		require.LessOrEqual(t, ov.EndUS, et.DurationUS, "overlay %q must not exceed the canonical duration", ov.ArtifactID)
		// The overlay identity flows from the certified render artifact.
		require.Equal(t, "overlay-sha256-certified", ov.SHA256, "overlay span must carry the certified artifact SHA")
		require.Equal(t, "https://drive.google.com/file/d/drive-overlay-001/view", ov.DriveLink, "overlay span must carry the Drive link")
		require.Equal(t, capabilityoverlay.DefaultOverlayContractV1.ID, ov.MediaContract, "overlay span must carry the media contract")
		// Zero timing mismatch: the overlay span's start/end must equal the
		// canonical entity occurrence timing from the EntityTimeline SSOT.
		occ, ok := canonicalOcc[occKey{ov.SceneID, ov.Entity}]
		require.True(t, ok, "overlay %q entity %q must map to a canonical occurrence", ov.ArtifactID, ov.Entity)
		require.Equal(t, occ.startUS, ov.StartUS, "overlay %q start must match canonical entity timing", ov.ArtifactID)
		require.Equal(t, occ.endUS, ov.EndUS, "overlay %q end must match canonical entity timing", ov.ArtifactID)
	}

	// Scenes are contiguous and cover the canonical timeline exactly.
	require.Equal(t, int64(0), et.Scenes[0].StartUS)
	for i := 1; i < len(et.Scenes); i++ {
		require.Equal(t, et.Scenes[i-1].EndUS, et.Scenes[i].StartUS, "scene %d/%d must be contiguous", i-1, i)
	}
	require.Equal(t, et.DurationUS, et.Scenes[2].EndUS, "scenes must cover the canonical duration exactly")
}
