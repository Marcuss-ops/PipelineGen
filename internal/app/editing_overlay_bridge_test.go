package app

import (
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestEditingOverlayBridge_ClipRenderCarriesSameLineage certifies Gate 7's
// editing-timeline → clip.render hop: the OverlayRefSpec projected from an
// EditingOverlaySpan must carry the SAME render_job_id / plan_fingerprint /
// render_key / source_video_asset_id the span carries. The final video
// request is built from the certified span, never from a second derivation —
// otherwise the video could claim an overlay it never composited.
func TestEditingOverlayBridge_ClipRenderCarriesSameLineage(t *testing.T) {
	// Seal a plan the way the runner does: Validate() freezes the item render
	// keys and the plan fingerprint.
	plan := &capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan,
		PlanID:        "test-plan",
		VideoID:       "source-video-asset-001",
		Width:         1920,
		Height:        1080,
		FPS:           30,
		MediaContract: "overlay-v1",
		Items: []capabilityoverlay.OverlayItem{
			{
				ID:         "overlay-scene-01-michael-jordan",
				SceneID:    "scene-01",
				Kind:       "entity_card",
				TemplateID: "person_default",
				Text:       "Michael Jordan",
				StartMs:    50,
				EndMs:      950,
				StartUS:    50000,
				DurationUS: 900000,
				EntityRef: &capabilityoverlay.OverlayEntityRef{
					EntityID: "ent-001",
					Type:     "PERSON",
					Name:     "Michael Jordan",
				},
			},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("seal plan: %v", err)
	}
	if plan.Fingerprint == "" || plan.Items[0].RenderKey == "" {
		t.Fatalf("sealed plan must carry fingerprint and item render key")
	}

	// Build the run result exactly like the certified run: frozen plan +
	// completed overlay.render queue job (+ entity timeline + intents so the
	// span carries the full lineage).
	result := &scriptgeneration.GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 15000000,
			Segments: []capabilityaudio.TimelineSegment{
				{ID: "scene-01", Index: 0, TimelineStartUS: 0, DurationUS: 15000000},
			},
		},
		FinalAudio: &scriptgeneration.FinalAudioReference{
			AssetID:          "final-audio-001",
			FinalAudioSHA256: "abc123",
			DurationUS:       15000000,
			DurationMS:       15000,
		},
		OverlayPlan: plan,
		OverlayIntents: []capabilityoverlay.OverlayIntent{
			{
				Version:     capabilityoverlay.OverlayIntentVersion,
				IntentID:    "intent-scene-01-michael-jordan",
				SceneID:     "scene-01",
				SceneIndex:  0,
				Entity:      capabilityoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Michael Jordan"},
				Source:      capabilityoverlay.IntentSourceEntity,
				Kind:        "entity_card",
				TemplateID:  "person_default",
				TimingState: capabilityoverlay.TimingStatePending,
			},
		},
		OverlayRender: &scriptgeneration.RenderReference{
			JobID:  "render-michael-jordan-overlay-001",
			Status: "COMPLETED",
			Artifact: &scriptgeneration.RenderArtifact{
				SHA256:      "overlay-sha256",
				DriveLink:   "https://drive.google.com/file/d/overlay",
				DriveFileID: "drive-overlay-1",
			},
		},
	}

	et, err := scriptgeneration.BuildEditingTimeline(result)
	if err != nil {
		t.Fatalf("BuildEditingTimeline failed: %v", err)
	}
	if len(et.Overlays) != 1 {
		t.Fatalf("expected 1 overlay span, got %d", len(et.Overlays))
	}
	span := et.Overlays[0]

	// The certified span must carry the full lineage itself.
	if span.RenderJobID != "render-michael-jordan-overlay-001" ||
		span.PlanFingerprint != plan.Fingerprint ||
		span.RenderKey != plan.Items[0].RenderKey ||
		span.SourceVideoAssetID != "source-video-asset-001" {
		t.Fatalf("span lineage incomplete: job=%q fp=%q key=%q src=%q",
			span.RenderJobID, span.PlanFingerprint, span.RenderKey, span.SourceVideoAssetID)
	}

	// THE HOP: project the span onto the clip.render wire contract.
	ref := overlayRefSpecFromEditingSpan(span)
	if ref == nil {
		t.Fatal("overlayRefSpecFromEditingSpan returned nil for a completed render")
	}

	// Identity must be byte-for-byte identical, never re-derived.
	if ref.RenderJobID != span.RenderJobID {
		t.Errorf("render_job_id: span=%q clip.render=%q", span.RenderJobID, ref.RenderJobID)
	}
	if ref.PlanFingerprint != span.PlanFingerprint {
		t.Errorf("plan_fingerprint: span=%q clip.render=%q", span.PlanFingerprint, ref.PlanFingerprint)
	}
	if ref.RenderKey != span.RenderKey {
		t.Errorf("render_key: span=%q clip.render=%q", span.RenderKey, ref.RenderKey)
	}
	if ref.SourceVideoAssetID != span.SourceVideoAssetID {
		t.Errorf("source_video_asset_id: span=%q clip.render=%q", span.SourceVideoAssetID, ref.SourceVideoAssetID)
	}
	// The declared compositing window must travel with the lineage so the
	// final video blends the segment at exactly the certified span's timing.
	if ref.StartUS != span.StartUS {
		t.Errorf("start_us: span=%d clip.render=%d", span.StartUS, ref.StartUS)
	}
	if ref.EndUS != span.EndUS {
		t.Errorf("end_us: span=%d clip.render=%d", span.EndUS, ref.EndUS)
	}

	// The projected ref must satisfy the clip.render all-or-nothing gate.
	req := &cliprender.RenderRequest{SourceAssetID: "source-video-asset-001", Overlay: ref}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Fatalf("clip.render request with projected overlay must validate: %v", err)
	}
}

// TestEditingOverlayBridge_FailClosedWithoutRender certifies the fail-closed
// half of the hop: a span whose overlay.render job has NOT completed yields
// nil — the clip.render request declares no overlay rather than a
// half-proven composition (matching OverlayRefSpec's all-or-nothing gate).
func TestEditingOverlayBridge_FailClosedWithoutRender(t *testing.T) {
	span := scriptgeneration.EditingOverlaySpan{
		ArtifactID:         "overlay-scene-01-michael-jordan",
		SceneID:            "scene-01",
		Entity:             "Michael Jordan",
		TemplateID:         "person_default",
		StartUS:            50000,
		EndUS:              950000,
		RenderJobID:        "", // render never completed
		PlanFingerprint:    "fp-001",
		RenderKey:          "key-001",
		SourceVideoAssetID: "source-video-asset-001",
	}
	if ref := overlayRefSpecFromEditingSpan(span); ref != nil {
		t.Fatalf("expected nil overlay ref for unrendered span, got %+v", ref)
	}

	// Fail-closed window: complete lineage but an invalid compositing window
	// (end <= start) is never declared.
	badWindow := scriptgeneration.EditingOverlaySpan{
		ArtifactID:         "overlay-scene-01-michael-jordan",
		SceneID:            "scene-01",
		Entity:             "Michael Jordan",
		TemplateID:         "person_default",
		StartUS:            950000,
		EndUS:              50000,
		RenderJobID:        "render-job-001",
		PlanFingerprint:    "fp-001",
		RenderKey:          "key-001",
		SourceVideoAssetID: "source-video-asset-001",
	}
	if ref := overlayRefSpecFromEditingSpan(badWindow); ref != nil {
		t.Fatalf("expected nil overlay ref for invalid window, got %+v", ref)
	}
}
