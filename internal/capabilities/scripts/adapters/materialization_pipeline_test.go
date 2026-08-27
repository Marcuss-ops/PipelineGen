package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestMaterializationPipelineStages(t *testing.T) {
	candidate := scriptpkg.SegmentAssetCandidate{AssetID: "asset-1", Provider: "internet_images", SourceURL: "https://source.example/a"}
	source, ok := resolveSource(candidate)
	if !ok || source != candidate.SourceURL {
		t.Fatalf("resolveSource = %q, %v", source, ok)
	}

	item := MaterializationPipelineItem{Candidate: candidate}
	item = materializeBytes(item, []byte("bytes"))
	item = probeMedia(item, "image/jpeg", 0)
	item = certifyContent(item, "sha256")
	asset := buildCanonicalAsset(item)
	if asset.LegacyFileMD5 != "sha256" || string(item.Bytes) != "bytes" || item.MediaType != "image/jpeg" {
		t.Fatalf("pipeline item = %+v asset = %+v", item, asset)
	}
	committed := commitCanonicalAsset(nil, asset)
	if len(committed) != 1 || committed[0].AssetID != "asset-1" {
		t.Fatalf("committed = %+v", committed)
	}
}

func TestResolveSourceRejectsEmptyCandidate(t *testing.T) {
	if source, ok := resolveSource(scriptpkg.SegmentAssetCandidate{}); ok || source != "" {
		t.Fatalf("empty candidate resolved to %q", source)
	}
}
