// Package scripts \u2014 clip_sampler_gates_test.go pins the FASE-8
// audit-gate contract: each of the 10 gates has a positive
// (gate passes when its criterion is satisfied) AND a negative
// (gate fails when its criterion is violated) sub-test. Per the
// user spec: "Test unitario per ogni gate (positivo+negativo)".
//
// godlike/06 SSOT: thresholds live in clip_sampler_gates.go; the
// tests reference the const names (not literal values) so a
// threshold tweak in one place updates both the gate and the
// golden expectations.
package usecase

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// helper: build a default-pass candidate with optional field
// overrides.
func passCandidate() ports.ClipSamplerCandidate {
	return ports.ClipSamplerCandidate{
		ClipID:              "clip-pass",
		Name:                "pass-candidate",
		Score:               0.95,
		Source:              "semantic",
		Transcript:          "Pacquiao and Broner traded punches at the center of the ring.",
		VisualSummary:       "The boxer and his opponent trading blows at center ring during the opening round.",
		MediaType:           "video",
		DurationMs:          6000,
		AnchorCoverageRatio: 0.75,
		DriveLink:           "https://drive.google.com/file/d/abc/view",
		Embedding:           []float32{1.0, 0.0, 0.0},
	}
}

func passSlot() scriptpkg.ClipSearchSlot {
	return scriptpkg.ClipSearchSlot{
		Ref:              "slot-1",
		Topic:            "Pacquiao Broner",
		TargetDurationMs: 6000,
	}
}

// \u2500\u2500 Gate 1: topic_relevance \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_TopicRelevance_Pass(t *testing.T) {
	g := topicRelevanceGate{}
	in := ClipSamplerGateInput{Candidate: passCandidate(), Slot: passSlot()}
	passed, reason := g.Evaluate(in)
	if !passed {
		t.Errorf("topic_relevance should pass when Transcript contains EVERY token of slot.Topic; got false (reason=%q)", reason)
	}
}

func TestGate_TopicRelevance_Fail(t *testing.T) {
	g := topicRelevanceGate{}
	bad := passCandidate()
	bad.Transcript = "totally unrelated content"
	bad.VisualSummary = "no relevant terms at all"
	slot := passSlot()
	slot.Topic = "boxing heavyweight championship"
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: bad, Slot: slot})
	if passed {
		t.Errorf("topic_relevance should fail on mismatched topic; got true")
	}
	if !strings.Contains(reason, "boxing heavyweight championship") {
		t.Errorf("reason should name the missing topic, got %q", reason)
	}
}

// TestGate_TopicRelevance_PartialOverlap_Fails pins the ALL-token
// (identity) rule that fixes the "Jackie Chan accepts a Tom Holland
// clip" regression: a candidate whose evidence mentions only the
// surname "chan" (in "the Chan era") must NOT satisfy a slot whose
// topic is "Jackie Chan". The old any-token rule let the partial
// overlap through; the gate now fails and names the missing token.
func TestGate_TopicRelevance_PartialOverlap_Fails(t *testing.T) {
	g := topicRelevanceGate{}
	c := passCandidate()
	c.Transcript = "Tom Holland talks about his latest interview and the Chan era of martial arts cinema."
	c.VisualSummary = "Tom Holland sits down for an interview."
	slot := passSlot()
	slot.Topic = "Jackie Chan"
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: slot})
	if passed {
		t.Fatalf("topic_relevance must reject a partial token overlap: candidate mentions only one of [jackie, chan]")
	}
	if !strings.Contains(reason, "jackie") {
		t.Errorf("reason should name the missing token 'jackie', got %q", reason)
	}
}

// TestGate_TopicRelevance_AllTokensRequired pins the ALL-token rule
// for descriptive multi-token topics: a candidate that corroborates
// every meaningful token passes; dropping any single token fails.
func TestGate_TopicRelevance_AllTokensRequired(t *testing.T) {
	g := topicRelevanceGate{}
	slot := passSlot()
	slot.Topic = "Pacquiao Broner recap"

	full := passCandidate()
	full.Transcript = "Pacquiao and Broner traded punches at the center of the ring after the recap."
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: full, Slot: slot})
	if !passed {
		t.Fatalf("all meaningful tokens present should pass, got %q", reason)
	}

	partial := passCandidate()
	partial.Transcript = "Pacquiao and Broner traded punches at the center of the ring."
	passed, reason = g.Evaluate(ClipSamplerGateInput{Candidate: partial, Slot: slot})
	if passed {
		t.Fatalf("dropping token 'recap' must fail the ALL-token rule")
	}
	if !strings.Contains(reason, "recap") {
		t.Errorf("reason should name the missing token 'recap', got %q", reason)
	}
}

// \u2500\u2500 Gate 2: source_anchor_coverage \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_SourceAnchorCoverage_Pass(t *testing.T) {
	g := sourceAnchorCoverageGate{}
	c := passCandidate()
	c.AnchorCoverageRatio = MinAnchorCoverageRatio + 0.01
	passed, _ := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if !passed {
		t.Errorf("source_anchor_coverage should pass when ratio >= MinAnchorCoverageRatio")
	}
}

func TestGate_SourceAnchorCoverage_Fail(t *testing.T) {
	g := sourceAnchorCoverageGate{}
	c := passCandidate()
	c.AnchorCoverageRatio = MinAnchorCoverageRatio - 0.10
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if passed {
		t.Errorf("source_anchor_coverage should fail when ratio < MinAnchorCoverageRatio")
	}
	if !strings.Contains(reason, "anchor coverage") {
		t.Errorf("reason should mention anchor coverage, got %q", reason)
	}
}

// \u2500\u2500 Gate 3: duration \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_Duration_Pass(t *testing.T) {
	g := durationGate{}
	c := passCandidate()
	c.DurationMs = 7000 // target=6000, floor=4800, ceiling=12000 \u2014 inside
	passed, _ := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if !passed {
		t.Errorf("duration should pass when DurationMs within [Floor, Ceiling]")
	}
}

func TestGate_Duration_Fail(t *testing.T) {
	g := durationGate{}
	c := passCandidate()
	c.DurationMs = 13000 // target=6000, ceiling=12000 \u2014 outside (over)
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if passed {
		t.Errorf("duration should fail when DurationMs > ceiling")
	}
	if !strings.Contains(reason, "ceiling") {
		t.Errorf("reason should mention ceiling, got %q", reason)
	}
}

// \u2500\u2500 Gate 4: diversity \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_Diversity_Pass(t *testing.T) {
	g := diversityGate{}
	c := passCandidate()
	c.Embedding = []float32{1.0, 0.0, 0.0} // orthogonal to previous
	prev := []scriptpkg.SlotClipBinding{
		{ClipID: "prev", Embedding: []float32{0.0, 1.0, 0.0}},
	}
	passed, _ := g.Evaluate(ClipSamplerGateInput{
		Candidate: c, Slot: passSlot(), PreviousSelections: prev,
	})
	if !passed {
		t.Errorf("diversity should pass when cosine < DiversityMaxCosine")
	}
}

func TestGate_Diversity_Fail(t *testing.T) {
	g := diversityGate{}
	c := passCandidate()
	c.Embedding = []float32{1.0, 0.0, 0.0}
	prev := []scriptpkg.SlotClipBinding{
		{ClipID: "prev", Embedding: []float32{1.0, 0.0, 0.0}}, // identical
	}
	passed, reason := g.Evaluate(ClipSamplerGateInput{
		Candidate: c, Slot: passSlot(), PreviousSelections: prev,
	})
	if passed {
		t.Errorf("diversity should fail when cosine >= DiversityMaxCosine")
	}
	if !strings.Contains(reason, "cosine=1.00") {
		t.Errorf("reason should report the cosine value, got %q", reason)
	}
}

// \u2500\u2500 Gate 5: chronological_order \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_ChronologicalOrder_Pass(t *testing.T) {
	g := chronologicalOrderGate{}
	c := passCandidate()
	c.SourceAnchor = &scriptpkg.SourceAnchor{StartOffset: 200, EndOffset: 400}
	prev := []scriptpkg.SlotClipBinding{
		{ClipID: "prev", SourceAnchor: &scriptpkg.SourceAnchor{StartOffset: 100, EndOffset: 200}},
	}
	passed, _ := g.Evaluate(ClipSamplerGateInput{
		Candidate: c, Slot: passSlot(), PreviousSelections: prev,
	})
	if !passed {
		t.Errorf("chronological_order should pass when StartOffset >= last")
	}
}

func TestGate_ChronologicalOrder_Fail(t *testing.T) {
	g := chronologicalOrderGate{}
	c := passCandidate()
	c.SourceAnchor = &scriptpkg.SourceAnchor{StartOffset: 50, EndOffset: 100}
	prev := []scriptpkg.SlotClipBinding{
		{ClipID: "prev", SourceAnchor: &scriptpkg.SourceAnchor{StartOffset: 100, EndOffset: 200}},
	}
	passed, reason := g.Evaluate(ClipSamplerGateInput{
		Candidate: c, Slot: passSlot(), PreviousSelections: prev,
	})
	if passed {
		t.Errorf("chronological_order should fail when StartOffset < last")
	}
	if !strings.Contains(reason, "backwards narrative order") {
		t.Errorf("reason should mention backwards order, got %q", reason)
	}
}

// \u2500\u2500 Gate 6: quality \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_Quality_Pass(t *testing.T) {
	g := qualityGate{}
	c := passCandidate()
	c.Score = MinQualityScore + 0.01
	passed, _ := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if !passed {
		t.Errorf("quality should pass when Score >= MinQualityScore")
	}
}

func TestGate_Quality_InclusiveBoundaryToleratesFloatRepresentation(t *testing.T) {
	g := qualityGate{}
	c := passCandidate()
	c.Score = MinQualityScore - (qualityScoreEpsilon / 2)
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if !passed {
		t.Fatalf("score at the inclusive threshold boundary should pass, got %q", reason)
	}
}

func TestGate_Quality_Fail(t *testing.T) {
	g := qualityGate{}
	c := passCandidate()
	c.Score = MinQualityScore - 0.10
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if passed {
		t.Errorf("quality should fail when Score < MinQualityScore")
	}
	if !strings.Contains(reason, "quality floor") {
		t.Errorf("reason should mention quality floor, got %q", reason)
	}
}

// \u2500\u2500 Gate 7: availability \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_Availability_Pass(t *testing.T) {
	g := availabilityGate{}
	c := passCandidate()
	c.AvailableByIngest = false
	c.DriveLink = "https://drive.google.com/file/d/abc/view"
	passed, _ := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if !passed {
		t.Errorf("availability should pass when DriveLink is set")
	}
}

func TestGate_Availability_Fail(t *testing.T) {
	g := availabilityGate{}
	c := passCandidate()
	c.DriveLink = ""
	c.AvailableByIngest = false
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if passed {
		t.Errorf("availability should fail with no DriveLink and not AvailableByIngest")
	}
	if !strings.Contains(reason, "DriveLink") {
		t.Errorf("reason should mention DriveLink, got %q", reason)
	}
}

// \u2500\u2500 Gate 8: no_duplicates \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_NoDuplicates_Pass(t *testing.T) {
	g := noDuplicatesAcrossSlotsGate{}
	c := passCandidate()
	prev := []scriptpkg.SlotClipBinding{{ClipID: "other"}}
	passed, _ := g.Evaluate(ClipSamplerGateInput{
		Candidate: c, Slot: passSlot(), PreviousSelections: prev,
	})
	if !passed {
		t.Errorf("no_duplicates should pass when ClipID is unique")
	}
}

func TestGate_NoDuplicates_Fail(t *testing.T) {
	g := noDuplicatesAcrossSlotsGate{}
	c := passCandidate()
	c.ClipID = "dup-id"
	prev := []scriptpkg.SlotClipBinding{{ClipID: "dup-id"}}
	passed, reason := g.Evaluate(ClipSamplerGateInput{
		Candidate: c, Slot: passSlot(), PreviousSelections: prev,
	})
	if passed {
		t.Errorf("no_duplicates should fail when ClipID collides with previous")
	}
	if !strings.Contains(reason, "dup-id") {
		t.Errorf("reason should name the colliding clip id, got %q", reason)
	}
}

// \u2500\u2500 Gate 9: transcript_visual_summary_present \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_TranscriptVisualSummary_Pass(t *testing.T) {
	g := transcriptVisualSummaryPresentGate{}
	c := passCandidate() // Transcript meets MinTranscriptWords=10; VisualSummary 30 runes pass
	passed, _ := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if !passed {
		t.Errorf("transcript_visual_summary_present should pass on populated fields")
	}
}

func TestGate_TranscriptVisualSummary_Fail(t *testing.T) {
	g := transcriptVisualSummaryPresentGate{}
	c := passCandidate()
	c.Transcript = "too few words" // 3 words < 10
	c.VisualSummary = "tiny"       // 4 runes < 20
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if passed {
		t.Errorf("transcript_visual_summary_present should fail on sparse fields")
	}
	if !strings.Contains(reason, "words=3") {
		t.Errorf("reason should report actual word count, got %q", reason)
	}
}

func TestGate_TranscriptVisualSummary_PassesDescriptionWithoutTranscript(t *testing.T) {
	g := transcriptVisualSummaryPresentGate{}
	c := passCandidate()
	c.Transcript = ""
	c.VisualSummary = "A detailed comedy interview at a round table"
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if !passed {
		t.Fatalf("description/summary should satisfy evidence gate without transcript: %s", reason)
	}
}

// \u2500\u2500 Gate 10: format_compatible \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500

func TestGate_FormatCompatible_Pass(t *testing.T) {
	g := formatCompatibleGate{}
	c := passCandidate()
	c.MediaType = "video"
	passed, _ := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if !passed {
		t.Errorf("format_compatible should pass when MediaType is set")
	}
}

func TestGate_FormatCompatible_Fail(t *testing.T) {
	g := formatCompatibleGate{}
	c := passCandidate()
	c.MediaType = ""
	passed, reason := g.Evaluate(ClipSamplerGateInput{Candidate: c, Slot: passSlot()})
	if passed {
		t.Errorf("format_compatible should fail when MediaType is empty")
	}
	if !strings.Contains(reason, "MediaType is empty") {
		t.Errorf("reason should explain empty MediaType, got %q", reason)
	}
}
