package scriptgeneration

import (
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestCompileOverlayPlanAddsPerSceneImageWithoutChangingEntityCardScope(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result := phraseCeilingFixture(t)
	result.Segments = []scriptpkg.VidRushSegmentResult{{
		SceneID: "scene-0",
		Assets: scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "logical-scene-image", Provider: scriptpkg.VidRushProviderInternetImages,
			SourceURL: "https://images.example/context.jpg", LocalPath: "/tmp/context.jpg", LegacyFileMD5: sha,
			MIMEType: "image/jpeg", AcquisitionStatus: scriptpkg.VidRushStatusAcquired,
			VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted,
			IndexStatus: scriptpkg.VidRushStatusIndexed,
		}}},
	}}
	// Leave room after the five-second entity-card opening for the scene still.
	result.ResolvedScenes[0].DurationUS = 7_000_000
	plan, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-scene-image", "video", "project", true, true)
	if err != nil {
		t.Fatalf("compile plan: %v", err)
	}
	images := 0
	for _, item := range plan.Items {
		if item.Kind != "image" {
			continue
		}
		images++
		if item.SceneID != "scene-0" || len(item.AssetRefs) != 1 || item.AssetRefs[0].SHA256 != sha {
			t.Fatalf("scene image item = %+v", item)
		}
	}
	if images != 1 {
		t.Fatalf("generic per-scene images = %d, want 1", images)
	}
	withoutSceneStills, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-entity-images-only", "video", "project", true, false)
	if err != nil {
		t.Fatalf("compile entity-only plan: %v", err)
	}
	for _, item := range withoutSceneStills.Items {
		if item.Kind == "image" && strings.HasPrefix(item.ID, "scene-0-image-") {
			t.Fatalf("generic scene image emitted while images_per_scene=0: %+v", item)
		}
	}
}

func TestSceneImageCandidateUsesMaterializedAssetAndSceneTiming(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result := &GenerateResult{Segments: []scriptpkg.VidRushSegmentResult{{
		SceneID: "scene-3",
		Assets: scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "logical-image-3", Provider: scriptpkg.VidRushProviderInternetImages,
			SourceURL: "https://images.example/stj.jpg", LocalPath: "/tmp/stj.jpg", LegacyFileMD5: sha,
			MIMEType: "image/jpeg", AcquisitionStatus: scriptpkg.VidRushStatusAcquired,
			VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted,
			IndexStatus: scriptpkg.VidRushStatusIndexed,
		}}},
	}}}
	image, ok := sceneImageCandidate(result, "scene-3", 12_345_678, nil, 0)
	if !ok {
		t.Fatal("ready scene image was not projected")
	}
	wantStartUS := 12_345_000 + capabilityoverlay.MaxImageOverlayDurationMS*1000
	if image.AssetID != sha || image.SHA256 != sha || image.StartUS != wantStartUS || image.DurationUS != capabilityoverlay.MaxImageOverlayDurationMS*1000 {
		t.Fatalf("scene image projection = %+v", image)
	}
	if _, ok := sceneImageCandidate(result, "scene-4", 12_345_678, nil, 0); ok {
		t.Fatal("image from another scene was reused")
	}
}

func TestSceneImageCandidateChoosesNextDistinctMaterializedAsset(t *testing.T) {
	firstSHA := strings.Repeat("a", 64)
	secondSHA := strings.Repeat("b", 64)
	ready := func(id, sha string) scriptpkg.SegmentAssetCandidate {
		return scriptpkg.SegmentAssetCandidate{
			AssetID: id, Provider: scriptpkg.VidRushProviderInternetImages,
			SourceURL: "https://images.example/" + id + ".jpg", LocalPath: "/tmp/" + id + ".jpg", LegacyFileMD5: sha,
			MIMEType: "image/jpeg", AcquisitionStatus: scriptpkg.VidRushStatusAcquired,
			VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted,
			IndexStatus: scriptpkg.VidRushStatusIndexed,
		}
	}
	result := &GenerateResult{Segments: []scriptpkg.VidRushSegmentResult{{
		SceneID: "scene-2",
		Assets: scriptpkg.SegmentAssetSelection{
			SecondaryImages: []scriptpkg.SegmentAssetCandidate{ready("first", firstSHA)},
			Candidates:      []scriptpkg.SegmentAssetCandidate{ready("second", secondSHA)},
		},
	}}}
	image, ok := sceneImageCandidate(result, "scene-2", 0, map[string]struct{}{firstSHA: {}}, 0)
	if !ok || image.SHA256 != secondSHA {
		t.Fatalf("distinct fallback image = %+v, ok=%v; want %s", image, ok, secondSHA)
	}
}

func TestSceneImageCandidateRejectsNonContentAddressedSelection(t *testing.T) {
	result := &GenerateResult{Segments: []scriptpkg.VidRushSegmentResult{{
		SceneID: "scene-1", Assets: scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "bad-image", Provider: scriptpkg.VidRushProviderInternetImages,
			SourceURL: "https://images.example/bad.jpg", LegacyFileMD5: "not-a-sha256",
			AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified,
			PersistenceStatus: scriptpkg.VidRushStatusPersisted, IndexStatus: scriptpkg.VidRushStatusIndexed,
		}}},
	}}}
	if _, ok := sceneImageCandidate(result, "scene-1", 0, nil, 0); ok {
		t.Fatal("candidate without canonical content address was accepted")
	}
}

func TestSceneImageCandidateClampsToOwningSceneEnd(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result := &GenerateResult{Segments: []scriptpkg.VidRushSegmentResult{{
		SceneID: "scene-short",
		Assets: scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "short-image", Provider: scriptpkg.VidRushProviderInternetImages,
			SourceURL: "https://images.example/short.jpg", LocalPath: "/tmp/short.jpg", LegacyFileMD5: sha,
			MIMEType: "image/jpeg", AcquisitionStatus: scriptpkg.VidRushStatusAcquired,
			VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted,
			IndexStatus: scriptpkg.VidRushStatusIndexed,
		}}},
	}}}
	image, ok := sceneImageCandidate(result, "scene-short", 7_248_000, nil, 16_896_000)
	if !ok {
		t.Fatal("short-scene image was not projected")
	}
	if image.StartUS != 12_248_000 || image.DurationUS != 4_648_000 || image.EndMs != 16_896 {
		t.Fatalf("short-scene image range = %+v; want [12248000,16896000)", image)
	}
}
