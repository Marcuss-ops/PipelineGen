// Package scripts \u2014 clip_sampler_audit_test.go is the FASE-8
// audit-trail integration test. The sampler MUST:
//   - evaluate ALL 10 gates per candidate (not just first failing),
//   - write a GateProvenanceRecord for EVERY evaluation,
//   - emit Provenance with the records in canonical order
//     (candidate iteration order, then canonical 10-gate order),
//   - drop candidates that fail any gate; emit candidates that
//     pass every gate.
package usecase

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// auditSlot returns a slot configured to make every positive-case
// gate's criterion trivially satisfiable.
func auditSlot() scriptpkg.ClipSearchSlot {
	return scriptpkg.ClipSearchSlot{
		Ref:              "slot-audit",
		Topic:            "audit-topic",
		TargetDurationMs: 6000,
	}
}

// auditCandidate returns a candidate that should pass every gate.
func auditCandidate(id string) ports.ClipSamplerCandidate {
	return ports.ClipSamplerCandidate{
		ClipID:              id,
		Name:                id + "-name",
		Score:               0.95,
		Source:              "semantic",
		Transcript:          "audit-topic content with sufficient word count and detailed references inside.",
		VisualSummary:       "The audit-topic scene rendered with sufficient rune length for the visual summary text.",
		MediaType:           "video",
		DurationMs:          6000, // exactly target \u2014 in [Floor=4800, Ceiling=12000]
		AnchorCoverageRatio: 0.80,
		DriveLink:           "https://drive.google.com/file/d/abc/view",
		Embedding:           []float32{0.0, 1.0, 0.0},
	}
}

// TestAudit_AllGatesPass_AccumulatesCleanly verifies that a single
// candidate passing all 10 gates produces 10 records (one per
// gate) and the candidate lands in the result with full Provenance.
func TestAudit_AllGatesPass_AccumulatesCleanly(t *testing.T) {
	sampler := NewDefaultClipSampler()
	req := ports.ClipSamplerRequest{
		SlotRef:       "slot-audit",
		Limit:         1,
		Slot:          auditSlot(),
		CallingSource: ClipSamplerCallerCatalog,
		SourceType:    scriptpkg.SourceCatalog,
	}
	cands := []ports.ClipSamplerCandidate{auditCandidate("clip-audit-1")}

	res, err := sampler.Select(req, cands)
	if err != nil {
		t.Fatalf("expected nil error (all gates pass): %v", err)
	}
	if len(res.ClipIDs) != 1 || res.ClipIDs[0] != "clip-audit-1" {
		t.Fatalf("expected [clip-audit-1], got %v", res.ClipIDs)
	}
	if len(res.Provenance.Records) != 11 {
		t.Fatalf("expected 11 Provenance records (one per gate), got %d", len(res.Provenance.Records))
	}
	// Verify all 11 gate names appear in canonical order.
	expectedGateNames := []string{
		"topic_relevance", "source_anchor_coverage", "duration",
		"diversity", "chronological_order", "quality",
		"availability", "no_duplicates", "transcript_visual_summary_present",
		"format_compatible", "subtitle_ready",
	}
	for i, rec := range res.Provenance.Records {
		if rec.GateName != expectedGateNames[i] {
			t.Errorf("Provenance[%d] GateName: want %q got %q", i, expectedGateNames[i], rec.GateName)
		}
		if !rec.Passed {
			t.Errorf("Provenance[%d] expected Passed=true, got reason=%q", i, rec.Reason)
		}
		if rec.CandidateID != "clip-audit-1" {
			t.Errorf("Provenance[%d] CandidateID: want clip-audit-1 got %q", i, rec.CandidateID)
		}
		if rec.SlotRef != "slot-audit" {
			t.Errorf("Provenance[%d] SlotRef: want slot-audit got %q", i, rec.SlotRef)
		}
	}
}

// TestAudit_AnyGateFails_RecordsButDrops verifies the audit-trail
// invariant: every gate's evaluation is recorded, even when the
// candidate fails \u2014 but the candidate itself is NOT in the result.
func TestDefaultGatesCanonicalOrder(t *testing.T) {
	// godlike/06 SSOT: the 10-gate audit contract names the
	// canonical evaluation order. This test pins the order so a
	// future PR reordering defaultGates() cannot silently update
	// the audit-test fixture in lock-step — the order is now
	// independently observable from the canonical name list.
	got := []string{}
	for _, g := range defaultGates() {
		got = append(got, g.Name())
	}
	want := []string{
		"topic_relevance", "source_anchor_coverage", "duration",
		"diversity", "chronological_order", "quality",
		"availability", "no_duplicates", "transcript_visual_summary_present",
		"format_compatible", "subtitle_ready",
	}
	if len(got) != len(want) {
		t.Fatalf("defaultGates count: want %d got %d", len(want), len(got))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("defaultGates[%d]: want %q got %q", i, want[i], got[i])
		}
	}
}

func TestAudit_AnyGateFails_RecordsButDrops(t *testing.T) {
	sampler := NewDefaultClipSampler()
	req := ports.ClipSamplerRequest{
		SlotRef:       "slot-audit",
		Limit:         1,
		Slot:          auditSlot(),
		CallingSource: ClipSamplerCallerCatalog,
		SourceType:    scriptpkg.SourceCatalog,
	}
	// Candidate fails transcript_visual_summary_present (1-word
	// Transcript < MinTranscriptWords=10; 4-rune VisualSummary <
	// MinVisualSummaryLength=20) AND fails quality (low
	// Score=0.10 < MinQualityScore=0.50). Threshold constants
	// live in clip_sampler_gates.go; this fixture's literals are
	// intentional but not arbitrary — they sit just below each
	// gate's floor so the audit row is unambiguous on replay.
	bad := auditCandidate("clip-bad-1")
	bad.Transcript = "short"
	bad.VisualSummary = "tiny"
	bad.Score = 0.10

	res, err := sampler.Select(req, []ports.ClipSamplerCandidate{bad})
	if err != nil {
		t.Fatalf("expected nil error (drop + record): %v", err)
	}
	if len(res.ClipIDs) != 0 {
		t.Fatalf("candidate failing gates should be dropped, got %v", res.ClipIDs)
	}
	if len(res.Provenance.Records) != 11 {
		t.Fatalf("expected 11 records (full audit trail even on fail), got %d", len(res.Provenance.Records))
	}
	// Spot-check the negative outcomes: at least 2 gates failed.
	failed := 0
	for _, rec := range res.Provenance.Records {
		if !rec.Passed {
			failed++
		}
	}
	if failed < 2 {
		t.Errorf("expected at least 2 failing gates (quality + transcript-visual-summary), got %d", failed)
	}
	// Spot-check no_duplicates gate passed with empty PreviousSelections.
	for _, rec := range res.Provenance.Records {
		if rec.GateName == "no_duplicates" && !rec.Passed {
			t.Errorf("no_duplicates should pass with empty PreviousSelections, got reason=%q", rec.Reason)
		}
	}
}
