package overlays

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSemanticRenderBundleV1_RejectsUngroundedEntity(t *testing.T) {
	b := SemanticRenderBundleV1{
		Version:  SemanticRenderBundleVersion,
		RunID:    "run-1",
		Scene:    NewSceneIR("scene-1", 0, "Gerard Butler spoke in London.", "", SegmentSemanticProfile{}),
		Entities: []ResolvedEntity{{EntityID: "person:gerard-butler", Type: "PERSON", Text: "Gerard", CanonicalText: "Gerard Butler", Evidence: "Gerard Butler", Start: 0, End: 13, Confidence: .97, SceneID: "scene-1"}},
	}
	if err := b.Validate(); err == nil {
		t.Fatal("expected source-grounding validation failure")
	}
}

func TestTimelinePlannerAndBundleBuildOverlayPlan(t *testing.T) {
	scene := NewSceneIR("scene-1", 0, "Gerard Butler spoke in London.", "", SegmentSemanticProfile{})
	entities := []ResolvedEntity{
		{EntityID: "person:gerard-butler", Type: "PERSON", Text: "Gerard Butler", CanonicalText: "Gerard Butler", Evidence: "Gerard Butler", Start: 0, End: 13, Confidence: .97, SceneID: scene.SegmentID},
		{EntityID: "location:london", Type: "LOCATION", Text: "London", CanonicalText: "London", Evidence: "London", Start: 23, End: 29, Confidence: .99, SceneID: scene.SegmentID},
	}
	planner := TimelinePlanner{}
	timeline, err := planner.Plan(10_000, entities, map[string]EntityTiming{
		"person:gerard-butler": {EntityID: "person:gerard-butler", StartMs: 2300, EndMs: 2800},
		"location:london":      {EntityID: "location:london", StartMs: 6500, EndMs: 7000},
	}, map[string]string{
		"person:gerard-butler": string(PresetModernName),
		"location:london":      string(PresetModernName),
	})
	if err != nil {
		t.Fatalf("plan timeline: %v", err)
	}
	if timeline[0].StartMs != 2050 || timeline[0].EndMs != 4550 {
		t.Fatalf("unexpected person window: %+v", timeline[0])
	}
	b := SemanticRenderBundleV1{Version: SemanticRenderBundleVersion, RunID: "run-1", Scene: scene, Entities: entities, Timeline: timeline}
	plan, err := BuildOverlayPlan(b, "video-1", "project-1", 1920, 1080, 24, 1)
	if err != nil {
		t.Fatalf("build overlay plan: %v", err)
	}
	if plan.SchemaVersion != SchemaVersionPlan || plan.FPSNum != 24 || len(plan.Items) != 2 || plan.Fingerprint == "" {
		t.Fatalf("unexpected overlay plan: %+v", plan)
	}
	if plan.Items[0].PresetID != string(PresetModernName) {
		t.Fatalf("preset was not carried into overlay plan: %+v", plan.Items[0])
	}
}

func TestBuildOverlayPlanUsesCanonicalImageCapability(t *testing.T) {
	scene := NewSceneIR("scene-1", 0, "Gerard Butler", "", SegmentSemanticProfile{})
	entityID := "person:gerard-butler"
	digest := sha256.Sum256([]byte("portrait"))
	bundle := SemanticRenderBundleV1{
		Version: SemanticRenderBundleVersion, RunID: "run-image", Scene: scene,
		Entities: []ResolvedEntity{{EntityID: entityID, Type: "PERSON", Text: "Gerard Butler", CanonicalText: "Gerard Butler", Evidence: "Gerard Butler", Start: 0, End: len(scene.SourceText), Confidence: .97, SceneID: scene.SegmentID}},
		Timeline: []TimelineEvent{{EntityID: entityID, StartMs: 0, EndMs: 2500, PresetID: string(PresetModernName)}},
		Assets:   []BoundAsset{{EntityID: entityID, AssetID: "asset-1", ContentHash: hex.EncodeToString(digest[:]), SourceURL: "https://example.test/portrait.jpg", Verified: true}},
	}
	plan, err := BuildOverlayPlan(bundle, "video-1", "project-1", 1920, 1080, 24, 1)
	if err != nil {
		t.Fatal(err)
	}
	item := plan.Items[0]
	if item.Kind != string(KindEntityImage) || item.TemplateID != "image_popup" || item.PresetID != string(PresetModernImage) {
		t.Fatalf("image item = %+v", item)
	}
}
