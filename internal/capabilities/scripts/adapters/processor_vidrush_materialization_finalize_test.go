package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSelectVidRushPrimaryVideoRequiresDurableVideo(t *testing.T) {
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "remote", Provider: scriptpkg.VidRushProviderArtlist, Score: 1},
		{AssetID: "image", Provider: scriptpkg.VidRushProviderInternetImages, DriveLink: "https://drive/image"},
	}
	if got := selectVidRushPrimaryVideo(candidates); got != nil {
		t.Fatalf("primary = %+v, want nil for non-durable candidates", got)
	}
}

func TestSelectVidRushPrimaryVideoChoosesHighestVerifiedVideo(t *testing.T) {
	candidates := []scriptpkg.SegmentAssetCandidate{
		{AssetID: "low", Provider: scriptpkg.VidRushProviderArtlist, Score: 0.2, AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted, IndexStatus: scriptpkg.VidRushStatusIndexed, DriveLink: "https://drive/low", LocalPath: "/tmp/low.mp4", MIMEType: "video/mp4"},
		{AssetID: "high", Provider: scriptpkg.VidRushProviderYouTube, Score: 0.9, AcquisitionStatus: scriptpkg.VidRushStatusAcquired, VerificationStatus: scriptpkg.VidRushStatusVerified, PersistenceStatus: scriptpkg.VidRushStatusPersisted, IndexStatus: scriptpkg.VidRushStatusIndexed, DriveLink: "https://drive/high", LocalPath: "/tmp/high.mp4", MIMEType: "video/mp4"},
	}
	got := selectVidRushPrimaryVideo(candidates)
	if got == nil || got.AssetID != "high" {
		t.Fatalf("primary = %+v, want high", got)
	}
	if got.SelectionReason == "" {
		t.Fatal("selected primary should carry a selection reason")
	}
}

func TestVidRushArtlistDiagnosticsLimitsCandidates(t *testing.T) {
	got := vidRushArtlistDiagnostics([]scriptpkg.SegmentAssetCandidate{
		{AssetID: "a", Provider: scriptpkg.VidRushProviderArtlist},
		{AssetID: "b", Provider: scriptpkg.VidRushProviderArtlist},
		{AssetID: "c", Provider: scriptpkg.VidRushProviderArtlist},
		{AssetID: "d", Provider: scriptpkg.VidRushProviderArtlist},
	})
	if len(got) != 3 {
		t.Fatalf("diagnostics = %v, want three entries", got)
	}
}
