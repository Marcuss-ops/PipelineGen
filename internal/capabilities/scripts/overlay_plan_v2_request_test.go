package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
		}, Images: []capabilityoverlay.ImageCandidate{{
			AssetID: "asset-1", URL: "https://cdn.example/one.jpg", SHA256: strings.Repeat("b", 64), MediaType: "image/jpeg",
			StartUS: 0, DurationUS: 1_000_000, Score: 0.5,
		}}, Maps: []capabilityoverlay.MapCandidate{{
			EntityID: "place:rome", Label: "Roma", Latitude: 41.9, Longitude: 12.5,
			StartUS: 0, DurationUS: 1_000_000, Score: 0.7,
		}}}}, nil, nil, map[string][2]int64{"scene-1": {0, 2_000_000}})
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
	if len(wireScene.Annotations.Images) != 1 || wireScene.Annotations.Images[0].AssetID != "asset-1" {
		t.Fatalf("scene images: %+v", wireScene.Annotations.Images)
	}
	if len(wireScene.Annotations.Maps) != 1 || wireScene.Annotations.Maps[0].Label != "Roma" {
		t.Fatalf("scene maps: %+v", wireScene.Annotations.Maps)
	}
	if request.Seal == nil || request.Seal.AudioDurationUS != 2_000_000 || request.Seal.OutputProfileID != canvas.OutputProfileID {
		t.Fatalf("seal: %+v", request.Seal)
	}
	if request.Budgets == nil || request.Budgets.PhraseOverlays != 4 || request.Budgets.MaxPerScene != 5 {
		t.Fatalf("budgets: %+v", request.Budgets)
	}
	if request.SceneWindows["scene-1"] != [2]int64{0, 2_000_000} {
		t.Fatalf("scene windows: %+v", request.SceneWindows)
	}
	if request.EntityTimeline == nil || request.EntityTimeline.Version != 1 || request.EntityTimeline.DurationUS != 2_000_000 {
		t.Fatalf("entity timeline: %+v", request.EntityTimeline)
	}
	if request.Media == nil {
		t.Fatal("media section is required by the v2 contract")
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

func TestBuildOverlayPlanV2RequestCarriesRawSurfacesNotTextSelection(t *testing.T) {
	result := phraseCeilingFixture(t)
	input, err := prepareOverlayPlanCompile(result, "en", GoldenOverlayCanvas,
		"plan-raw", "video-raw", "project-raw", nil)
	require.NoError(t, err)
	require.NotNil(t, input)

	request, err := buildOverlayPlanV2Request(input.result, input.language, input.canvas,
		input.planID, input.videoID, input.projectID, input.motionSeed,
		input.timedScenes, input.scenes, input.rawScenes, input.imagePlan, input.timelineBounds)
	require.NoError(t, err)
	require.Len(t, request.Scenes, 1)
	wire := request.Scenes[0]
	require.NotNil(t, wire.Raw, "production must carry the raw surface")
	require.NotEmpty(t, wire.Raw.ImportantPhrases, "the phrase candidates must travel raw")
	require.NotNil(t, request.NumericLexicon, "the Rust cleaner must receive its vocabulary")
	require.NotNil(t, request.ImagePlan, "Rust must receive the image eligibility/ceiling")
	require.True(t, request.ImagePlan.Enabled)
	require.NotNil(t, request.Media, "verified image candidates must travel as media facts")
	if wire.Annotations != nil {
		require.Empty(t, wire.Annotations.Phrases, "Go must not pre-select phrases")
		require.Empty(t, wire.Annotations.Numbers, "Go must not pre-select numbers")
		require.Empty(t, wire.Annotations.Maps, "Go must not pre-select maps")
	}
}

func TestOverlayPlanClipsProjectsCanonicalSpans(t *testing.T) {
	result := &GenerateResult{CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
		Version:    capabilityaudio.TimelineVersion,
		DurationUS: 10_000_000,
		Segments: []capabilityaudio.TimelineSegment{{
			ID:              "scene-1",
			TimelineStartUS: 1_000_000,
			DurationUS:      5_000_000,
			VideoSegments: []capabilityaudio.VideoSegment{
				{AssetID: "asset-a", TimelineOffsetUS: 0, TimelineDurationUS: 2_000_000},
				{AssetID: "asset-b", TimelineOffsetUS: 2_000_000, TimelineDurationUS: 1_000_000, Freeze: true},
			},
		}},
	}}
	clips := overlayPlanClips(result, map[string]struct{}{"scene-1": {}})
	require.Len(t, clips, 2)
	require.Equal(t, overlayplan.Clip{
		SceneID: "scene-1", AssetID: "asset-a", Index: 0,
		StartUS: 1_000_000, DurationUS: 2_000_000,
	}, clips[0])
	require.True(t, clips[1].Freeze, "the freeze tail must stay explicit")
	require.Equal(t, int64(3_000_000), clips[1].StartUS)
	require.Nil(t, overlayPlanClips(nil, nil))
	require.Nil(t, overlayPlanClips(result, nil), "no plan scene means no clip")
}

// A fixed-media intro lives in the canonical timeline but not in the editorial
// plan. Projecting its span as a clip made the request contradict itself and the
// Rust compiler rejected every final job that carried an intro.
func TestBuildOverlayPlanV2RequestKeepsClipsInsideThePlanScenes(t *testing.T) {
	narration := Scene{
		ID: "scene-01", DurationMS: 2_000, ExecutionMode: "body",
		Text: map[Language]string{"it": "Nike corre."},
		Voiceover: map[Language]AudioReference{"it": {Timing: &capabilityaudio.SpeechTimingArtifact{
			DurationUS: 2_000_000,
			Words:      []capabilityaudio.SpeechWordTiming{{Index: 0, Text: "Nike", StartUS: 0, EndUS: 400_000}},
		}}},
	}
	result := &GenerateResult{
		SourceLanguage: "it",
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			Version:    capabilityaudio.TimelineVersion,
			DurationUS: 8_000_000,
			Segments: []capabilityaudio.TimelineSegment{
				{
					ID: "scene-intro", FixedMedia: true, TimelineStartUS: 0, DurationUS: 6_000_000,
					VideoSegments: []capabilityaudio.VideoSegment{
						{AssetID: "intro-clip", SourceDurationUS: 6_000_000, TimelineDurationUS: 6_000_000},
					},
				},
				{
					ID: "scene-01", TimelineStartUS: 6_000_000, DurationUS: 2_000_000,
					Video: capabilityaudio.VideoSegment{AssetID: "asset-narration", SourceDurationUS: 2_000_000, TimelineDurationUS: 2_000_000},
				},
			},
		},
	}
	request, err := buildOverlayPlanV2Request(result, "it", OverlayCanvasSpec{
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, MaxOverlaysPerScene: 5,
	}, "plan-1", "video-1", "project-1", "seed-1",
		[]Scene{narration}, nil, nil, nil, map[string][2]int64{"scene-01": {6_000_000, 8_000_000}})
	require.NoError(t, err)
	require.Len(t, request.Scenes, 1)
	require.Equal(t, "scene-01", request.Scenes[0].ID)
	require.Len(t, request.Clips, 1, "the intro span is not part of this plan")
	require.Equal(t, "scene-01", request.Clips[0].SceneID)
	require.Equal(t, int64(6_000_000), request.Clips[0].StartUS)
	require.NotContains(t, request.SceneWindows, "scene-intro", "an intro window is not an editorial scene window")
}

func TestBuildOverlayPlanV2RequestFailsClosedOnMissingIdentity(t *testing.T) {
	_, err := buildOverlayPlanV2Request(&GenerateResult{}, "it", OverlayCanvasSpec{}, "", "video-1", "", "",
		nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "plan_id and video_id are required") {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildOverlayPlanV2RequestFailsClosedOnMissingBounds(t *testing.T) {
	result := &GenerateResult{}
	scene := Scene{ID: "scene-1", Text: map[Language]string{"it": "Nike corre."}}
	_, err := buildOverlayPlanV2Request(result, "it", OverlayCanvasSpec{}, "plan-1", "video-1", "", "",
		[]Scene{scene}, nil, nil, nil, map[string][2]int64{})
	if err == nil || !strings.Contains(err.Error(), "no canonical timeline bounds") {
		t.Fatalf("error=%v", err)
	}
}
