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
	plan, err := CompileOverlayPlan(result, "en", GoldenOverlayCanvas, "plan-scene-image", "video", "project", true)
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
	image, ok := sceneImageCandidate(result, "scene-3", 12_345_678)
	if !ok {
		t.Fatal("ready scene image was not projected")
	}
	wantStartUS := 12_345_000 + capabilityoverlay.MaxImageOverlayDurationMS*1000
	if image.AssetID != sha || image.SHA256 != sha || image.StartUS != wantStartUS || image.DurationUS != capabilityoverlay.MaxImageOverlayDurationMS*1000 {
		t.Fatalf("scene image projection = %+v", image)
	}
	if _, ok := sceneImageCandidate(result, "scene-4", 12_345_678); ok {
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
	image, ok := sceneImageCandidate(result, "scene-2", 0, map[string]struct{}{firstSHA: {}})
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
	if _, ok := sceneImageCandidate(result, "scene-1", 0); ok {
		t.Fatal("candidate without canonical content address was accepted")
	}
}
