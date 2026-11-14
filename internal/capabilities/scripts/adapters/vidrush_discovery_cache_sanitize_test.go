package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSanitizeVidRushDiscoveryCandidatesDropsMaterializationState(t *testing.T) {
	input := []scriptpkg.SegmentAssetCandidate{{
		AssetID:            "File:Quad fountain.jpg",
		Provider:           "internet_images",
		Query:              "Bobby Stewart",
		Entity:             "Bobby Stewart",
		SourceURL:          "https://example.invalid/old.jpg",
		DriveLink:          "https://drive.google.com/file/d/stale/view",
		ContentSHA256:      "43a89a82c81d50ced4de58c31cbba743a6f0981902e9bc33efc3e7b88155f1c8",
		LocalPath:          "/tmp/stale.jpg",
		AcquisitionStatus:  "acquired",
		VerificationStatus: "verified",
		PersistenceStatus:  "persisted",
		IndexStatus:        "indexed",
	}}

	got := sanitizeVidRushDiscoveryCandidates(input)
	if len(got) != 1 {
		t.Fatalf("sanitized candidates = %d, want 1", len(got))
	}
	candidate := got[0]
	if candidate.DriveLink != "" || candidate.ContentSHA256 != "" || candidate.LocalPath != "" {
		t.Fatalf("durable materialization leaked into discovery: %+v", candidate)
	}
	if candidate.AcquisitionStatus != "" || candidate.VerificationStatus != "" ||
		candidate.PersistenceStatus != "" || candidate.IndexStatus != "" {
		t.Fatalf("materialization lifecycle leaked into discovery: %+v", candidate)
	}
	if candidate.AssetID != input[0].AssetID || candidate.SourceURL != input[0].SourceURL {
		t.Fatalf("discovery identity was lost: %+v", candidate)
	}
	if input[0].DriveLink == "" {
		t.Fatal("sanitize mutated the caller's candidate slice")
	}
}

func TestSanitizeInternetImageCachePayloadDropsMaterializationState(t *testing.T) {
	payload := sanitizeInternetImageCachePayload(internetImageCachePayload{
		Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID:       "stale-asset",
			SourceURL:     "https://example.invalid/stale.jpg",
			DriveLink:     "https://drive.google.com/file/d/stale/view",
			ContentSHA256: "stale-hash",
			LocalPath:     "/tmp/stale.jpg",
		}},
	})
	if len(payload.Candidates) != 1 {
		t.Fatalf("payload candidates = %d, want 1", len(payload.Candidates))
	}
	if payload.Candidates[0].DriveLink != "" || payload.Candidates[0].ContentSHA256 != "" || payload.Candidates[0].LocalPath != "" {
		t.Fatalf("durable materialization leaked into payload: %+v", payload.Candidates[0])
	}
}
