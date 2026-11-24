package vidrush

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestProjectEntityImageBindingsBindsSecondaryGPE(t *testing.T) {
	spec := scriptpkg.SpecSceneOutput{Scenes: []scriptpkg.SpecScene{{
		ID: "scene-1", SegmentID: "seg-1", Index: 0,
		Annotations: &scriptpkg.SceneAnnotations{SecondaryEntities: []scriptpkg.AnnotatedEntity{{
			Text: "Chichén Itzá", CanonicalName: "Chichén Itzá", Type: "GPE",
		}}},
	}}}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "seg-1", SceneID: "scene-1", Position: 0,
		Assets: scriptpkg.SegmentAssetSelection{SecondaryImages: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "gpe-asset", Provider: scriptpkg.VidRushProviderInternetImages,
			Entity: "Chichén Itzá", Query: "Chichén Itzá",
			SourceURL: "https://images.example/chichen-itza.jpg", MIMEType: "image/jpeg",
			DriveLink:     "https://drive.google.com/file/d/gpe-asset/view",
			ContentSHA256: strings.Repeat("a", 64), SemanticStatus: "accepted",
			RightsStatus: "unknown_allowed", AcquisitionStatus: scriptpkg.VidRushStatusAcquired,
			VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted,
			IndexStatus: scriptpkg.VidRushStatusIndexed,
		}}},
	}}
	out := projectEntityImageBindings(spec, segments, media.EntityImagePolicy{Enabled: true})
	image := out.Scenes[0].Annotations.SecondaryEntities[0].Image
	if image == nil || image.Status != "resolved" || image.AssetID != "gpe-asset" {
		t.Fatalf("secondary GPE image = %+v, want the resolved candidate", image)
	}
}
