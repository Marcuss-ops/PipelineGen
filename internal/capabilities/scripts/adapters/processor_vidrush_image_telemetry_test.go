package adapters

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestLogVidRushSelectedImagesEmitsProviderQueryAndScore(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logVidRushSelectedImages(zap.New(core), "seg-1", []scriptpkg.SegmentAssetCandidate{
		{AssetID: "a", Provider: "internet_images", Query: "reef", Score: 0.9, PerceptualHash: "dhash64:0000000000000000"},
		{AssetID: "b", Provider: "image_generation", Query: "coral", Score: 0.4},
	})

	entries := logs.FilterMessage("VidRush image selected").All()
	if len(entries) != 2 {
		t.Fatalf("emitted %d telemetry lines, want 2", len(entries))
	}
	context := entries[0].ContextMap()
	if context["provider"] != "internet_images" || context["query"] != "reef" || context["score"] != 0.9 {
		t.Fatalf("first entry context = %v", context)
	}
	if context["segment_id"] != "seg-1" || context["asset_id"] != "a" {
		t.Fatalf("first entry identity = %v", context)
	}
	if context["perceptual_hash"] != "dhash64:0000000000000000" {
		t.Fatalf("first entry perceptual hash = %v", context["perceptual_hash"])
	}
}

func TestLogVidRushSelectedImagesIsNoOpForNilLoggerAndEmptySelection(t *testing.T) {
	logVidRushSelectedImages(nil, "seg-1", []scriptpkg.SegmentAssetCandidate{{AssetID: "a"}})

	core, logs := observer.New(zap.DebugLevel)
	logVidRushSelectedImages(zap.New(core), "seg-empty", nil)
	if logs.Len() != 0 {
		t.Fatalf("empty selection emitted %d lines, want 0", logs.Len())
	}
}
