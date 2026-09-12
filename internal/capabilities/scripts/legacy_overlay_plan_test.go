package scriptgeneration

import (
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestCompileLegacyOverlayPlan_PreservesEntityImagePresetAndTiming(t *testing.T) {
	text := "Ada Lovelace designed the Analytical Engine."
	words := strings.Fields(text)
	timingWords := make([]capabilityaudio.SpeechWordTiming, len(words))
	for i, word := range words {
		timingWords[i] = capabilityaudio.SpeechWordTiming{
			Index: i, Text: word, StartUS: int64(i) * 100_000, EndUS: int64(i+1) * 100_000,
		}
	}
	timing := &capabilityaudio.SpeechTimingArtifact{
		Version:      capabilityaudio.SpeechTimingVersion,
		Provider:     "test-tts",
		BoundaryMode: capabilityaudio.BoundaryWord,
		Language:     "en",
		TextSHA256:   "text-sha",
		AudioSHA256:  "audio-sha",
		DurationUS:   int64(len(words)) * 100_000,
		Words:        timingWords,
	}

	result := &scriptpkg.GenerationResult{
		ItemID:    "batch-item",
		Title:     "Legacy overlay image",
		AudioMode: "COMBINED_TIMELINE",
		Output: scriptpkg.ScriptOutput{
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{{
					ID:    "scene-0",
					Index: 0,
					Text:  text,
					Annotations: &scriptpkg.SceneAnnotations{
						Version: 1, Language: "en",
						ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "Analytical Engine", Score: 0.9}},
						PrimaryEntities: []scriptpkg.AnnotatedEntity{{
							CanonicalName: "Ada Lovelace", Type: "PERSON", Confidence: 0.99,
							Image: &scriptpkg.EntityImageBinding{
								Status: "resolved", AssetID: "ada-image", PreviewURL: "https://cdn.example/ada.jpg",
								SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
							},
						}},
					},
					Bindings: scriptpkg.SceneBindings{Voiceover: &scriptpkg.VoiceoverBinding{
						Status: "completed", DurationMs: int64(len(words)) * 100,
					}},
				}},
			},
		},
	}

	style := &scriptpkg.OverlayStyleSpec{Color: []float64{0.1, 0.2, 0.3, 1}}
	plan, err := CompileOverlayPlanFromGenerationResultWithStyle(
		result, Language("en"), map[string]*capabilityaudio.SpeechTimingArtifact{"en:0": timing},
		nil, style, "batch-plan", "project",
	)
	if err != nil {
		t.Fatalf("compile legacy overlay plan: %v", err)
	}
	if plan == nil || len(plan.Items) == 0 {
		t.Fatal("legacy bridge produced no overlay items")
	}

	var foundEntity, foundPhrase bool
	for _, item := range plan.Items {
		if item.TemplateID == "PERSON" || item.TemplateID == "person_default" {
			foundEntity = true
			if item.ImagePresetID == "" {
				t.Error("entity card has no image preset")
			}
			if len(item.AssetRefs) != 1 || item.AssetRefs[0].SHA256 == "" {
				t.Errorf("entity card has no verified image asset: %+v", item.AssetRefs)
			}
			if item.DurationUS <= 0 || item.StartUS != 0 {
				t.Errorf("entity card is not anchored to real timing: start=%d duration=%d", item.StartUS, item.DurationUS)
			}
		}
		if item.TemplateID == "IMPORTANT_PHRASE" {
			foundPhrase = true
			if item.PresetID == "" || item.DurationUS <= 0 {
				t.Errorf("phrase item lacks preset or timing: %+v", item)
			}
		}
	}
	if !foundEntity {
		t.Fatal("legacy bridge did not produce the entity card")
	}
	if !foundPhrase {
		t.Fatal("legacy bridge did not produce the timed phrase")
	}
}
