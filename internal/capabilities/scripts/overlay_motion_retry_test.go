package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestCompileOverlayPlanRetryKeepsStableIndependentImageMotions verifies
// deterministic plan identity selection end-to-end. Recompilation of one plan
// is an idempotent retry; a distinct plan ID rotates the certified choices.
func TestCompileOverlayPlanRetryKeepsStableIndependentImageMotions(t *testing.T) {
	const (
		portraitOneHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		portraitTwoHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	words := []capabilityaudio.SpeechWordTiming{
		{Index: 0, Text: "Ada", StartUS: 0, EndUS: 500_000},
		{Index: 1, Text: "met", StartUS: 500_000, EndUS: 1_000_000},
		{Index: 2, Text: "Grace", StartUS: 1_000_000, EndUS: 1_500_000},
		{Index: 3, Text: "today", StartUS: 1_500_000, EndUS: 2_000_000},
	}
	timing := capabilityaudio.SpeechTimingArtifact{
		Version: capabilityaudio.SpeechTimingVersion, Provider: "test", BoundaryMode: capabilityaudio.BoundaryWord,
		Language: "en", TextSHA256: "script-hash", AudioSHA256: "voiceover-hash", DurationUS: 2_000_000, Words: words,
	}
	entity := func(name, assetID, hash, url string, start, end int64, wordIndex int) (scriptpkg.AnnotatedEntity, capabilityentities.EntityOccurrence) {
		entityID := capabilityentities.StableEntityID("PERSON", name)
		return scriptpkg.AnnotatedEntity{
				ID: entityID, Text: name, CanonicalName: name, Type: "PERSON", Confidence: 0.99,
				CanonicalEntityID: "person:" + strings.ToLower(strings.ReplaceAll(name, " ", "-")),
				Image:             &scriptpkg.EntityImageBinding{Status: "bound", AssetID: assetID, SHA256: hash, PreviewURL: url, MediaType: "image/jpeg"},
			}, capabilityentities.EntityOccurrence{
				EntityID: entityID, Name: name, Type: "PERSON", SceneID: "scene-0", SceneIndex: 0,
				TextStart: wordIndex * 2, TextEnd: wordIndex*2 + len(name), WordStart: wordIndex, WordEnd: wordIndex,
				LocalStartUS: start, LocalEndUS: end, TimelineStartUS: 0, AudioStartUS: start, AudioEndUS: end, Confidence: 0.99,
			}
	}
	ada, adaOccurrence := entity("Ada Lovelace", "ada-asset", portraitOneHash, "https://cdn.example.test/ada.jpg", 0, 500_000, 0)
	grace, graceOccurrence := entity("Grace Hopper", "grace-asset", portraitTwoHash, "https://cdn.example.test/grace.jpg", 1_000_000, 1_500_000, 2)
	result := &GenerateResult{
		Scenes: []Scene{{
			ID: "scene-0", Index: 0,
			Text:        map[Language]string{"en": "Ada Lovelace met Grace Hopper today."},
			Voiceover:   map[Language]AudioReference{"en": {ID: "voiceover-0", Duration: 2, Timing: &timing}},
			Annotations: &scriptpkg.SceneAnnotations{Version: 1, Language: "en", Status: "completed", PrimaryEntities: []scriptpkg.AnnotatedEntity{ada, grace}},
		}},
		SourceLanguage: "en",
		ResolvedScenes: []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: 2_000_000}},
		EntityTimeline: &capabilityentities.EntityTimeline{
			Version: capabilityentities.EntityTimelineVersion, Language: "en", DurationUS: 2_000_000,
			Scenes: []capabilityentities.SceneEntityTimeline{{
				SceneID: "scene-0", SceneIndex: 0, TimelineStartUS: 0,
				Entities: []capabilityentities.EntityOccurrence{adaOccurrence, graceOccurrence},
			}},
		},
		Segments: []scriptpkg.VidRushSegmentResult{{Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			{AssetID: "ada-asset", LocalPath: ""}, {AssetID: "grace-asset", LocalPath: ""},
		}}}},
	}

	motionsForPlan := func(planID string) ([]string, string) {
		t.Helper()
		plan, err := compileOverlayPlanWithMotionOffset(result, "en", GoldenOverlayCanvas, planID, "retry-video", "retry-project", nil, nil)
		if err != nil {
			t.Fatalf("compile plan %q: %v", planID, err)
		}
		if plan == nil {
			t.Fatalf("plan %q produced no output", planID)
		}
		if err := plan.Validate(); err != nil {
			t.Fatalf("plan %q is invalid: %v", planID, err)
		}
		if len(plan.Items) != 1 || len(plan.Items[0].ImageLayers) != 2 {
			t.Fatalf("plan %q should produce one two-image composite, got %+v", planID, plan.Items)
		}
		item := plan.Items[0]
		if item.StartMs != 0 || item.EndMs != 2000 || item.ImageLayers[1].StartMS != 1000 || item.ImageLayers[0].EndMS != 2000 || item.ImageLayers[1].EndMS != 2000 {
			t.Fatalf("plan %q lost the staggered mention timing: %+v", planID, item)
		}
		got := []string{item.ImageLayers[0].MotionID, item.ImageLayers[1].MotionID}
		certified := capabilityoverlay.CertifiedImageMotions()
		if got[0] == got[1] {
			t.Fatalf("plan %q reused one motion for both portraits: %v", planID, got)
		}
		for _, id := range got {
			if !containsMotionID(certified, id) {
				t.Fatalf("plan %q emitted uncertified image motion %q", planID, id)
			}
		}
		wire, err := json.Marshal(plan)
		if err != nil {
			t.Fatalf("marshal plan %q: %v", planID, err)
		}
		return got, string(wire)
	}

	first, firstWire := motionsForPlan("retry-job")
	retry, retryWire := motionsForPlan("retry-job")
	if first[0] != retry[0] || first[1] != retry[1] || firstWire != retryWire {
		t.Fatalf("retry changed the serialized render plan: motions %v vs %v", first, retry)
	}
	newPlan, _ := motionsForPlan("retry-job-revision-2")
	if first[0] == newPlan[0] && first[1] == newPlan[1] {
		t.Fatalf("distinct plan identity reused the full image motion rotation: old=%v new=%v", first, newPlan)
	}
}

func containsMotionID(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}
