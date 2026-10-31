package scriptgeneration

import (
	"strings"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// The r10 incident class: the plan promised entity-image overlays whose
// assets carried empty SHA256/locator fields, every card failed open to
// text-only, and the silent-empty visual was detected only after a full
// remote render. The image preflight must reject that class before any
// remote attempt, without introducing false positives for the documented
// fail-open (partial miss) and clip-window (dropped by design) behaviors.

func imagePreflightItem(id, kind, sha, url string) capoverlay.OverlayItem {
	item := capoverlay.OverlayItem{ID: id, Kind: kind, SceneID: "scene-1"}
	if sha != "" || url != "" {
		item.AssetRefs = []capoverlay.OverlayAssetRef{{AssetID: "asset-" + id, SHA256: sha, URL: url}}
	}
	return item
}

func TestFinalJobImagePreflight_UnverifiedPromisedImagesFailClosed(t *testing.T) {
	items := []capoverlay.OverlayItem{
		imagePreflightItem("img-1", "entity_image", "", ""),
		imagePreflightItem("img-2", "entity_image", "", ""),
	}
	err := finalJobImagePreflight(items, nil)
	if err == nil {
		t.Fatal("expected preflight failure for promised entity images without verified assets")
	}
	if !strings.Contains(err.Error(), "preflight") || !strings.Contains(err.Error(), "img-1") {
		t.Fatalf("error must name the gate and the first unverified overlay, got: %v", err)
	}
	// Pinned message shape: operators grep this from retry logs.
	if !strings.Contains(err.Error(), "predicted silent visual loss") {
		t.Fatalf("error must classify the predicted silent visual loss, got: %v", err)
	}
}

func TestFinalJobImagePreflight_VerifiedAssetsAdmitted(t *testing.T) {
	items := []capoverlay.OverlayItem{
		imagePreflightItem("img-1", "entity_image",
			"1111111111111111111111111111111111111111111111111111111111111111",
			"https://drive.example.com/download/asset"),
		imagePreflightItem("img-2", "entity_image",
			"2222222222222222222222222222222222222222222222222222222222222222",
			"https://upload.wikimedia.org/stasi.jpg"),
	}
	if err := finalJobImagePreflight(items, nil); err != nil {
		t.Fatalf("verified image assets must be admitted: %v", err)
	}
}

func TestFinalJobImagePreflight_NoImagesPlannedPasses(t *testing.T) {
	items := []capoverlay.OverlayItem{
		imagePreflightItem("date-1", "number", "", ""),
		imagePreflightItem("phrase-1", "text_phrase", "", ""),
	}
	if err := finalJobImagePreflight(items, nil); err != nil {
		t.Fatalf("a text-only plan must pass the image preflight: %v", err)
	}
}

func TestFinalJobImagePreflight_PartialUnverifiedStaysFailOpen(t *testing.T) {
	// One of two promised cards is verified: the documented fail-open policy
	// (one invalid binding keeps its card text-only) must keep the run going.
	items := []capoverlay.OverlayItem{
		imagePreflightItem("img-1", "entity_image",
			"1111111111111111111111111111111111111111111111111111111111111111",
			"https://drive.example.com/download/asset"),
		imagePreflightItem("img-2", "entity_image", "", ""),
	}
	if err := finalJobImagePreflight(items, nil); err != nil {
		t.Fatalf("partial image miss must stay fail-open, got: %v", err)
	}
}

func TestFinalJobImagePreflight_VerifiedLocalPathAdmitted(t *testing.T) {
	// The r38 cutover renders from the durable catalog: a LocalPath-backed
	// binding without a public URL is a full identity when the file exists.
	item := imagePreflightItem("img-1", "entity_image",
		"3333333333333333333333333333333333333333333333333333333333333333", "")
	item.AssetRefs[0].LocalPath = "final_job_image_preflight_test.go"
	if err := finalJobImagePreflight([]capoverlay.OverlayItem{item}, nil); err != nil {
		t.Fatalf("a usable local path is a verified locator: %v", err)
	}
	nonFile := item
	nonFile.AssetRefs[0].LocalPath = "/definitely/not/a/real/path/for/preflight-test.png"
	if err := finalJobImagePreflight([]capoverlay.OverlayItem{nonFile}, nil); err == nil {
		t.Fatal("a dangling local path is not a verified locator")
	}
}

func TestFinalJobImagePreflight_CompositeRequiresEveryLayerVerified(t *testing.T) {
	base := capoverlay.OverlayItem{
		ID: "group-1", Kind: "entity_image", SceneID: "scene-1",
		ImageLayers: []capoverlay.OverlayImageLayer{{ID: "l-1", AssetID: "a-1"}, {ID: "l-2", AssetID: "a-2"}},
	}
	verifiedRef := func(sha string) capoverlay.OverlayAssetRef {
		return capoverlay.OverlayAssetRef{AssetID: "a", SHA256: sha, URL: "https://drive.example.com/download/asset"}
	}
	full := base
	full.AssetRefs = []capoverlay.OverlayAssetRef{
		verifiedRef("4444444444444444444444444444444444444444444444444444444444444444"),
		verifiedRef("5555555555555555555555555555555555555555555555555555555555555555"),
	}
	if err := finalJobImagePreflight([]capoverlay.OverlayItem{full}, nil); err != nil {
		t.Fatalf("a fully verified composite group must be admitted: %v", err)
	}
	half := base
	half.AssetRefs = []capoverlay.OverlayAssetRef{verifiedRef("4444444444444444444444444444444444444444444444444444444444444444")}
	if err := finalJobImagePreflight([]capoverlay.OverlayItem{half}, nil); err == nil {
		t.Fatal("a composite group missing a verified layer must be unverified")
	}
}

func TestFinalJobImagePreflight_ClipSceneCardsExcluded(t *testing.T) {
	// Image-style cards on intermediate clip scenes are dropped by the
	// final-job handoff by design; promising one there is not a broken run.
	items := []capoverlay.OverlayItem{imagePreflightItem("img-1", "entity_image", "", "")}
	items[0].SceneID = "scene-clip"
	if err := finalJobImagePreflight(items, map[string]struct{}{"scene-clip": {}}); err != nil {
		t.Fatalf("clip-scene image cards are dropped by design, got: %v", err)
	}
}

func TestFinalJobImagePreflight_NonHexHashIsNotVerified(t *testing.T) {
	items := []capoverlay.OverlayItem{
		imagePreflightItem("img-1", "entity_image",
			strings.Repeat("g", 64),
			"https://drive.example.com/download/asset"),
	}
	if err := finalJobImagePreflight(items, nil); err == nil {
		t.Fatal("a non-hex content address is not a verified asset")
	}
}
