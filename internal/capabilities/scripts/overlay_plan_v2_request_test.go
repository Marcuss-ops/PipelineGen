package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildOverlayPlanV2RequestMapsTheVersionedEnvelope(t *testing.T) {
	title := "Nike corre"
	result := &GenerateResult{
		SourceLanguage: "it",
		Editorial: &scriptpkg.EditorialManifest{
			SchemaVersion:  overlayplan.EditorialManifestVersion,
			SourceLanguage: "it",
			ProfileVersion: overlayplan.EditorialProfileVersion,
			Certified:      true,
			Fingerprint:    strings.Repeat("a", 64),
			Scenes: []scriptpkg.EditorialScene{{
				SceneID: "scene-1", Title: &title, TitleStatus: "resolved", TitleSource: "sentence",
				Bullets: []scriptpkg.EditorialSceneBullet{{SentenceStart: 0, SentenceEnd: 1, Text: "Nike corre."}},
			}},
		},
		ChapterManifest: &scriptpkg.ChapterManifest{
			SchemaVersion: overlayplan.ChapterManifestVersion,
			Chapters: []scriptpkg.ChapterManifestEntry{{
				Bullets: []scriptpkg.ChapterBullet{{StartSentence: 0, EndSentence: 1, Text: "Nike corre."}},
			}},
		},
	}
	scene := Scene{
		ID: "scene-1", DurationMS: 2_000, ExecutionMode: "body",
		Text: map[Language]string{"it": "Nike corre. La statua è al museo."},
		Voiceover: map[Language]AudioReference{"it": {Timing: &capabilityaudio.SpeechTimingArtifact{
			DurationUS: 2_000_000,
			Words: []capabilityaudio.SpeechWordTiming{
				{Index: 0, Text: "Nike", StartUS: 100_000, EndUS: 400_000},
				{Index: 1, Text: "corre", StartUS: 400_000, EndUS: 800_000},
			},
		}}},
		OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryTitles: "enabled",
		},
	}
	canvas := OverlayCanvasSpec{
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryBullets: "disabled",
		},
		MaxOverlaysPerScene: 5, MaxPhraseOverlays: 4, MaxImageOverlays: 3, MaxMapOverlays: 2,
	}

	request, err := buildOverlayPlanV2Request(result, "it", canvas, "plan-1", "video-1", "project-1", "seed-1",
		[]Scene{scene}, []capabilityoverlay.SceneInput{{ID: "scene-1", Phrases: []capabilityoverlay.TimedAnnotation{
			{Text: "Nike corre.", StartUS: 100_000, DurationUS: 700_000, Score: 0.9, PresentationRole: "scene_bullet"},
		}}}, map[string][2]int64{"scene-1": {0, 2_000_000}})
	if err != nil {
		t.Fatalf("build v2 request: %v", err)
	}
	if request.SchemaVersion != overlayplan.SchemaVersionV2 {
		t.Fatalf("schema_version=%q", request.SchemaVersion)
	}
	if request.Plan.PlanID != "plan-1" || request.Plan.FPSNum != 24 || request.Plan.MotionSeed != "seed-1" {
		t.Fatalf("plan identity: %+v", request.Plan)
	}
	if len(request.Scenes) != 1 {
		t.Fatalf("scenes=%d", len(request.Scenes))
	}
	wireScene := request.Scenes[0]
	if wireScene.TimelineStartUS != 0 || wireScene.DurationUS != 2_000_000 {
		t.Fatalf("scene bounds: %+v", wireScene)
	}
	if wireScene.Voiceover == nil || wireScene.Voiceover.Timing == nil || len(wireScene.Voiceover.Timing.Words) != 2 {
		t.Fatalf("scene timing: %+v", wireScene.Voiceover)
	}
	if wireScene.OverlayCategories[string(scriptpkg.OverlayCategoryTitles)] != "enabled" {
		t.Fatalf("scene categories: %+v", wireScene.OverlayCategories)
	}
	if wireScene.Annotations == nil || len(wireScene.Annotations.Phrases) != 1 ||
		wireScene.Annotations.Phrases[0].StartUS != 100_000 ||
		wireScene.Annotations.Phrases[0].PresentationRole != "scene_bullet" {
		t.Fatalf("scene annotations: %+v", wireScene.Annotations)
	}
	if request.Canvas.OverlayCategories[string(scriptpkg.OverlayCategoryBullets)] != "disabled" {
		t.Fatalf("canvas categories: %+v", request.Canvas.OverlayCategories)
	}
	if request.Editorial == nil || request.Editorial.Scenes[0].Title == nil || *request.Editorial.Scenes[0].Title != "Nike corre" {
		t.Fatalf("editorial manifest: %+v", request.Editorial)
	}
	if request.Chapters == nil || len(request.Chapters.Chapters) != 1 || len(request.Chapters.Chapters[0].Bullets) != 1 {
		t.Fatalf("chapter manifest: %+v", request.Chapters)
	}

	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded overlayplan.V2Request
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.SchemaVersion != overlayplan.SchemaVersionV2 || len(decoded.Scenes) != 1 {
		t.Fatalf("round trip: %+v", decoded)
	}
	if !strings.Contains(string(raw), `"schema_version":"editorial_plan.v2"`) ||
		!strings.Contains(string(raw), `"start_us":100000`) {
		t.Fatalf("wire shape: %s", raw)
	}
}

func TestBuildOverlayPlanV2RequestFailsClosedOnMissingIdentity(t *testing.T) {
	_, err := buildOverlayPlanV2Request(&GenerateResult{}, "it", OverlayCanvasSpec{}, "", "video-1", "", "",
		nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "plan_id and video_id are required") {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildOverlayPlanV2RequestFailsClosedOnMissingBounds(t *testing.T) {
	result := &GenerateResult{}
	scene := Scene{ID: "scene-1", Text: map[Language]string{"it": "Nike corre."}}
	_, err := buildOverlayPlanV2Request(result, "it", OverlayCanvasSpec{}, "plan-1", "video-1", "", "",
		[]Scene{scene}, nil, map[string][2]int64{})
	if err == nil || !strings.Contains(err.Error(), "no canonical timeline bounds") {
		t.Fatalf("error=%v", err)
	}
}
