package scriptgeneration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestAttachEntityCardAssetCarriesVerifiedLocalPathWithoutSerializingIt(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "verified-person.jpg")
	if err := os.WriteFile(localPath, []byte("verified image"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := &GenerateResult{
		Scenes: []Scene{{Annotations: &scriptpkg.SceneAnnotations{
			PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				ID: "person-1", Type: "PERSON", CanonicalName: "Ada Lovelace", Confidence: 0.9,
				Image: &scriptpkg.EntityImageBinding{
					Status: "resolved", AssetID: "ada-asset", SHA256: "ada-sha",
					PreviewURL: "https://drive.google.com/uc?export=download&id=ada",
					MediaType:  "image/jpeg",
				},
			},
			}}}},
		Segments: []scriptpkg.VidRushSegmentResult{{Assets: scriptpkg.SegmentAssetSelection{
			Candidates: []scriptpkg.SegmentAssetCandidate{{AssetID: "ada-asset", LocalPath: localPath}},
		}}},
	}
	media, canonicalByStable := entityCardMediaIndex(result)
	stableID := capabilityentities.StableEntityID("PERSON", "Ada Lovelace")
	item := attachEntityCardAsset(capabilityoverlay.OverlayItem{
		ID: "ada-card", EntityID: stableID, Kind: string(capabilityoverlay.KindEntityCard),
		MotionID:     "phrase_apple_clean_01_blur_soft_reveal",
		MotionParams: map[string]any{"stagger": 3},
	}, media, canonicalByStable, "plan-1")
	if item.Kind != string(capabilityoverlay.KindEntityImage) || len(item.AssetRefs) != 1 {
		t.Fatalf("entity image = %#v, want resolved image card", item)
	}
	if item.MotionID != "" || item.MotionParams != nil {
		t.Fatalf("entity image motion = %q params=%v, want run-level selection without stale text params", item.MotionID, item.MotionParams)
	}
	if item.AssetRefs[0].LocalPath != localPath {
		t.Fatalf("local path = %q, want verified producer path", item.AssetRefs[0].LocalPath)
	}
	wire, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), localPath) {
		t.Fatal("producer-local filesystem path leaked into the semantic overlay plan")
	}
}

func TestAttachEntityCardAssetResolvesLocalizedStableIdentity(t *testing.T) {
	image := &scriptpkg.EntityImageBinding{
		Status: "resolved", AssetID: "asset-mike-tyson", SHA256: strings.Repeat("a", 64),
		PreviewURL: "https://drive.google.com/uc?export=download&id=entity-image-person-mike-tyson",
		MediaType:  "image/jpeg",
	}
	result := &GenerateResult{Scenes: []Scene{{
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			Type: "PERSON", CanonicalName: "Mike Tyson", CanonicalEntityID: "person:mike-tyson", Image: image,
		}}},
		LocalizedAnnotations: map[Language]*scriptpkg.SceneAnnotations{
			"pl": {PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				Type: "PERSON", CanonicalName: "Mike’a Tysona", CanonicalEntityID: "person:mike-tyson", Image: image,
			}}},
		},
	}}}

	media, canonicalByStable := entityCardMediaIndex(result)
	localizedStableID := capabilityentities.StableEntityID("PERSON", "Mike’a Tysona")
	item := attachEntityCardAsset(capabilityoverlay.OverlayItem{
		ID: "localized-mike-card", EntityID: localizedStableID, Kind: string(capabilityoverlay.KindEntityCard),
	}, media, canonicalByStable, "plan-1")
	if item.Kind != string(capabilityoverlay.KindEntityImage) || len(item.AssetRefs) != 1 {
		t.Fatalf("localized entity image = %#v, want resolved image card", item)
	}
	if item.EntityRef == nil || item.EntityRef.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("localized entity ref = %#v, want canonical source identity", item.EntityRef)
	}
}

func TestCapEntityImageOverlaysKeepsDistinctIdentitiesUpToRunCeiling(t *testing.T) {
	maxImages := capabilityoverlay.MaxEntityImageOverlaysPerRun
	items := make([]capabilityoverlay.OverlayItem, 0, maxImages+4)
	for i := 0; i < maxImages+3; i++ {
		entityID := fmt.Sprintf("entity-%d", i)
		items = append(items, capabilityoverlay.OverlayItem{
			ID:   "image-" + entityID,
			Kind: string(capabilityoverlay.KindEntityImage), EntityID: entityID,
		})
	}
	// A repeated occurrence must not consume a second slot.
	items = append(items, capabilityoverlay.OverlayItem{
		ID: "image-entity-0-repeat", Kind: string(capabilityoverlay.KindEntityImage), EntityID: "entity-0",
	})
	items = append(items, capabilityoverlay.OverlayItem{ID: "phrase", Kind: "text_phrase"})

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	if len(got) != maxImages+1 {
		t.Fatalf("items=%d, want %d image items plus phrase", len(got), maxImages)
	}
	seen := map[string]bool{}
	for _, item := range got {
		if item.Kind == string(capabilityoverlay.KindEntityImage) {
			seen[item.EntityID] = true
		}
	}
	if len(seen) != capabilityoverlay.MaxEntityImageOverlaysPerRun {
		t.Fatalf("distinct image identities=%d, want %d", len(seen), capabilityoverlay.MaxEntityImageOverlaysPerRun)
	}
}

func TestComposeNearbyEntityImagesCreatesOneStaggeredComposite(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "second", SceneID: "scene-1", EntityID: "stable-second", Kind: string(capabilityoverlay.KindEntityImage), TemplateID: "image_popup", PresetID: "image_focus_in", StartMs: 2500, EndMs: 7500,
			EntityRef: &capabilityoverlay.OverlayEntityRef{EntityID: "stable-second", Type: "PERSON", Name: "Second Person", CanonicalEntityID: "person:second"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-b", SHA256: "hash-b"}}},
		{ID: "phrase", SceneID: "scene-1", Kind: "text_phrase", StartMs: 4000, EndMs: 5000},
		{ID: "first", SceneID: "scene-1", EntityID: "stable-first", Kind: string(capabilityoverlay.KindEntityImage), TemplateID: "image_popup", PresetID: "image_scale_in", StartMs: 1000, EndMs: 6000,
			StartUS: 1_000_000, DurationUS: 5_000_000,
			EntityRef: &capabilityoverlay.OverlayEntityRef{EntityID: "stable-first", Type: "PERSON", Name: "First Person", CanonicalEntityID: "person:first"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-a", SHA256: "hash-a"}}},
	}

	got := composeNearbyEntityImages(items, 1920, 1080)
	if len(got) != 2 {
		t.Fatalf("composed items = %d, want one composite and the phrase", len(got))
	}
	var composite *capabilityoverlay.OverlayItem
	for i := range got {
		if len(got[i].ImageLayers) > 0 {
			composite = &got[i]
		}
	}
	if composite == nil {
		t.Fatalf("no composite item in %+v", got)
	}
	if composite.ID != "first+second" || composite.StartMs != 1000 || composite.EndMs != 7500 || composite.StartUS != 1_000_000 || composite.DurationUS != 6_500_000 {
		t.Fatalf("composite timing/identity = %+v", composite)
	}
	if len(composite.AssetRefs) != 2 || len(composite.ImageLayers) != 2 {
		t.Fatalf("composite assets/layers = %d/%d, want 2/2", len(composite.AssetRefs), len(composite.ImageLayers))
	}
	first, second := composite.ImageLayers[0], composite.ImageLayers[1]
	if first.AssetID != "asset-a" || first.StartMS != 0 || first.EndMS != 5000 || first.PresetID != "image_scale_in" {
		t.Fatalf("first portrait layer = %+v", first)
	}
	if second.AssetID != "asset-b" || second.StartMS != 1500 || second.EndMS != 6500 || second.PresetID != "image_focus_in" {
		t.Fatalf("second portrait layer = %+v", second)
	}
	if first.Params["position_x"] != float64(-1920)*0.24 || second.Params["position_x"] != float64(1920)*0.24 {
		t.Fatalf("portrait positions = %v / %v", first.Params["position_x"], second.Params["position_x"])
	}

	assignEntityImageMotions(got, 7, 1920, 1080)
	firstMotion, secondMotion := got[0].ImageLayers[0].MotionID, got[0].ImageLayers[1].MotionID
	if firstMotion == "" || secondMotion == "" || firstMotion == secondMotion {
		t.Fatalf("composite child motions must be independently assigned: %q / %q", firstMotion, secondMotion)
	}
	if !containsString(capabilityoverlay.CertifiedImageMotions(), firstMotion) || !containsString(capabilityoverlay.CertifiedImageMotions(), secondMotion) {
		t.Fatalf("composite motions are not certified: %q / %q", firstMotion, secondMotion)
	}
	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "composite-plan", VideoID: "composite-video",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Items: []capabilityoverlay.OverlayItem{*composite},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("composite overlay plan does not satisfy its wire contract: %v", err)
	}
	wire, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Items []struct {
			ImageLayers []capabilityoverlay.OverlayImageLayer `json:"image_layers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(wire, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Items) != 1 || len(document.Items[0].ImageLayers) != 2 || document.Items[0].ImageLayers[1].MotionID != secondMotion {
		t.Fatalf("composite semantic wire lost independently assigned image layers: %+v", document.Items)
	}
}

func TestComposeNearbyEntityImagesHonorsThreeSecondMentionGap(t *testing.T) {
	makeItem := func(id string, start int64) capabilityoverlay.OverlayItem {
		return capabilityoverlay.OverlayItem{ID: id, SceneID: "scene", Kind: string(capabilityoverlay.KindEntityImage),
			StartMs: start, EndMs: start + 5000, PresetID: "image_focus_in",
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: id, SHA256: id}}}
	}
	within := composeNearbyEntityImages([]capabilityoverlay.OverlayItem{makeItem("a", 0), makeItem("b", 3000)}, 1280, 720)
	if len(within) != 1 || len(within[0].ImageLayers) != 2 {
		t.Fatalf("3s gap should compose to one item, got %+v", within)
	}
	outside := composeNearbyEntityImages([]capabilityoverlay.OverlayItem{makeItem("a", 0), makeItem("b", 3001)}, 1280, 720)
	if len(outside) != 2 || len(outside[0].ImageLayers) != 0 || len(outside[1].ImageLayers) != 0 {
		t.Fatalf("gap over 3s must remain separate, got %+v", outside)
	}
}

func TestCapEntityImageOverlaysDeduplicatesSameCanonicalEntity(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "scene-1", EntityID: "occurrence-1", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:mike-tyson"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "portrait-sha"}}},
		{ID: "scene-2", EntityID: "occurrence-2", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:mike-tyson"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "portrait-sha"}}},
		{ID: "scene-3", EntityID: "occurrence-3", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:evander-holyfield"},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{SHA256: "other-sha"}}},
	}

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	if len(got) != 2 {
		t.Fatalf("items=%d, want one Tyson image plus one other entity", len(got))
	}
	if got[0].EntityRef == nil || got[0].EntityRef.CanonicalEntityID != "person:mike-tyson" {
		t.Fatalf("first retained image = %#v, want Mike Tyson", got[0])
	}
}
