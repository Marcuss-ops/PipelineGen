package usecase

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestEvaluateQualityGate_PassesCleanGeneration(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "The quick brown fox jumps over the lazy dog in Italy.",
			WordCount: 10,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-1"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:    "en",
		SourceText:  "The quick brown fox jumps over the lazy dog in Italy.",
		TargetWords: 10,
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err != nil {
		t.Fatalf("expected quality gate to pass, got error: %v", err)
	}
	if quality == nil {
		t.Fatal("expected quality to be populated")
	}
	if !quality.Passed {
		t.Fatalf("expected quality.Passed=true, got false with quality=%+v", quality)
	}
	if quality.LanguageRequested != "en" {
		t.Errorf("language_requested=%q want en", quality.LanguageRequested)
	}
	if quality.LanguageDetected != "en" {
		t.Errorf("language_detected=%q want en", quality.LanguageDetected)
	}
	if quality.SourceTextCoverage < defaultMinSourceTextCoverage {
		t.Errorf("source_text_coverage=%.2f want >= %.2f", quality.SourceTextCoverage, defaultMinSourceTextCoverage)
	}
	if quality.ClipEvidenceCoverage != 1.0 {
		t.Errorf("clip_evidence_coverage=%.2f want 1.0", quality.ClipEvidenceCoverage)
	}
	if quality.UnsupportedClaims != 0 {
		t.Errorf("unsupported_claims=%d want 0", quality.UnsupportedClaims)
	}
	if quality.TargetWords != 10 {
		t.Errorf("target_words=%d want 10", quality.TargetWords)
	}
	if quality.ActualWords != 10 {
		t.Errorf("actual_words=%d want 10", quality.ActualWords)
	}
}

func TestEvaluateQualityGate_FailsLanguageMismatch(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "Il gatto salta sul tavolo e guarda il cielo.",
			WordCount: 9,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-2"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:   "en",
		SourceText: "The cat jumps on the table and looks at the sky.",
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for language mismatch")
	}
	if quality == nil {
		t.Fatal("expected quality to be populated on failure")
	}
	if quality.Passed {
		t.Fatal("expected quality.Passed=false")
	}
	if quality.LanguageDetected != "it" {
		t.Errorf("language_detected=%q want it", quality.LanguageDetected)
	}
	if quality.LanguageRequested != "en" {
		t.Errorf("language_requested=%q want en", quality.LanguageRequested)
	}
}

func TestEvaluateQualityGate_FailsSourceTextCoverage(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "A completely different story about space travel and aliens.",
			WordCount: 9,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-3"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:   "en",
		SourceText: "The quick brown fox jumps over the lazy dog.",
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for low source coverage")
	}
	if quality.SourceTextCoverage >= defaultMinSourceTextCoverage {
		t.Errorf("source_text_coverage=%.2f want < %.2f", quality.SourceTextCoverage, defaultMinSourceTextCoverage)
	}
}

func TestEvaluateQualityGate_FailsTargetWordsTolerance(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "The quick brown fox.",
			WordCount: 4,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-4"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:    "en",
		SourceText:  "The quick brown fox jumps over the lazy dog.",
		TargetWords: 100,
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for target words tolerance")
	}
	if quality.ActualWords != 4 {
		t.Errorf("actual_words=%d want 4", quality.ActualWords)
	}
	if quality.TargetWords != 100 {
		t.Errorf("target_words=%d want 100", quality.TargetWords)
	}
}

func TestEvaluateQualityGate_FailsEmptyText(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "   ",
			WordCount: 0,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-5"}
	plan := scriptpkg.ResolvedGenerationPlan{Language: "en"}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for empty text")
	}
	if quality.Passed {
		t.Fatal("expected quality.Passed=false")
	}
}

func TestEvaluateQualityGate_FailsGenericText(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "This is a sample text placeholder for the video.",
			WordCount: 9,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-6"}
	plan := scriptpkg.ResolvedGenerationPlan{Language: "en"}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for generic text")
	}
	if quality.Passed {
		t.Fatal("expected quality.Passed=false")
	}
}

func TestEvaluateQualityGate_SourceAbsentIsNotEvaluated(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{Text: "The generated documentary narrative is complete.", WordCount: 6},
	}
	quality, err := evaluateQualityGate(result, scriptpkg.GenerationItemV2{ID: "no-source"}, scriptpkg.ResolvedGenerationPlan{Language: "en"})
	if err != nil {
		t.Fatalf("source-free generation should not fail the source coverage check: %v", err)
	}
	if quality.SourceTextCoverageStatus != "NOT_EVALUATED" {
		t.Fatalf("status=%q want NOT_EVALUATED", quality.SourceTextCoverageStatus)
	}
	if quality.SourceTextCoverage == 1.0 {
		t.Fatal("source-free coverage must not be promoted to an artificial pass")
	}
}

func TestEvaluateQualityGate_FailsClipEvidenceCoverage(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "Scene one uses clip one.",
			WordCount: 5,
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{
						ID:    "scene-1",
						Index: 0,
						Text:  "Scene one uses clip one.",
						Kind:  scriptpkg.SceneClip,
						Bindings: scriptpkg.SceneBindings{
							Clip: &scriptpkg.ClipBinding{ClipID: "clip-1"},
						},
					},
				},
			},
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-7"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:        "en",
		SourceText:      "Scene one uses clip one.",
		GroundingPolicy: scriptpkg.GroundingPolicyClipsPrimary,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-1", "clip-2"},
		},
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for incomplete clip evidence coverage")
	}
	if quality.ClipEvidenceCoverage != 0.5 {
		t.Errorf("clip_evidence_coverage=%.2f want 0.5", quality.ClipEvidenceCoverage)
	}
}

func TestEvaluateQualityGate_FailsUnsupportedClaims(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "Marco Polo visited Venice and discovered new lands.",
			WordCount: 9,
		},
		Artifacts: scriptpkg.ArtifactResult{
			Entities: &scriptpkg.EntityResult{
				Persons: []scriptpkg.Entity{{Value: "Marco Polo"}},
				Places:  []scriptpkg.Entity{{Value: "Venice"}},
				Concepts: []scriptpkg.Entity{
					{Value: "new lands"},
					{Value: "unknown concept"},
				},
			},
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-8"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:   "en",
		SourceText: "Marco Polo visited Venice and discovered new lands.",
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for unsupported claims")
	}
	if quality.UnsupportedClaims != 1 {
		t.Errorf("unsupported_claims=%d want 1", quality.UnsupportedClaims)
	}
}

func TestComputeSourceTextCoverage(t *testing.T) {
	generated := "the quick brown fox jumps"
	source := "the quick brown fox jumps over the lazy dog"
	coverage := computeSourceTextCoverage(generated, source)
	if coverage != 1.0 {
		t.Errorf("coverage=%.2f want 1.0", coverage)
	}

	generated = "space travel aliens unknown planet"
	source = "the quick brown fox jumps over the lazy dog"
	coverage = computeSourceTextCoverage(generated, source)
	if coverage >= defaultMinSourceTextCoverage {
		t.Errorf("coverage=%.2f want < %.2f", coverage, defaultMinSourceTextCoverage)
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"The quick brown fox jumps over the lazy dog.", "en"},
		{"Il gatto salta sul tavolo.", "it"},
		{"El gato salta sobre la mesa.", "es"},
		{"Le chat saute sur la table.", "fr"},
		{"Der Hund läuft im Park.", "de"},
		{"xyz qwerty 12345", ""},
	}
	for _, tc := range cases {
		got := detectLanguage(tc.text)
		if got != tc.want {
			t.Errorf("detectLanguage(%q)=%q want %q", tc.text, got, tc.want)
		}
	}
}

func TestPolicyThresholds(t *testing.T) {
	cases := []struct {
		policy      string
		wantSource  float64
		wantClip    float64
		description string
	}{
		{scriptpkg.GroundingPolicyClipsPrimary, 0.40, 1.00, "clips_primary: lower text threshold, full clip binding"},
		{scriptpkg.GroundingPolicySourcePrimary, 0.85, 0.00, "source_primary: high text threshold, no clip binding required"},
		{scriptpkg.GroundingPolicyBalanced, 0.60, 0.50, "balanced: moderate text and clip thresholds"},
		{"", defaultMinSourceTextCoverage, 0.00, "default: fallback text threshold, no clip binding"},
	}

	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			src, clip := policyThresholds(tc.policy)
			if src != tc.wantSource {
				t.Errorf("policyThresholds(%q) source=%.2f want %.2f", tc.policy, src, tc.wantSource)
			}
			if clip != tc.wantClip {
				t.Errorf("policyThresholds(%q) clip=%.2f want %.2f", tc.policy, clip, tc.wantClip)
			}
		})
	}
}

func TestEvaluateQualityGate_SourcePrimaryRequiresHighTextCoverage(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "The quick brown fox jumps over the lazy dog in Italy.",
			WordCount: 10,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-source-primary"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:        "en",
		SourceText:      "The quick brown fox jumps over the lazy dog in Italy.",
		TargetWords:     10,
		GroundingPolicy: scriptpkg.GroundingPolicySourcePrimary,
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err != nil {
		t.Fatalf("expected quality gate to pass, got error: %v", err)
	}
	if !quality.Passed {
		t.Fatalf("expected quality.Passed=true for source_primary with high coverage")
	}
}

func TestEvaluateQualityGate_SourcePrimaryFailsLowTextCoverage(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "A completely different story about space travel and aliens.",
			WordCount: 9,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-source-primary-fail"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:        "en",
		SourceText:      "The quick brown fox jumps over the lazy dog.",
		GroundingPolicy: scriptpkg.GroundingPolicySourcePrimary,
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for source_primary with low text coverage")
	}
	if quality.Passed {
		t.Fatal("expected quality.Passed=false")
	}
}

func TestEvaluateQualityGate_ClipsPrimaryPassesLowTextCoverageWithFullClipBinding(t *testing.T) {
	// Generated text shares enough tokens with source_text to clear
	// the 0.40 source coverage threshold for clips_primary.
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "The quick brown fox jumps over the lazy dog in Italy.",
			WordCount: 10,
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{
						Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "clip-1"}},
					},
				},
			},
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-clips-primary"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:        "en",
		SourceText:      "The quick brown fox jumps over the lazy dog in Italy.",
		GroundingPolicy: scriptpkg.GroundingPolicyClipsPrimary,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-1"},
		},
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err != nil {
		t.Fatalf("expected quality gate to pass for clips_primary with full clip binding, got error: %v", err)
	}
	if !quality.Passed {
		t.Fatalf("expected quality.Passed=true")
	}
}

func TestEvaluateQualityGate_BalancedFailsWhenClipCoverageLow(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      "The quick brown fox jumps over the lazy dog.",
			WordCount: 9,
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{
						Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "clip-1"}},
					},
				},
			},
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "item-balanced-fail"}
	plan := scriptpkg.ResolvedGenerationPlan{
		Language:        "en",
		SourceText:      "The quick brown fox jumps over the lazy dog.",
		GroundingPolicy: scriptpkg.GroundingPolicyBalanced,
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"clip-1", "clip-2", "clip-3"},
		},
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatal("expected quality gate to fail for balanced with low clip coverage")
	}
	if quality.Passed {
		t.Fatal("expected quality.Passed=false")
	}
}

func TestComputeClipEvidenceCoverage(t *testing.T) {
	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			SpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{
					{Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "c1"}}},
					{Bindings: scriptpkg.SceneBindings{Clip: &scriptpkg.ClipBinding{ClipID: "c2"}}},
				},
			},
		},
	}
	plan := scriptpkg.ResolvedGenerationPlan{
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs: []string{"c1", "c2", "c3"},
		},
	}
	coverage := computeClipEvidenceCoverage(result, plan)
	if coverage != 2.0/3.0 {
		t.Errorf("coverage=%.2f want %.2f", coverage, 2.0/3.0)
	}
}

// TestEvaluateQualityGate_SingleSegmentCoveragePasses pins the generic
// default threshold without a scenario-specific relaxation.
func TestEvaluateQualityGate_SingleSegmentModerateCoveragePasses(t *testing.T) {
	const generated = "Sugar Ray Robinson boxer American career investments legacy heritage"
	const source = "Sugar Ray Robinson boxer American career investments legacy heritage"

	coverage := computeSourceTextCoverage(generated, source)
	if coverage < defaultMinSourceTextCoverage {
		t.Fatalf("FIXTURE INVALID: coverage=%.3f below new default %.3f; pick richer source text",
			coverage, defaultMinSourceTextCoverage)
	}

	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      generated,
			WordCount: 9,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "single-segment-doc"}
	plan := scriptpkg.ResolvedGenerationPlan{
		SourceText:  source,
		TargetWords: 10,
		SingleScene: true,
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err != nil {
		t.Fatalf("expected single-segment documentary gate pass at default coverage (coverage=%.3f, threshold=%.3f), got error: %v",
			coverage, defaultMinSourceTextCoverage, err)
	}
	if quality == nil {
		t.Fatal("expected quality to be populated")
	}
	if !quality.Passed {
		t.Fatalf("expected quality.Passed=true for single-segment documentary, got false (quality=%+v)", quality)
	}
}

// TestEvaluateQualityGate_SingleSegmentSubThresholdFails is the
// negative regression: a single-segment documentary whose prose
// shares none of its non-stop vocabulary with the source (coverage
// 0.0, below the 0.40 default) MUST fail regardless of SingleScene.
func TestEvaluateQualityGate_SingleSegmentSubThresholdFails(t *testing.T) {
	const generated = "space travel aliens unknown planet"
	const source = "Sugar Ray Robinson boxer American career"

	coverage := computeSourceTextCoverage(generated, source)
	if coverage >= defaultMinSourceTextCoverage {
		t.Skipf("FIXTURE skipped: coverage=%.3f no longer below default threshold %.3f; cannot exercise negative path",
			coverage, defaultMinSourceTextCoverage)
	}

	result := &scriptpkg.GenerationResult{
		Output: scriptpkg.ScriptOutput{
			Text:      generated,
			WordCount: 9,
		},
	}
	item := scriptpkg.GenerationItemV2{ID: "single-segment-default"}
	plan := scriptpkg.ResolvedGenerationPlan{
		SourceText:  source,
		TargetWords: 10,
		SingleScene: true,
	}

	quality, err := evaluateQualityGate(result, item, plan)
	if err == nil {
		t.Fatalf("expected gate FAIL when coverage (%.3f) is below default threshold %.3f, got nil error",
			coverage, defaultMinSourceTextCoverage)
	}
	if quality == nil {
		t.Fatal("expected quality to be populated on failure")
	}
	if quality.Passed {
		t.Fatal("expected quality.Passed=false when default minimum applies")
	}
}
