package adapters

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func hashedCandidate(assetID, query, hash string) scriptpkg.SegmentAssetCandidate {
	candidate := readyImageCandidateForSelection(assetID, query, .8)
	candidate.PerceptualHash = hash
	return candidate
}

func TestDropPerceptualDuplicatesCollapsesNearIdenticalImages(t *testing.T) {
	base := hashedCandidate("base", "reef", "dhash64:0000000000000000")
	near := hashedCandidate("near", "reef-two", "dhash64:0000000000000001") // 1 bit apart
	distinct := hashedCandidate("distinct", "reef-three", "dhash64:ffffffffffffffff")
	unhashed := hashedCandidate("unhashed", "reef-four", "")

	out := dropPerceptualDuplicates([]scriptpkg.SegmentAssetCandidate{base, near, distinct, unhashed})
	got := make([]string, 0, len(out))
	for _, candidate := range out {
		got = append(got, candidate.AssetID)
	}
	want := []string{"base", "distinct", "unhashed"}
	if len(got) != len(want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kept %v, want %v", got, want)
		}
	}
}

func TestDropPerceptualDuplicatesIgnoresMalformedHashes(t *testing.T) {
	a := hashedCandidate("a", "q", "dhash64:0000000000000000")
	b := hashedCandidate("b", "q2", "not-a-hash")
	out := dropPerceptualDuplicates([]scriptpkg.SegmentAssetCandidate{a, b})
	if len(out) != 2 {
		t.Fatalf("malformed hash caused a drop: %+v", out)
	}
}

func TestSelectExactVidRushImagesSkipsPerceptualDuplicates(t *testing.T) {
	// Artlist is enabled, so this is NOT the Images-only grouping path: the
	// selection is a straight prefix of the durable images, which makes the
	// perceptual filter the only thing that can remove a candidate here.
	plan := &scriptpkg.ResolvedGenerationPlan{ImagesPerScene: 3, MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{
			InternetImages: media.MediaToggleEnabled,
			Artlist:        media.MediaToggleEnabled,
		},
	}}
	candidates := []scriptpkg.SegmentAssetCandidate{
		hashedCandidate("first", "reef", "dhash64:0000000000000000"),
		hashedCandidate("duplicate", "reef-dup", "dhash64:0000000000000003"),
		hashedCandidate("second", "reef-two", "dhash64:ffffffffffffffff"),
		hashedCandidate("third", "reef-three", "dhash64:aaaaaaaaaaaaaaaa"),
	}
	selected := selectExactVidRushImages(candidates, 3, plan)
	if len(selected) != 3 {
		t.Fatalf("selected = %d, want 3: %+v", len(selected), selected)
	}
	for _, candidate := range selected {
		if candidate.AssetID == "duplicate" {
			t.Fatalf("near-duplicate survived selection: %+v", selected)
		}
	}
}
