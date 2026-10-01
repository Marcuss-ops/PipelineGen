package scriptgeneration

import (
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestCompileOverlayPlanRetrySamplesFreshIndependentImageMotions exercises the
// complete semantic-plan compilation path twice for the same job identity,
// with a fresh motion-pool sample per attempt. The injected offsets make the
// retry assertion deterministic; production supplies crypto/rand at the same
// boundary. Each composite portrait consumes a distinct ordinal on each run.
func TestCompileOverlayPlanRetrySamplesFreshIndependentImageMotions(t *testing.T) {
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

	motionsForAttempt := func(offset int) []string {
		t.Helper()
		calls := 0
		plan, err := compileOverlayPlanWithMotionOffset(result, "en", GoldenOverlayCanvas, "retry-job", "retry-video", "retry-project", func() (int, error) {
			calls++
			return offset, nil
		}, nil)
		if err != nil {
			t.Fatalf("compile attempt with offset %d: %v", offset, err)
		}
		if calls != 1 {
			t.Fatalf("attempt sampled the motion offset %d times, want exactly once", calls)
		}
		if plan == nil {
			t.Fatalf("attempt with offset %d produced no plan", offset)
		}
		if err := plan.Validate(); err != nil {
			t.Fatalf("attempt with offset %d produced invalid plan: %v", offset, err)
		}
		if len(plan.Items) != 1 || len(plan.Items[0].ImageLayers) != 2 {
			t.Fatalf("attempt with offset %d should produce one two-image composite, got %+v", offset, plan.Items)
		}
		item := plan.Items[0]
		if item.StartMs != 0 || item.EndMs != 6000 || item.ImageLayers[1].StartMS != 1000 {
			t.Fatalf("attempt with offset %d lost the staggered mention timing: %+v", offset, item)
		}
		got := []string{item.ImageLayers[0].MotionID, item.ImageLayers[1].MotionID}
		want := []string{
			capabilityoverlay.ImageMotionAtOffset(offset, 0),
			capabilityoverlay.ImageMotionAtOffset(offset, 1),
		}
		if got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("attempt with offset %d motions = %v, want independent pool selection %v", offset, got, want)
		}
		certified := capabilityoverlay.CertifiedImageMotions()
		if got[0] == got[1] {
			t.Fatalf("attempt with offset %d reused one motion for both portraits: %v", offset, got)
		}
		for _, id := range got {
			if !containsMotionID(certified, id) {
				t.Fatalf("attempt with offset %d emitted uncertified image motion %q", offset, id)
			}
		}
		return got
	}

	firstAttempt := motionsForAttempt(0)
	retryAttempt := motionsForAttempt(1)
	if firstAttempt[0] == retryAttempt[0] && firstAttempt[1] == retryAttempt[1] {
		t.Fatalf("retry reused both previous portrait motions: first=%v retry=%v", firstAttempt, retryAttempt)
	}
	if firstAttempt[0] == retryAttempt[0] || firstAttempt[1] == retryAttempt[1] {
		t.Fatalf("retry should resample both portrait motions independently: first=%v retry=%v", firstAttempt, retryAttempt)
	}

	// A worker replay consumes the already compiled/serialized plan; this test
	// models a fresh generation attempt by invoking the compiler again.
}

func containsMotionID(pool []string, id string) bool {
	for _, candidate := range pool {
		if candidate == id {
			return true
		}
	}
	return false
}
