// Package adapters — reconciliation_image_test.go
// covers the Drive-backed image binding reconciliation paths of the
// asset-location reconciliation processor: missing/malformed Drive
// images are cleared and failed, while non-Drive provider URLs are
// left untouched.
package adapters

import (
	"context"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// 12. Drive-backed image missing → URL cleared and status failed.
func TestAssetLocationReconciliation_DriveImageMissingCleared(t *testing.T) {
	link := "https://drive.google.com/file/d/image-gone/view"
	r := newStubVerifier()
	r.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID: "image-1", DriveFileID: "image-gone", State: scriptpkg.LocationStateMissing,
	})

	p := NewAssetLocationReconciliationProcessor(r)
	got, err := p.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithImage("image-1", link),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	image := got.UpdatedSpecScene.Scenes[0].Bindings.Image
	if image.URL != "" {
		t.Fatalf("Drive image URL should be cleared, got %q", image.URL)
	}
	if image.Status != string(scriptpkg.ImageStatusFailed) {
		t.Fatalf("Drive image status = %q, want %q", image.Status, scriptpkg.ImageStatusFailed)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "MISSING") {
		t.Fatalf("expected one image MISSING warning, got %v", got.Warnings)
	}
}

func TestAssetLocationReconciliation_MalformedDriveImageCleared(t *testing.T) {
	link := "https://drive.google.com/file/d//view"
	verifier := newStubVerifier()
	verifier.stubResult(link, &scriptpkg.VerifiedLocation{
		AssetID:   "image-malformed",
		State:     scriptpkg.LocationStateMalformed,
		ErrorCode: "MALFORMED_LINK",
	})

	processor := NewAssetLocationReconciliationProcessor(verifier)
	result, err := processor.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithImage("image-malformed", link),
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	image := result.UpdatedSpecScene.Scenes[0].Bindings.Image
	if image.URL != "" || image.Status != string(scriptpkg.ImageStatusFailed) {
		t.Fatalf("malformed Drive image must be cleared and failed, got URL=%q status=%q", image.URL, image.Status)
	}
}

// 13. Non-Drive provider image URL is not sent to the Drive verifier.
func TestAssetLocationReconciliation_ExternalImageURLPreserved(t *testing.T) {
	link := "https://images.example.test/generated/image-1.png"
	p := NewAssetLocationReconciliationProcessor(newStubVerifier())
	got, err := p.Process(context.Background(), nil, ProcessInput{
		SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{
			sceneWithImage("image-1", link),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedSpecScene.Scenes[0].Bindings.Image.URL != link {
		t.Fatalf("external image URL should be preserved, got %q", got.UpdatedSpecScene.Scenes[0].Bindings.Image.URL)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("external image URL should not produce Drive warnings, got %v", got.Warnings)
	}
}
