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
		EntityRef:    &capabilityoverlay.OverlayEntityRef{EntityID: stableID, Type: "PERSON", Name: "Ada Lovelace"},
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
	if item.EntityCaption != "Ada Lovelace" || item.Text != "" {
		t.Fatalf("single-image caption/text = %q/%q, want visible entity caption and no generic image text", item.EntityCaption, item.Text)
	}
	wire, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), localPath) {
		t.Fatal("producer-local filesystem path leaked into the semantic overlay plan")
	}
	if !strings.Contains(string(wire), `"entity_caption":"Ada Lovelace"`) {
		t.Fatalf("single-image caption missing from semantic plan wire: %s", wire)
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

func TestCapEntityImageOverlaysPerSceneKeepsRepeatedEntityInEachScene(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "scene-1-milton", SceneID: "scene-1", EntityID: "milton", Kind: string(capabilityoverlay.KindEntityImage), EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:milton-leite"}},
		{ID: "scene-2-milton", SceneID: "scene-2", EntityID: "milton", Kind: string(capabilityoverlay.KindEntityImage), EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:milton-leite"}},
		{ID: "scene-2-other", SceneID: "scene-2", EntityID: "other", Kind: string(capabilityoverlay.KindEntityImage), EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:other"}},
		{ID: "phrase", SceneID: "scene-2", Kind: "text_phrase"},
	}
	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun, true)
	if len(got) != 3 || got[0].ID != "scene-1-milton" || got[1].ID != "scene-2-milton" || got[2].Kind != "text_phrase" {
		t.Fatalf("per-scene image cap = %+v; want one image per scene and preserve phrase", got)
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
	if composite.ImageLayers[0].Caption != "First Person" || composite.ImageLayers[1].Caption != "Second Person" {
		t.Fatalf("composite captions = %q / %q, want both entity names", composite.ImageLayers[0].Caption, composite.ImageLayers[1].Caption)
	}
	first, second := composite.ImageLayers[0], composite.ImageLayers[1]
	if first.AssetID != "asset-a" || first.StartMS != 0 || first.EndMS != 6500 || first.PresetID != "image_scale_in" {
		t.Fatalf("first portrait layer = %+v", first)
	}
	if second.AssetID != "asset-b" || second.StartMS != 1500 || second.EndMS != 6500 || second.PresetID != "image_focus_in" {
		t.Fatalf("second portrait layer = %+v", second)
	}
	if first.EndMS != 6500 {
		t.Fatalf("first portrait should stay visible to the group end, got %d ms", first.EndMS)
	}
	if first.Params["position_x"] != float64(-1920)*0.18 || second.Params["position_x"] != float64(1920)*0.18 {
		t.Fatalf("portrait positions = %v / %v", first.Params["position_x"], second.Params["position_x"])
	}
	if first.Params["width"] != 1920*34/100 || second.Params["width"] != 1920*34/100 {
		t.Fatalf("portrait widths = %v / %v, want larger 34%% cards", first.Params["width"], second.Params["width"])
	}
	if first.EntityID != "stable-first" || second.EntityID != "stable-second" {
		t.Fatalf("composite child entity ids = %q / %q", first.EntityID, second.EntityID)
	}

	assignEntityImageMotions(got, 7, 1920, 1080, "")
	firstMotion, secondMotion := got[0].ImageLayers[0].MotionID, got[0].ImageLayers[1].MotionID
	if firstMotion == "" || secondMotion == "" || firstMotion == secondMotion {
		t.Fatalf("composite child motions must be independently assigned: %q / %q", firstMotion, secondMotion)
	}
	captionPool := capabilityoverlay.CertifiedEntityCaptionMotions()
	firstCaptionMotion, secondCaptionMotion := got[0].ImageLayers[0].CaptionMotionID, got[0].ImageLayers[1].CaptionMotionID
	if !containsString(captionPool, firstCaptionMotion) || !containsString(captionPool, secondCaptionMotion) || firstCaptionMotion == secondCaptionMotion {
		t.Fatalf("composite captions need distinct certified motions: %q / %q", firstCaptionMotion, secondCaptionMotion)
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
	if document.Items[0].ImageLayers[0].CaptionMotionID != firstCaptionMotion || document.Items[0].ImageLayers[1].CaptionMotionID != secondCaptionMotion {
		t.Fatalf("composite semantic wire lost caption motions: %+v", document.Items[0].ImageLayers)
	}
}

func TestComposeNearbyEntityImagesHonorsFiveSecondMentionGap(t *testing.T) {
	makeItem := func(id, scene string, start int64) capabilityoverlay.OverlayItem {
		return capabilityoverlay.OverlayItem{ID: id, SceneID: scene, EntityID: "ent-" + id, Kind: string(capabilityoverlay.KindEntityImage),
			StartMs: start, EndMs: start + 5000, PresetID: "image_focus_in",
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: id, SHA256: id}}}
	}
	within := composeNearbyEntityImages([]capabilityoverlay.OverlayItem{makeItem("a", "scene", 0), makeItem("b", "scene", 5000)}, 1280, 720)
	if len(within) != 1 || len(within[0].ImageLayers) != 2 {
		t.Fatalf("5s gap should compose to one item, got %+v", within)
	}
	outside := composeNearbyEntityImages([]capabilityoverlay.OverlayItem{makeItem("a", "scene", 0), makeItem("b", "scene", 5001)}, 1280, 720)
	if len(outside) != 2 || len(outside[0].ImageLayers) != 0 || len(outside[1].ImageLayers) != 0 {
		t.Fatalf("gap over 5s must remain separate, got %+v", outside)
	}
	crossScene := composeNearbyEntityImages([]capabilityoverlay.OverlayItem{makeItem("a", "scene-1", 0), makeItem("b", "scene-2", 1000)}, 1280, 720)
	if len(crossScene) != 2 || len(crossScene[0].ImageLayers) != 0 || len(crossScene[1].ImageLayers) != 0 {
		t.Fatalf("entities from different scenes must remain separate, got %+v", crossScene)
	}
}

func TestComposeNearbyEntityImagesGroupsTwoThroughFiveWithDeterministicRemainders(t *testing.T) {
	for _, count := range []int{2, 3, 4, 5, 6, 10, 11} {
		t.Run(fmt.Sprintf("images-%d", count), func(t *testing.T) {
			items := make([]capabilityoverlay.OverlayItem, count)
			for index := range items {
				id := fmt.Sprintf("image-%02d", index)
				items[index] = capabilityoverlay.OverlayItem{
					ID: id, SceneID: "scene", EntityID: "entity-" + id,
					Kind: string(capabilityoverlay.KindEntityImage), PresetID: "image_focus_in",
					StartMs: int64(index * 100), EndMs: int64(index*100 + 5000),
					EntityRef: &capabilityoverlay.OverlayEntityRef{EntityID: "entity-" + id, Type: "PERSON", Name: "Person " + id},
					AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-" + id, SHA256: "hash-" + id}},
				}
			}
			got := composeNearbyEntityImages(items, 1920, 1080)
			wantGroups := (count + maxEntityImageGroup - 1) / maxEntityImageGroup
			if len(got) != wantGroups {
				t.Fatalf("render groups = %d, want %d: %+v", len(got), wantGroups, got)
			}
			layersSeen := 0
			for _, item := range got {
				if len(item.ImageLayers) < 2 || len(item.ImageLayers) > maxEntityImageGroup {
					t.Fatalf("group %q has %d layers; expected 2..5", item.ID, len(item.ImageLayers))
				}
				layersSeen += len(item.ImageLayers)
				for index, layer := range item.ImageLayers {
					if layer.EntityID != items[layersSeen-len(item.ImageLayers)+index].EntityID {
						t.Fatalf("group child %d lost entity identity: %+v", index, layer)
					}
					if layer.Caption == "" || layer.AssetID == "" || layer.PresetID == "" {
						t.Fatalf("group child lost caption, asset or preset: %+v", layer)
					}
				}
			}
			if layersSeen != count {
				t.Fatalf("composed %d images, want all %d", layersSeen, count)
			}
		})
	}
}

func TestComposeNearbyEntityImagesPreservesMoreThanFiveInSameScene(t *testing.T) {
	items := make([]capabilityoverlay.OverlayItem, 7)
	for index := range items {
		id := fmt.Sprintf("entity-%d", index)
		items[index] = capabilityoverlay.OverlayItem{
			ID: id, SceneID: "same-scene", EntityID: id, Kind: string(capabilityoverlay.KindEntityImage),
			StartMs: int64(index * 400), EndMs: int64(index*400 + 5000),
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: id, SHA256: id}},
		}
	}
	got := composeNearbyEntityImages(items, 1920, 1080)
	if len(got) != 2 || len(got[0].ImageLayers) != 5 || len(got[1].ImageLayers) != 2 {
		t.Fatalf("seven nearby images must partition into 5+2, got %+v", got)
	}
}

func TestCompositeChildIdentityRemainsProducerOnlyAndNotRenderKeyInput(t *testing.T) {
	plan := capabilityoverlay.OverlayPlan{
		SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "identity-wire", VideoID: "video",
		Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1,
		Items: []capabilityoverlay.OverlayItem{{
			ID: "pair", Kind: string(capabilityoverlay.KindEntityImage), TemplateID: "image_popup",
			StartMs: 0, EndMs: 5000, AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "a"}, {AssetID: "b"}},
			ImageLayers: []capabilityoverlay.OverlayImageLayer{
				{ID: "a", EntityID: "stable-a", AssetID: "a", StartMS: 0, EndMS: 5000},
				{ID: "b", EntityID: "stable-b", AssetID: "b", StartMS: 0, EndMS: 5000},
			},
		}},
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	key := plan.Items[0].RenderKey
	plan.Items[0].ImageLayers[0].EntityID = "different-producer-identity"
	if got := capabilityoverlay.ComputeRenderKey(plan, plan.Items[0]); got != key {
		t.Fatalf("producer-only child identity changed render key %q to %q", key, got)
	}
	wire, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "stable-a") || strings.Contains(string(wire), "different-producer-identity") || strings.Contains(string(wire), "entity_id") {
		t.Fatalf("producer-only child identity leaked into worker wire: %s", wire)
	}
}

func TestComposeNearbyEntityImagesKeepsNonEntityImagesUntouched(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "a", SceneID: "scene", Kind: string(capabilityoverlay.KindEntityImage), StartMs: 0, EndMs: 5000, AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "a"}}},
		{ID: "b", SceneID: "scene", Kind: string(capabilityoverlay.KindEntityImage), StartMs: 1000, EndMs: 6000, AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "b"}}},
		{ID: "photo", SceneID: "scene", Kind: "image", StartMs: 500, EndMs: 2000, AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "photo"}}},
	}
	got := composeNearbyEntityImages(items, 1920, 1080)
	if len(got) != 2 {
		t.Fatalf("expected entity composite and standalone photo, got %+v", got)
	}
	for _, item := range got {
		if item.ID == "photo" && len(item.ImageLayers) != 0 {
			t.Fatalf("unrelated scene image was grouped: %+v", item)
		}
	}
}

func TestEditorialImageBudgetRunsBeforeCompositionAndKeepsUniqueChildren(t *testing.T) {
	items := make([]capabilityoverlay.OverlayItem, 0, capabilityoverlay.MaxEntityImageOverlaysPerRun+4)
	for index := 0; index < capabilityoverlay.MaxEntityImageOverlaysPerRun; index++ {
		id := fmt.Sprintf("entity-%02d", index)
		items = append(items, capabilityoverlay.OverlayItem{
			ID: id, SceneID: "scene", EntityID: id,
			Kind: string(capabilityoverlay.KindEntityImage), StartMs: int64(index * 100), EndMs: int64(index*100 + 5000),
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-" + id, SHA256: id}},
		})
	}
	// A context hit for one child must be deduped before composition, not cause
	// the whole composite to be discarded after its asset list is merged.
	items = append(items, capabilityoverlay.OverlayItem{
		ID: "duplicate-context", SceneID: "other", Kind: "image",
		AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-entity-00", SHA256: "entity-00"}},
	})
	capped := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun)
	budgeted, _ := capabilityoverlay.ApplyEditorialOverlayBudgetWithImageLimit(capped, 0, capabilityoverlay.MaxImageOverlaysPerRun, capabilityoverlay.MaxMapOverlaysPerRun)
	grouped := composeNearbyEntityImages(budgeted, 1920, 1080)
	children := 0
	duplicateContextSurvived := false
	for _, item := range grouped {
		if len(item.ImageLayers) > 0 {
			children += len(item.ImageLayers)
		} else if item.Kind == string(capabilityoverlay.KindEntityImage) {
			children++
		} else if item.ID == "duplicate-context" {
			duplicateContextSurvived = true
		}
	}
	if children != capabilityoverlay.MaxEntityImageOverlaysPerRun || duplicateContextSurvived {
		t.Fatalf("budget/composition children=%d duplicate-context=%v; want %d unique entity images and no duplicate context item: %+v", children, duplicateContextSurvived, capabilityoverlay.MaxEntityImageOverlaysPerRun, grouped)
	}
}

func TestNamedEntityCardCeilingDoesNotLimitImageCompositeChildren(t *testing.T) {
	items := make([]capabilityoverlay.OverlayItem, 0, 8)
	for index := 0; index < 5; index++ {
		id := fmt.Sprintf("image-%d", index)
		items = append(items, capabilityoverlay.OverlayItem{
			ID: id, SceneID: "scene", EntityID: id, Kind: string(capabilityoverlay.KindEntityImage),
			StartMs: int64(index * 100), EndMs: int64(index*100 + 5000),
			EntityRef: &capabilityoverlay.OverlayEntityRef{EntityID: id, Type: "PERSON", Name: id},
			AssetRefs: []capabilityoverlay.OverlayAssetRef{{AssetID: "asset-" + id, SHA256: "sha-" + id}},
		})
	}
	for index := 0; index < 3; index++ {
		items = append(items, capabilityoverlay.OverlayItem{
			ID: fmt.Sprintf("card-%d", index), SceneID: "scene", EntityID: fmt.Sprintf("card-ent-%d", index),
			Kind: string(capabilityoverlay.KindEntityCard), Text: fmt.Sprintf("Named entity %d", index),
		})
	}
	items = capNamedEntityCardsPerScene(items, maxNamedEntityCardsPerScene)
	cards := 0
	images := 0
	for _, item := range items {
		if item.Kind == string(capabilityoverlay.KindEntityCard) {
			cards++
		} else if item.Kind == string(capabilityoverlay.KindEntityImage) {
			images++
		}
	}
	if cards != maxNamedEntityCardsPerScene || images != 5 {
		t.Fatalf("per-scene cap retained %d named cards and %d image entities; want 2 and 5", cards, images)
	}
	grouped := composeNearbyEntityImages(items, 1920, 1080)
	groupSize := 0
	for _, item := range grouped {
		groupSize += len(item.ImageLayers)
	}
	if groupSize != 5 {
		t.Fatalf("named entity cap reduced composite image children to %d, want 5", groupSize)
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

func TestCapEntityImageOverlaysKeepsRepeatedIdentityInPerSceneScope(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "scene-1", SceneID: "scene-1", EntityID: "occurrence-1", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:isabelle-caracristi"}},
		{ID: "scene-2", SceneID: "scene-2", EntityID: "occurrence-2", Kind: string(capabilityoverlay.KindEntityImage),
			EntityRef: &capabilityoverlay.OverlayEntityRef{CanonicalEntityID: "person:isabelle-caracristi"}},
	}

	got := capEntityImageOverlays(items, capabilityoverlay.MaxEntityImageOverlaysPerRun, true)
	if len(got) != 2 {
		t.Fatalf("per-scene image count = %d, want repeated identity retained once in each scene", len(got))
	}
	if got[0].SceneID != "scene-1" || got[1].SceneID != "scene-2" {
		t.Fatalf("retained scenes = %q, %q; want scene-1 and scene-2", got[0].SceneID, got[1].SceneID)
	}
}
