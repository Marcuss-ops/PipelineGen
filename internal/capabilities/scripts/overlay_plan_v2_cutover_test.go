package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

type recordingOverlayPlanV2Compiler struct {
	payload json.RawMessage
	plan    *capmodel.OverlayPlan
	calls   int
}

func (r *recordingOverlayPlanV2Compiler) CompileOverlayPlanV2(_ context.Context, payload json.RawMessage) (*capmodel.OverlayPlan, error) {
	r.calls++
	r.payload = append(json.RawMessage(nil), payload...)
	return r.plan, nil
}

func TestCompileOverlayPlanV2SendsOneCompleteRequestAndAdoptsThePlan(t *testing.T) {
	scene := Scene{
		ID: "s1", DurationMS: 3_000,
		Text: map[Language]string{"it": "Nike corre al museo."},
		Voiceover: map[Language]AudioReference{"it": {Timing: &capabilityaudio.SpeechTimingArtifact{
			DurationUS: 3_000_000,
			Words:      []capabilityaudio.SpeechWordTiming{{Index: 0, Text: "Nike", StartUS: 100_000, EndUS: 400_000}},
		}}},
	}
	result := &GenerateResult{SourceLanguage: "it", Scenes: []Scene{scene}}
	canvas := OverlayCanvasSpec{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1}
	scenes := []capabilityoverlay.SceneInput{{
		ID: "s1",
		Phrases: []capabilityoverlay.TimedAnnotation{{
			Text: "Nike corre al museo.", StartUS: 100_000, DurationUS: 1_200_000, Score: 0.9,
		}},
	}}
	bounds := map[string][2]int64{"s1": {0, 3_000_000}}
	plan := &capmodel.OverlayPlan{
		PlanID: "plan-1", VideoID: "video-1",
		Items: []capmodel.OverlayItem{{ID: "item-1", Kind: "text_phrase"}},
	}
	compiler := &recordingOverlayPlanV2Compiler{plan: plan}

	adopted, err := compileOverlayPlanV2(context.Background(), compiler, result, "it", canvas,
		"plan-1", "video-1", "project-1", "seed-1", []Scene{scene}, scenes, nil, nil, bounds, nil)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	if compiler.calls != 1 {
		t.Fatalf("calls=%d, want one single call", compiler.calls)
	}
	if adopted != plan {
		t.Fatal("the returned plan must be adopted, not rebuilt")
	}
	if !strings.Contains(string(compiler.payload), `"schema_version":"editorial_plan.v2"`) {
		t.Fatalf("payload is not the v2 envelope: %s", compiler.payload)
	}
	var request overlayplan.V2Request
	if err := json.Unmarshal(compiler.payload, &request); err != nil {
		t.Fatalf("decode v2 request: %v", err)
	}
	if request.Seal == nil || request.Media == nil || request.Budgets == nil || request.EntityTimeline == nil {
		t.Fatalf("request is missing a required section: %+v", request)
	}
	if request.SceneWindows["s1"] != [2]int64{0, 3_000_000} {
		t.Fatalf("scene windows: %+v", request.SceneWindows)
	}
	if request.Scenes[0].Annotations == nil || len(request.Scenes[0].Annotations.Phrases) != 1 {
		t.Fatalf("scene annotations: %+v", request.Scenes[0].Annotations)
	}
}

func TestCompileOverlayPlanWithRustResolverNoOpAndIdentity(t *testing.T) {
	plan, err := CompileOverlayPlanWithRustResolver(context.Background(), nil, "en", GoldenOverlayCanvas,
		"plan-1", "video-1", "", nil, passthroughOverlayPlanCompiler{})
	if err != nil {
		t.Fatalf("nil result: %v", err)
	}
	if plan != nil {
		t.Fatalf("nil result produced a plan: %+v", plan)
	}

	result := &GenerateResult{Scenes: []Scene{{ID: "scene-0", Index: 0, Text: map[Language]string{"en": "Tim Cook speaks."}}}}
	plan, err = CompileOverlayPlanWithRustResolver(context.Background(), result, "en", GoldenOverlayCanvas,
		"plan-1", "video-1", "", nil, passthroughOverlayPlanCompiler{})
	if err != nil {
		t.Fatalf("surfaceless result: %v", err)
	}
	if plan != nil {
		t.Fatalf("surfaceless result produced a plan: %+v", plan)
	}

	_, err = CompileOverlayPlanWithRustResolver(context.Background(), result, "en", GoldenOverlayCanvas,
		"", "video-1", "", nil, passthroughOverlayPlanCompiler{})
	if err == nil {
		t.Fatal("missing plan_id must fail closed")
	}
	_, err = CompileOverlayPlanWithRustResolver(context.Background(), result, "en", GoldenOverlayCanvas,
		"plan-1", "", "", nil, passthroughOverlayPlanCompiler{})
	if err == nil {
		t.Fatal("missing video_id must fail closed")
	}
}
