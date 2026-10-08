package overlays

import (
	"fmt"
	"strings"
	"testing"
)

func TestCertifiedAppleSpatialEntityStylesCoverAll25Variants(t *testing.T) {
	if len(CertifiedAppleSpatialEntityStyles) != 25 {
		t.Fatalf("certified Apple Spatial style list = %d entries, want 25", len(CertifiedAppleSpatialEntityStyles))
	}
	groups := map[string]int{}
	for index, id := range CertifiedAppleSpatialEntityStyles {
		switch index / 5 {
		case 0:
			groups["entrance_below"]++
		case 1:
			groups["side"]++
		case 2:
			groups["typewriter"]++
		case 3:
			groups["badge"]++
		case 4:
			groups["camera"]++
		}
		if len(id) < 4 || id[0] < '0' || id[0] > '9' || id[1] < '0' || id[1] > '9' || id[2] != '_' {
			t.Fatalf("style id %q must carry its positional reference", id)
		}
	}
	if groups["badge"] != 5 || groups["camera"] != 5 || groups["typewriter"] != 5 || groups["side"] != 5 || groups["entrance_below"] != 5 {
		t.Fatalf("style group distribution = %v, want 5 per composition class", groups)
	}
}

func TestValidateEntityStyleSelector(t *testing.T) {
	for _, selector := range CertifiedEntityStyleSelectors {
		if !validateEntityStyleSelector(selector) {
			t.Fatalf("selector %q must be certified", selector)
		}
	}
	for _, style := range CertifiedAppleSpatialEntityStyles {
		if !validateEntityStyleSelector(style) {
			t.Fatalf("variant %q must be transportable", style)
		}
	}
	if validateEntityStyleSelector("") || validateEntityStyleSelector("not_a_style") {
		t.Fatal("unknown or empty selectors must be refused")
	}
}

func TestStampEntityStyleOnlyStampsPortraitCardsWithCaption(t *testing.T) {
	items := []OverlayItem{
		// Satisfies the precondition: stamped.
		{ID: "portrait", Kind: "entity_image", EntityCaption: "Ada", AssetRefs: []OverlayAssetRef{{AssetID: "a"}}},
		// No caption: untouched (RenderingGen refuses the field).
		{ID: "silent", Kind: "entity_image", AssetRefs: []OverlayAssetRef{{AssetID: "a"}}},
		// No asset: untouched.
		{ID: "textcard", Kind: "entity_image", EntityCaption: "Ada"},
		// Phrase: untouched.
		{ID: "phrase", Kind: "text_phrase", Text: "hello"},
	}
	StampEntityStyle(items)
	if items[0].EntityStyleID != IdentityEntityStyleSelector {
		t.Fatalf("portrait style = %q, want %q", items[0].EntityStyleID, IdentityEntityStyleSelector)
	}
	for _, item := range items[1:] {
		if item.EntityStyleID != "" {
			t.Fatalf("item %q was stamped despite failing the style precondition", item.ID)
		}
	}
}

func TestImageWithTextMotionPoolFullCatalogByDefault(t *testing.T) {
	full := ImageWithTextMotionPool(0)
	if len(full) != len(generatedImageWithTextMotionCandidates) {
		t.Fatalf("full pool = %d motions, want the complete certified catalog (%d)", len(full), len(generatedImageWithTextMotionCandidates))
	}
	capped := ImageWithTextMotionPool(5)
	if len(capped) != 5 {
		t.Fatalf("capped pool = %d motions, want the channel-configured 5", len(capped))
	}
	// The rotation must reach the whole catalog across a long run.
	seen := map[string]struct{}{}
	for ordinal := 0; ordinal < len(generatedImageWithTextMotionCandidates); ordinal++ {
		seen[ImageWithTextMotionAtOffset(0, ordinal, 0)] = struct{}{}
	}
	if len(seen) != len(generatedImageWithTextMotionCandidates) {
		t.Fatalf("full rotation reached %d distinct motions, want %d", len(seen), len(generatedImageWithTextMotionCandidates))
	}
}

func TestEntityCaptionMotionPoolFullCatalogByDefault(t *testing.T) {
	full := EntityCaptionMotionPool(0)
	if len(full) != len(generatedEntityCaptionMotionCandidates) {
		t.Fatalf("full caption pool = %d motions, want the complete certified catalog (%d)", len(full), len(generatedEntityCaptionMotionCandidates))
	}
	if len(EntityCaptionMotionPool(5)) != 5 {
		t.Fatal("channel-configured caption cap must still be honored")
	}
}

func TestPlannerDefaultImageWithTextRotationUsesTheFullCatalog(t *testing.T) {
	// Three captioned entity images, no AnimationCounts configured: the run
	// must rotate the COMPLETE image-with-text catalog (the legacy cap was
	// five) and stamp the certified entity-style selector.
	scenes := make([]SceneInput, 0, 3)
	for i := 0; i < 3; i++ {
		scenes = append(scenes, SceneInput{ID: fmt.Sprintf("scene-%d", i), MultiEntityGroups: []MultiEntityGroup{{
			GroupID: fmt.Sprintf("group-%d", i), SceneID: fmt.Sprintf("scene-%d", i),
			GroupType: MultiEntityKindImage, Count: 1, StartMS: int64(1000 * (i + 1)), EndMS: int64(1000*(i+1) + 5000),
			Items: []MultiEntityCandidate{{
				ID: fmt.Sprintf("img-%d", i), SceneID: fmt.Sprintf("scene-%d", i), Kind: MultiEntityKindImage,
				Name: "Ada", StartMS: int64(1000 * (i + 1)), EndMS: int64(1000*(i+1) + 5000),
				AssetRef: &OverlayAssetRef{AssetID: fmt.Sprintf("asset-%d", i), SHA256: fmt.Sprintf("hash-%d", i)},
			}},
		}}})
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "p1", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Scenes: scenes,
	}, AllCandidatesPlannerConfig(scenes))
	if err != nil {
		t.Fatal(err)
	}
	motions := map[string]struct{}{}
	styled := 0
	for _, item := range plan.Items {
		if item.Kind != string(KindEntityImage) {
			continue
		}
		if item.MotionID != "" {
			motions[item.MotionID] = struct{}{}
		}
		if item.EntityStyleID == IdentityEntityStyleSelector {
			styled++
		}
	}
	if styled != 3 {
		t.Fatalf("styled entity items = %d, want 3", styled)
	}
	if len(motions) < 2 {
		t.Fatalf("three captioned images rotated only %d distinct motions; the full-catalog default must exceed the legacy five-style cap window for consecutive ordinals: %v", len(motions), motions)
	}
}

// threeCaptionedScenes is the shared fixture for selector-pinning tests.
func threeCaptionedScenes() []SceneInput {
	scenes := make([]SceneInput, 0, 3)
	for i := 0; i < 3; i++ {
		scenes = append(scenes, SceneInput{ID: fmt.Sprintf("scene-%d", i), MultiEntityGroups: []MultiEntityGroup{{
			GroupID: fmt.Sprintf("group-%d", i), SceneID: fmt.Sprintf("scene-%d", i),
			GroupType: MultiEntityKindImage, Count: 1, StartMS: int64(1000 * (i + 1)), EndMS: int64(1000*(i+1) + 5000),
			Items: []MultiEntityCandidate{{
				ID: fmt.Sprintf("img-%d", i), SceneID: fmt.Sprintf("scene-%d", i), Kind: MultiEntityKindImage,
				Name: "Ada", StartMS: int64(1000 * (i + 1)), EndMS: int64(1000*(i+1) + 5000),
				AssetRef: &OverlayAssetRef{AssetID: fmt.Sprintf("asset-%d", i), SHA256: fmt.Sprintf("hash-%d", i)},
			}},
		}}})
	}
	return scenes
}

func TestPlanInputEntityStylePinsTagSelectors(t *testing.T) {
	for _, selector := range []string{"badge", "camera", "side", "typewriter", "testo_sotto", "01_entity_pitch_lift_text_below"} {
		t.Run(selector, func(t *testing.T) {
			plan, err := BuildPlan(PlanInput{
				PlanID: "pin-" + selector, VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
				Scenes: threeCaptionedScenes(), EntityStyleID: selector,
			}, AllCandidatesPlannerConfig(threeCaptionedScenes()))
			if err != nil {
				t.Fatalf("BuildPlan with entity_style_id %q: %v", selector, err)
			}
			styled := 0
			for _, item := range plan.Items {
				if item.Kind == string(KindEntityImage) && item.EntityStyleID != "" {
					styled++
					if item.EntityStyleID != selector {
						t.Fatalf("entity_style_id = %q, want the pinned selector %q", item.EntityStyleID, selector)
					}
				}
			}
			if styled == 0 {
				t.Fatal("no entity item carried the pinned selector")
			}
		})
	}
}

func TestPlanInputInvalidEntityStyleFailsClosed(t *testing.T) {
	_, err := BuildPlan(PlanInput{
		PlanID: "pin-bad", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Scenes: threeCaptionedScenes(), EntityStyleID: "neon_explosion",
	}, AllCandidatesPlannerConfig(threeCaptionedScenes()))
	if err == nil {
		t.Fatal("an unknown entity_style_id selector must fail closed")
	}
	if !strings.Contains(err.Error(), "neon_explosion") {
		t.Fatalf("error must name the offending selector: %v", err)
	}
}

func TestPlanInputDefaultEntityStyleIsRandom(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "pin-default", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
		Scenes: threeCaptionedScenes(),
	}, AllCandidatesPlannerConfig(threeCaptionedScenes()))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.Kind == string(KindEntityImage) && item.EntityStyleID != "" && item.EntityStyleID != IdentityEntityStyleSelector {
			t.Fatalf("default selector = %q, want %q", item.EntityStyleID, IdentityEntityStyleSelector)
		}
	}
}
