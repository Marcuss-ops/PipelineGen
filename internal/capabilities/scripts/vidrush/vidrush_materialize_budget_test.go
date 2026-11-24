package vidrush

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestVidRushAcquisitionBudgetKeyScopesByEntityAndScene(t *testing.T) {
	alice := vidRushAcquisitionBudgetKey("duckduckgo", "Alice", "s01")
	bob := vidRushAcquisitionBudgetKey("duckduckgo", "Bob", "s01")
	if alice == bob {
		t.Fatal("two entities in the same scene share one acquisition budget")
	}
	if alice == vidRushAcquisitionBudgetKey("duckduckgo", "Alice", "s02") {
		t.Fatal("the same entity in two scenes shares one acquisition budget")
	}
	if vidRushAcquisitionBudgetKey("duckduckgo", "", "s01") == vidRushAcquisitionBudgetKey("duckduckgo", "", "s02") {
		t.Fatal("two scenes without an entity share one acquisition budget")
	}
}

func TestVidRushReadyImageGroupForContentHash(t *testing.T) {
	hash := strings.Repeat("d", 64)
	first := scriptpkg.SegmentAssetCandidate{Provider: scriptpkg.VidRushProviderInternetImages, AssetID: "asset-1", ContentSHA256: hash}
	second := scriptpkg.SegmentAssetCandidate{Provider: scriptpkg.VidRushProviderInternetImages, AssetID: "asset-2", ContentSHA256: hash}
	if vidRushReadyImageGroupFor(first, true, false) != vidRushReadyImageGroupFor(second, true, false) {
		t.Fatal("duplicate content hashes must share one ready-image group")
	}
	second.ContentSHA256 = strings.Repeat("e", 64)
	if vidRushReadyImageGroupFor(first, true, false) == vidRushReadyImageGroupFor(second, true, false) {
		t.Fatal("different content hashes must not share a ready-image group")
	}
}
