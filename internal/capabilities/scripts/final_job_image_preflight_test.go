package scriptgeneration

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The r10 incident class: the plan promised entity-image overlays whose
// assets carried empty SHA256/locator fields, every card failed open to
// text-only, and the silent-empty visual was detected only after a full
// remote render. The image preflight must reject that class before any
// remote attempt, without introducing false positives for the documented
// fail-open (partial miss) and clip-window (dropped by design) behaviors.

func imagePreflightItem(id, kind, sha, url string) capmodel.OverlayItem {
	item := capmodel.OverlayItem{ID: id, Kind: kind, SceneID: "scene-1"}
	if sha != "" || url != "" {
		item.AssetRefs = []capmodel.OverlayAssetRef{{AssetID: "asset-" + id, SHA256: sha, URL: url}}
	}
	return item
}

// preflightPNGBytes returns a real, decodable image and its content address.
// The preflight verifies the bytes it will actually render, so fixture hashes
// must describe fixture bytes rather than arbitrary 64-hex strings.
func preflightPNGBytes(t *testing.T) ([]byte, string) {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode preflight PNG: %v", err)
	}
	data := encoded.Bytes()
	return data, fmt.Sprintf("%x", sha256.Sum256(data))
}

func preflightPNGFile(t *testing.T) (string, string) {
	t.Helper()
	data, sha := preflightPNGBytes(t)
	path := filepath.Join(t.TempDir(), "preflight.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write preflight PNG: %v", err)
	}
	return path, sha
}

func TestFinalJobImagePreflight_UnverifiedPromisedImagesFailClosed(t *testing.T) {
	items := []capmodel.OverlayItem{
		imagePreflightItem("img-1", "entity_image", "", ""),
		imagePreflightItem("img-2", "entity_image", "", ""),
	}
	err := finalJobImagePreflight(items, nil, false)
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
	localPath, localSHA := preflightPNGFile(t)
	remoteBytes, remoteSHA := preflightPNGBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(remoteBytes)
	}))
	defer server.Close()
	items := []capmodel.OverlayItem{
		imagePreflightItem("img-1", "entity_image", localSHA, ""),
		imagePreflightItem("img-2", "entity_image", remoteSHA, server.URL+"/image.png"),
	}
	items[0].AssetRefs[0].LocalPath = localPath
	if err := finalJobImagePreflight(items, nil, false); err != nil {
		t.Fatalf("verified image assets must be admitted: %v", err)
	}
}

func TestFinalJobImagePreflight_NoImagesPlannedPasses(t *testing.T) {
	items := []capmodel.OverlayItem{
		imagePreflightItem("date-1", "number", "", ""),
		imagePreflightItem("phrase-1", "text_phrase", "", ""),
	}
	if err := finalJobImagePreflight(items, nil, false); err != nil {
		t.Fatalf("a text-only plan must pass the image preflight: %v", err)
	}
}

func TestFinalJobImagePreflight_PartialUnverifiedFailsClosed(t *testing.T) {
	// One of two promised cards is verified. A partial miss is still a silent
	// visual loss: the delivered video would carry one text-only card while
	// claiming the image contract was satisfied.
	localPath, localSHA := preflightPNGFile(t)
	items := []capmodel.OverlayItem{
		imagePreflightItem("img-1", "entity_image", localSHA, ""),
		imagePreflightItem("img-2", "entity_image", "", ""),
	}
	items[0].AssetRefs[0].LocalPath = localPath
	if err := finalJobImagePreflight(items, nil, false); err == nil {
		t.Fatal("partial image miss must fail closed")
	}
}

func TestFinalJobImagePreflight_VerifiedLocalPathAdmitted(t *testing.T) {
	// The r38 cutover renders from the durable catalog: a LocalPath-backed
	// binding without a public URL is a full identity when the file exists.
	path, sha := preflightPNGFile(t)
	item := imagePreflightItem("img-1", "entity_image", sha, "")
	item.AssetRefs[0].LocalPath = path
	if err := finalJobImagePreflight([]capmodel.OverlayItem{item}, nil, false); err != nil {
		t.Fatalf("a usable local path is a verified locator: %v", err)
	}
	nonFile := item
	nonFile.AssetRefs = append([]capmodel.OverlayAssetRef(nil), item.AssetRefs...)
	nonFile.AssetRefs[0].LocalPath = "/definitely/not/a/real/path/for/preflight-test.png"
	if err := finalJobImagePreflight([]capmodel.OverlayItem{nonFile}, nil, false); err == nil {
		t.Fatal("a dangling local path is not a verified locator")
	}
}

func TestFinalJobImagePreflight_CompositeRequiresEveryLayerVerified(t *testing.T) {
	base := capmodel.OverlayItem{
		ID: "group-1", Kind: "entity_image", SceneID: "scene-1",
		ImageLayers: []capmodel.OverlayImageLayer{{ID: "l-1", AssetID: "a-1"}, {ID: "l-2", AssetID: "a-2"}},
	}
	path1, sha1 := preflightPNGFile(t)
	path2, sha2 := preflightPNGFile(t)
	verifiedRef := func(sha, path string) capmodel.OverlayAssetRef {
		return capmodel.OverlayAssetRef{AssetID: "a", SHA256: sha, LocalPath: path}
	}
	full := base
	full.AssetRefs = []capmodel.OverlayAssetRef{
		verifiedRef(sha1, path1),
		verifiedRef(sha2, path2),
	}
	if err := finalJobImagePreflight([]capmodel.OverlayItem{full}, nil, false); err != nil {
		t.Fatalf("a fully verified composite group must be admitted: %v", err)
	}
	half := base
	half.AssetRefs = []capmodel.OverlayAssetRef{verifiedRef(sha1, path1)}
	if err := finalJobImagePreflight([]capmodel.OverlayItem{half}, nil, false); err == nil {
		t.Fatal("a composite group missing a verified layer must be unverified")
	}
}

func TestFinalJobImagePreflight_ClipSceneCardsExcluded(t *testing.T) {
	// Image-style cards on intermediate clip scenes are dropped by the
	// final-job handoff by design; promising one there is not a broken run.
	items := []capmodel.OverlayItem{imagePreflightItem("img-1", "entity_image", "", "")}
	items[0].SceneID = "scene-clip"
	if err := finalJobImagePreflight(items, map[string]struct{}{"scene-clip": {}}, false); err != nil {
		t.Fatalf("clip-scene image cards are dropped by design, got: %v", err)
	}
}

// TestFinalJobImageRequested_R20MinutiPayloadShape pins the 2026-11-03
// regression: the Garlasco payload enabled images through the extraction
// surface (include: [persons, ...]) and max_image_overlays, and left the
// legacy entity_images toggle absent. Reading only the toggle returned false,
// so the preflight was skipped and the run shipped a silent image-free render
// after a full remote attempt.
func TestFinalJobEntityImageGateRejectsPartialCoverage(t *testing.T) {
	audit := &OverlayAudit{EntityImages: &OverlayAuditEntityImages{
		Selected: 3,
		Bound:    3,
		Reasons:  map[string]int{EntityImageReasonBound: 3},
	}}
	if err := finalJobEntityImageGate(audit, true); err != nil {
		t.Fatalf("fully bound audit must pass: %v", err)
	}
	zeroAudit := &OverlayAudit{EntityImages: &OverlayAuditEntityImages{Selected: 0}}
	if err := finalJobEntityImageGate(zeroAudit, true); err == nil {
		t.Fatal("requested images with zero selected entity occurrences must fail closed (0/0 is not a pass)")
	}
	audit.EntityImages.Missing = 1
	audit.EntityImages.Bound = 2
	audit.EntityImages.Reasons = map[string]int{
		EntityImageReasonBound:           2,
		EntityImageReasonNoVerifiedAsset: 1,
	}
	if err := finalJobEntityImageGate(audit, true); err == nil {
		t.Fatal("one missing portrait out of three must fail the final-job gate")
	}
	if err := finalJobEntityImageGate(audit, false); err != nil {
		t.Fatalf("a run that did not request images must not be gated: %v", err)
	}
}

func TestFinalJobImageRequested_R20MinutiPayloadShape(t *testing.T) {
	req := GenerateRequest{MaxImageOverlays: 12}
	req.MediaPlan.ProviderPolicy.InternetImages = media.MediaToggleEnabled
	req.MediaPlan.Extraction.Include = []string{media.ExtractionIncludePersons}
	if !finalJobImageRequested(req) {
		t.Fatal("the person-extraction surface with an image budget is an image request")
	}
	// Zero budget is the certified default (20 images), not "off": the canvas
	// reads it that way, so a payload that enables the images category without
	// an explicit budget must still arm the preflight.
	zeroBudget := req
	zeroBudget.MaxImageOverlays = 0
	zeroBudget.OverlayCategories = map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryImages: scriptpkg.ToggleEnabled,
	}
	if !finalJobImageRequested(zeroBudget) {
		t.Fatal("zero image budget with the images category enabled is still an image request")
	}
	entitySurfaceOnly := req
	entitySurfaceOnly.MaxImageOverlays = 0
	entitySurfaceOnly.OverlayCategories = nil
	if !finalJobImageRequested(entitySurfaceOnly) {
		t.Fatal("an enabled entity-image extraction surface is an image request without a second overlay toggle")
	}
	zeroBudget.OverlayCategories[scriptpkg.OverlayCategoryImages] = scriptpkg.ToggleDisabled
	if finalJobImageRequested(zeroBudget) {
		t.Fatal("an explicit images=disabled category must not arm the preflight")
	}
	groupDisabled := req
	groupDisabled.OverlayGroupLimits = map[string]int{overlayGroupImages: 0}
	if finalJobImageRequested(groupDisabled) {
		t.Fatal("overlay_group_limits.images=0 disables the group and must not arm the preflight")
	}
	entityOnly := GenerateRequest{}
	entityOnly.MediaPlan.ProviderPolicy.InternetImages = media.MediaToggleEnabled
	entityOnly.MediaPlan.Extraction.EntityImages = media.EntityImagePolicy{Enabled: true, EntityTypes: []string{"PERSON"}}
	if !finalJobImageRequested(entityOnly) {
		t.Fatal("an explicit entity_images request must arm the preflight without a second overlay toggle")
	}
	// The same payload with the legacy toggle absent must still reach the
	// preflight with zero planned images: that is the fail-closed path.
	if err := finalJobImagePreflight([]capmodel.OverlayItem{{ID: "phrase-1", Kind: "text_phrase"}}, nil, finalJobImageRequested(req)); err == nil {
		t.Fatal("a requested image class with zero image overlays must fail closed")
	} else if !strings.Contains(err.Error(), "zero verified image overlays") {
		t.Fatalf("error must name the silent image-free class, got: %v", err)
	}

	cases := []struct {
		name string
		mute func(*GenerateRequest)
	}{
		{name: "images category disabled", mute: func(r *GenerateRequest) {
			r.OverlayCategories = map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
				scriptpkg.OverlayCategoryImages: scriptpkg.ToggleDisabled,
			}
		}},
		{name: "internet images disabled", mute: func(r *GenerateRequest) {
			r.MediaPlan.ProviderPolicy.InternetImages = media.MediaToggleDisabled
		}},
		{name: "no identity surface", mute: func(r *GenerateRequest) {
			r.MaxImageOverlays = 0
			r.MediaPlan.Extraction.Include = []string{media.ExtractionIncludeLocations}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			muted := req
			tc.mute(&muted)
			if finalJobImageRequested(muted) {
				t.Fatal("a payload without an image request must not arm the preflight")
			}
		})
	}
}

func TestFinalJobImagePreflight_NonHexHashIsNotVerified(t *testing.T) {
	items := []capmodel.OverlayItem{
		imagePreflightItem("img-1", "entity_image",
			strings.Repeat("g", 64),
			"https://drive.example.com/download/asset"),
	}
	if err := finalJobImagePreflight(items, nil, false); err == nil {
		t.Fatal("a non-hex content address is not a verified asset")
	}
}
