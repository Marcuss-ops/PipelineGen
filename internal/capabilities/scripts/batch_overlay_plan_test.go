package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type batchRecordingCompiler struct {
	calls   int
	request overlayplan.V2Request
}

func (c *batchRecordingCompiler) CompileOverlayPlanV2(_ context.Context, payload json.RawMessage) (*capmodel.OverlayPlan, error) {
	c.calls++
	if err := json.Unmarshal(payload, &c.request); err != nil {
		return nil, err
	}
	plan := &capmodel.OverlayPlan{
		PlanID:    c.request.Plan.PlanID,
		VideoID:   c.request.Plan.VideoID,
		ProjectID: c.request.Plan.ProjectID,
		Language:  c.request.Language,
		Width:     c.request.Plan.Width,
		Height:    c.request.Plan.Height,
		FPSNum:    c.request.Plan.FPSNum,
		FPSDen:    c.request.Plan.FPSDen,
	}
	if len(c.request.Scenes) > 0 {
		scene := c.request.Scenes[0]
		durationUS := scene.DurationUS
		if durationUS > 1_000_000 {
			durationUS = 1_000_000
		}
		endUS := scene.TimelineStartUS + durationUS
		plan.DurationMS = (endUS + 999) / 1000
		plan.Items = []capmodel.OverlayItem{{
			ID: "batch-phrase", SceneID: scene.ID, Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
			PresetID: "phrase_default", Text: scene.Text,
			StartMs: scene.TimelineStartUS / 1000, EndMs: (endUS + 999) / 1000,
			StartUS: scene.TimelineStartUS, DurationUS: durationUS,
		}}
	}
	return plan, nil
}

func TestCompileOverlayPlanFromGenerationResultUsesV2Compiler(t *testing.T) {
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
		Title:     "Batch overlay compiler",
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
						ImportantPhrases: []scriptpkg.AnnotationSpan{{Text: "the Analytical Engine", Score: 0.9}},
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
	compiler := &batchRecordingCompiler{}
	plan, err := CompileOverlayPlanFromGenerationResult(
		context.Background(), result, Language("en"), map[string]*capabilityaudio.SpeechTimingArtifact{"en:0": timing},
		nil, style, 0, "batch-plan", "project", compiler, testPhraseAligner{},
	)
	if err != nil {
		t.Fatalf("compile batch overlay plan: %v", err)
	}
	if compiler.calls != 1 {
		t.Fatalf("v2 compiler calls = %d, want 1", compiler.calls)
	}
	if plan == nil || len(plan.Items) == 0 {
		t.Fatal("batch bridge produced no overlay items")
	}
	if compiler.request.SchemaVersion != overlayplan.SchemaVersionV2 {
		t.Fatalf("schema_version = %q", compiler.request.SchemaVersion)
	}
	if len(compiler.request.Scenes) != 1 {
		t.Fatalf("scenes = %d, want 1", len(compiler.request.Scenes))
	}
	scene := compiler.request.Scenes[0]
	if scene.ID != "scene-0" || strings.TrimSpace(scene.Text) != text {
		t.Fatalf("wire scene = %+v", scene)
	}
	if scene.Voiceover == nil || scene.Voiceover.Timing == nil {
		t.Fatal("wire scene lost the certified voiceover timing")
	}
	if compiler.request.EntityTimeline == nil || len(compiler.request.EntityTimeline.Scenes) == 0 {
		t.Fatal("wire request lost the entity timeline")
	}
}
