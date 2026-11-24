package vidrush

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestValidVidRushCandidateRejectsNonHTTPURL(t *testing.T) {
	candidate := scriptpkg.SegmentAssetCandidate{
		AssetID:   "scheme",
		Provider:  scriptpkg.VidRushProviderInternetImages,
		SourceURL: "file:///etc/passwd",
	}
	if validVidRushCandidate(candidate) {
		t.Fatal("file:// candidate passed selection")
	}
	candidate.SourceURL = "https://images.example/scheme.jpg"
	if !validVidRushCandidate(candidate) {
		t.Fatal("absolute HTTPS candidate was rejected")
	}
}

func TestCandidateSetHashIgnoresMutableLifecycleStatus(t *testing.T) {
	candidate := canonicalReadyEntityImageCandidate("hash", "John Gotti")
	before := candidateSetHash([]scriptpkg.SegmentAssetCandidate{candidate})
	candidate.SemanticStatus = "rejected"
	candidate.AcquisitionStatus = "failed"
	candidate.VerificationStatus = "failed"
	candidate.PersistenceStatus = "failed"
	candidate.IndexStatus = "failed"
	candidate.DriveLink = ""
	after := candidateSetHash([]scriptpkg.SegmentAssetCandidate{candidate})
	if before != after {
		t.Fatalf("candidate set hash changed with lifecycle status: %q != %q", before, after)
	}
}

func TestReadyVidRushCandidateUnifiesProviderAndURLFallback(t *testing.T) {
	youtube := scriptpkg.SegmentAssetCandidate{
		AssetID:            "yt",
		Provider:           scriptpkg.VidRushProviderYouTube,
		SourceURL:          "https://youtube.com/watch?v=x",
		SourceStartMs:      0,
		SourceEndMs:        1000,
		DriveLink:          "https://drive.google.com/file/d/yt/view",
		RightsStatus:       "verified",
		AcquisitionStatus:  scriptpkg.VidRushStatusAcquired,
		VerificationStatus: scriptpkg.VidRushStatusVerified,
		PersistenceStatus:  scriptpkg.VidRushStatusPersisted,
		IndexStatus:        "indexed",
	}
	if !readyVidRushCandidate(youtube) {
		t.Fatal("valid YouTube candidate was not ready")
	}

	internet := canonicalReadyEntityImageCandidate("net", "John Gotti")
	if !readyVidRushCandidate(internet) {
		t.Fatal("durable internet image candidate was not ready")
	}
	internet.DriveLink = ""
	if readyVidRushCandidate(internet) {
		t.Fatal("internet image without a durable Drive copy passed the normal readiness path")
	}
	if !verifiedImageCandidateWithoutDrive(internet) {
		t.Fatal("verified internet image lost its explicit URL fallback")
	}
}

func TestPreserveSelectedVidRushImagesKeepsSelectedIdentity(t *testing.T) {
	selected := canonicalReadyEntityImageCandidate("same", "John Gotti")
	selected.DriveLink = "https://drive.google.com/file/d/selected/view"
	refreshed := canonicalReadyEntityImageCandidate("same", "John Gotti")
	refreshed.DriveLink = "https://drive.google.com/file/d/refreshed/view"

	out := preserveSelectedVidRushImages(
		[]scriptpkg.SegmentAssetCandidate{selected},
		[]scriptpkg.SegmentAssetCandidate{refreshed},
	)
	if len(out) != 1 || out[0].DriveLink != selected.DriveLink {
		t.Fatalf("selected identity was replaced: %+v", out)
	}
	if !strings.Contains(out[0].DriveLink, "selected") {
		t.Fatalf("selected Drive link = %q", out[0].DriveLink)
	}
}
