package adapters

import (
	"encoding/json"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"testing"
)

func TestExplainVidRushSegment(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{YouTube: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleEnabled}}}
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "s", Assets: scriptpkg.SegmentAssetSelection{PrimaryVideo: &scriptpkg.SegmentAssetCandidate{Provider: "youtube", AssetID: "asset-1", Score: .93, SelectionReason: "direct historical footage", SourceURL: "https://youtube.test/v", SourceStartMs: 181000, SourceEndMs: 190000}}}
	got := ExplainVidRushSegment(plan, segment, "video")
	if got.SegmentID != "s" || got.Winner == nil || got.Winner.Provider != "youtube" || got.Winner.AssetID != "asset-1" || got.Winner.SourceURL != "https://youtube.test/v" || got.Winner.SourceStartMs != 181000 || got.Winner.SourceEndMs != 190000 || got.Winner.Score != .93 || got.Winner.Reason != "direct historical footage" {
		t.Fatalf("explanation=%+v", got)
	}
	if len(got.ProvidersConsidered) != 2 {
		t.Fatalf("providers=%+v", got.ProvidersConsidered)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	providers, ok := decoded["providers_considered"].([]any)
	if !ok || len(providers) != 2 {
		t.Fatalf("serialized providers=%v", decoded["providers_considered"])
	}
	winner, ok := decoded["winner"].(map[string]any)
	if !ok || winner["asset_id"] != "asset-1" || winner["start_ms"] != float64(181000) || winner["end_ms"] != float64(190000) {
		t.Fatalf("serialized winner=%v", decoded["winner"])
	}
}
