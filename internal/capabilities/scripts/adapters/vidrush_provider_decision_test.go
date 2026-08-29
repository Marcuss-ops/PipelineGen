package adapters

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestSegmentProviderDecisionUsesPlannerMetadataAndHardAllowlist(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		Planner:        mediadomain.MediaPlannerPolicy{Strategy: "small_model_rerank", Model: "qwen3:1.7b", Version: "v1", CandidateLimit: 8},
		ProviderPolicy: mediadomain.MediaProviderPolicy{YouTube: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleDisabled},
	}}
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "segment-1", Insights: scriptpkg.SegmentInsights{
		Entities: []scriptpkg.ExtractedEntity{{Value: "Ada Lovelace", Type: "PERSON"}},
	}}
	decision := buildSegmentProviderDecision(plan, segment, "video")
	if decision.Strategy != "small_model_rerank" || decision.Model != "qwen3:1.7b" || decision.Version != "v1" || decision.CandidateLimit != 8 {
		t.Fatalf("decision metadata=%+v", decision)
	}
	if !providerEnabledByHardPolicy(plan, scriptpkg.VidRushProviderYouTube) {
		t.Fatal("enabled YouTube policy was rejected")
	}
	if providerEnabledByHardPolicy(plan, scriptpkg.VidRushProviderArtlist) {
		t.Fatal("disabled Artlist policy was allowed")
	}
}

func TestEffectiveProviderEnabledRequiresDecisionAndPolicy(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{YouTube: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleDisabled},
	}}
	decision := SegmentProviderDecision{Preferences: []ProviderPreference{{Provider: scriptpkg.VidRushProviderYouTube}}}
	if !effectiveProviderEnabled(plan, decision, scriptpkg.VidRushProviderYouTube) {
		t.Fatal("YouTube should be effective")
	}
	if effectiveProviderEnabled(plan, decision, scriptpkg.VidRushProviderArtlist) {
		t.Fatal("Artlist must remain blocked by hard allowlist")
	}
	if effectiveProviderEnabled(plan, SegmentProviderDecision{}, scriptpkg.VidRushProviderYouTube) {
		t.Fatal("provider without decision must not start")
	}
}
