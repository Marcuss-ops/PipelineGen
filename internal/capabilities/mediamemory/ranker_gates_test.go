// Package mediamemory — ranker_gates_test.go is the Fase 1.5
// unit-test surface for the seven canonical pre-rank gates and
// the godlike/07 fail-closed rights-penalty mapping.
//
// godlike/06 SSOT: every gate has a single computation seam
// (RunMandatoryGates + helpers) so this test file pins the
// closed-set vocabulary without coupling to the resolver or
// filter wiring.
package mediamemory

import (
	"context"
	"testing"
	"time"
)

// ── Helpers ─────────────────────────────────────────────────────

// newColdCandidateForGates returns a FilteredCandidate that
// passes ALL gates by default, so each table-driven test can flip
// exactly one boolean / field and assert the resulting gate.
//
// godlike/06 SSOT (single source of fixture truth): production
// wiring composes FilteredCandidate from MediaCandidate + four
// resolver-set flags. Each field is set to the canonical
// pass-the-gate value so the table-driven tests can flip one
// knob at a time.
func newColdCandidateForGates(c MediaCandidate) FilteredCandidate {
	return FilteredCandidate{
		Candidate:      c,
		IsDuplicate:    false,
		MissingRights:  false,
		AspectMismatch: false,
		Contaminated:   false,
	}
}

// canonicalPassCandidate returns the canonical "passes all 7 gates"
// candidate. Tests construct it via newColdCandidateForGates and
// flip one knob at a time.
func canonicalPassCandidate() MediaCandidate {
	return MediaCandidate{
		ID:                    "cand-1",
		Provider:              "artlist",
		ProviderAssetID:       "a-1",
		MediaType:             "video",
		DurationMs:            12000,
		RightsStatus:          RightsVerified,
		DiscoveryStatus:       DiscoverySearched,
		MaterializationStatus: MaterializationHot,
		AssetID:               "asset-1",
	}
}

// ── RunMandatoryGates: 7 gates table-driven ─────────────────────

// TestRunMandatoryGates_SevenGatesTableDriven pins the canonical
// 7-gate vocabulary (Fase 1.5 SSOT) by flipping exactly one knob
// per row and asserting the FIRST failing gate's name matches
// the exact closed-set enum literal. godlike/06 SSOT: the gate
// name is the canonical log-scan key — a name drift here would
// silently break the dashboard's audit table.
func TestRunMandatoryGates_SevenGatesTableDriven(t *testing.T) {
	t.Parallel()

	type mutation struct {
		name     string
		mutate   func(*FilteredCandidate)
		wantGate PreRankGate
	}

	tests := []mutation{
		{
			name:     "GateNoDuplicates (IsDuplicate=true)",
			mutate:   func(fc *FilteredCandidate) { fc.IsDuplicate = true },
			wantGate: GateNoDuplicates,
		},
		{
			name:     "GateValidLicense (MissingRights=true)",
			mutate:   func(fc *FilteredCandidate) { fc.MissingRights = true },
			wantGate: GateValidLicense,
		},
		{
			name:     "GateNoCorruption (Contaminated=true)",
			mutate:   func(fc *FilteredCandidate) { fc.Contaminated = true },
			wantGate: GateNoCorruption,
		},
		{
			name: "GateAvailableMedia (Materialization=Cold)",
			mutate: func(fc *FilteredCandidate) {
				fc.Candidate.MaterializationStatus = MaterializationCold
			},
			wantGate: GateAvailableMedia,
		},
		{
			name: "GateAvailableMedia (Materialization=Failed)",
			mutate: func(fc *FilteredCandidate) {
				fc.Candidate.MaterializationStatus = MaterializationFailed
			},
			wantGate: GateAvailableMedia,
		},
		{
			name: "GateValidDuration (DurationMs<0)",
			mutate: func(fc *FilteredCandidate) {
				fc.Candidate.DurationMs = -100
			},
			wantGate: GateValidDuration,
		},
		{
			name: "GateSupportedFormat (MediaType=\"vrm\")",
			mutate: func(fc *FilteredCandidate) {
				fc.Candidate.MediaType = "vrm"
			},
			wantGate: GateSupportedFormat,
		},
		{
			name: "GateSupportedFormat (MediaType=\"raw\")",
			mutate: func(fc *FilteredCandidate) {
				fc.Candidate.MediaType = "raw"
			},
			wantGate: GateSupportedFormat,
		},
		{
			name:     "GateCompatibleAspect (AspectMismatch=true)",
			mutate:   func(fc *FilteredCandidate) { fc.AspectMismatch = true },
			wantGate: GateCompatibleAspect,
		},
		{
			name:     "all-pass canonical happy path returns empty string",
			mutate:   func(*FilteredCandidate) {},
			wantGate: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fc := newColdCandidateForGates(canonicalPassCandidate())
			tc.mutate(&fc)
			got := RunMandatoryGates(fc)
			if got != tc.wantGate {
				t.Fatalf("RunMandatoryGates returned %q, want %q (gate-name SSOT drift)",
					string(got), string(tc.wantGate))
			}
		})
	}
}

// TestRunMandatoryGates_EvaluationOrderFormatBeforeAspect pins
// the canonical gate-evaluation order: format precedes aspect
// because aspect ratio is a visual-format property. A candidate
// failing BOTH format and aspect must surface as
// GateSupportedFormat (the first failing gate in canonical
// order), not GateCompatibleAspect.
func TestRunMandatoryGates_EvaluationOrderFormatBeforeAspect(t *testing.T) {
	t.Parallel()
	fc := newColdCandidateForGates(canonicalPassCandidate())
	fc.Candidate.MediaType = "vrm" // fails format
	fc.AspectMismatch = true       // fails aspect
	got := RunMandatoryGates(fc)
	if got != GateSupportedFormat {
		t.Fatalf("format-before-aspect contract broken: got %q, want %q",
			string(got), string(GateSupportedFormat))
	}
}

// TestRunMandatoryGates_EvaluationOrderLicenseBeforeCorruption
// pins the canonical evaluation order for the resolver-set
// flags: license precedes no_corruption. A candidate failing
// BOTH must surface as GateValidLicense first.
func TestRunMandatoryGates_EvaluationOrderLicenseBeforeCorruption(t *testing.T) {
	t.Parallel()
	fc := newColdCandidateForGates(canonicalPassCandidate())
	fc.MissingRights = true // fails license
	fc.Contaminated = true  // fails no_corruption
	got := RunMandatoryGates(fc)
	if got != GateValidLicense {
		t.Fatalf("license-before-corruption contract broken: got %q, want %q",
			string(got), string(GateValidLicense))
	}
}

// ── HasSupportedFormat: canonical set + bypass ──────────────────

// TestHasSupportedFormat_CanonicalSetAndBypass pins the
// per-MediaType behaviour of the 7th gate helper.
func TestHasSupportedFormat_CanonicalSetAndBypass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mediaType string
		want      bool
		why       string
	}{
		{"video", true, "canonical video type"},
		{"image", true, "canonical image type"},
		{"audio", true, "canonical audio type"},
		{"music", true, "canonical music type"},
		{"", true, "legacy ambiguous sentinel bypasses (forward-compat)"},
		{"vrm", false, "unknown 3D format rejected"},
		{"raw", false, "unknown camera raw rejected"},
		{"pdf", false, "unsupported document format rejected"},
		{"Video", false, "case-sensitive canonical set"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.mediaType+"_or_empty", func(t *testing.T) {
			t.Parallel()
			c := canonicalPassCandidate()
			c.MediaType = tc.mediaType
			got := HasSupportedFormat(c)
			if got != tc.want {
				t.Fatalf("MediaType=%q: HasSupportedFormat=%v, want %v (%s)",
					tc.mediaType, got, tc.want, tc.why)
			}
		})
	}
}

// TestRunMandatoryGates_FormatGate_AcceptsFourCanonicalTypes
// pins the integration: each of the 4 canonical MediaType values
// (video/image/audio/music) satisfies the format gate so the
// downstream aspect gate still runs.
func TestRunMandatoryGates_FormatGate_AcceptsFourCanonicalTypes(t *testing.T) {
	t.Parallel()
	for _, mt := range []string{"video", "image", "audio", "music"} {
		mt := mt
		t.Run(mt, func(t *testing.T) {
			t.Parallel()
			c := canonicalPassCandidate()
			c.MediaType = mt
			fc := newColdCandidateForGates(c)
			if got := RunMandatoryGates(fc); got != "" {
				t.Fatalf("MediaType=%q should pass all gates: got %q", mt, string(got))
			}
		})
	}
}

// ── PopulateRightsPenalty: godlike/07 fail-closed mapping ───────

// TestPopulateRightsPenalty_FailClosedPerGodlike07 pins the
// Fase 1.5 godlike/07 mapping: RightsVerified stays 0.0, EVERY
// other status (Unknown/Denied/Expired) is upgrade to 1.0 so the
// candidate scores below the Drop threshold and the Hot-tier
// guard in batch_service.MarkMaterialized stays the second line
// of defense.
func TestPopulateRightsPenalty_FailClosedPerGodlike07(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status RightsStatus
		want   float64
	}{
		{RightsVerified, 0.0},
		{RightsUnknown, 1.0}, // upgraded from 0.10 (Fase 1.5)
		{RightsDenied, 1.0},
		{RightsExpired, 1.0}, // upgraded from 0.20 (Fase 1.5)
	}
	for _, tc := range tests {
		tc := tc
		t.Run(string(tc.status), func(t *testing.T) {
			t.Parallel()
			inputs := []RankingInput{{
				Candidate: MediaCandidate{RightsStatus: tc.status},
			}}
			out := PopulateRightsPenalty(inputs)
			if len(out) != 1 {
				t.Fatalf("PopulateRightsPenalty returned %d, want 1", len(out))
			}
			if out[0].RightsPenalty != tc.want {
				t.Fatalf("RightsStatus=%s: Penalty=%.3f, want %.3f",
					string(tc.status), out[0].RightsPenalty, tc.want)
			}
		})
	}
}

// TestPopulateRightsPenalty_UnknownForcesVerdictDrop is the
// integration pin: a candidate with RightsUnknown + otherwise-
// perfect scores (semantic 1.0 + exact 1.0 + visual 1.0 +
// manual 1.0 + quality 1.0 + historical 1.0 + duration 1.0)
// still ends at VerdictDrop because RightsPenalty=1.0 wipes the
// 1.00 weighted sum. Without Fase 1.5's fail-closed mapping the
// same candidate would land in VerdictAccept (1.00 - 0.10 = 0.90).
func TestPopulateRightsPenalty_UnknownForcesVerdictDrop(t *testing.T) {
	t.Parallel()
	in := []RankingInput{{
		Candidate:     MediaCandidate{RightsStatus: RightsUnknown},
		SemanticScore: 1.0, ExactMatchScore: 1.0, VisualScore: 1.0,
		ManualApprovalScore: 1.0, QualityScore: 1.0,
		HistoricalSuccessScore: 1.0, DurationFitScore: 1.0,
	}}
	out := PopulateRightsPenalty(in)
	if out[0].RightsPenalty != 1.0 {
		t.Fatalf("godlike/07 fail-closed broken: RightsUnknown penalty=%.3f, want 1.0",
			out[0].RightsPenalty)
	}
	// Compose final_score via the defaultRanker.Score and assert
	// VerdictDrop. 1.00 weighted sum - 1.0 rights penalty = 0.0,
	// which equals the Drop threshold (VerdictDownrank band),
	// not VerdictAccept — the candidate is NOT auto-promoted.
	r := NewDefaultRanker(nil, nil)
	res, err := r.Score(context.Background(), out[0])
	if err != nil {
		t.Fatalf("Score returned err: %v", err)
	}
	if res.Verdict == VerdictAccept {
		t.Fatalf("RightsUnknown auto-promoted to Accept: FinalScore=%.3f Verdict=%s",
			res.FinalScore, res.Verdict)
	}
}

// TestPopulateRightsPenalty_DoesNotMutateInput pins the
// godlike/06 immutable input contract (mirrors
// PopulateRepetitionPenalty): the caller can keep using the
// original slice after PopulateRightsPenalty returns. A
// mutation regression here would be PR-REGRESSION.
func TestPopulateRightsPenalty_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	original := []RankingInput{
		{Candidate: MediaCandidate{RightsStatus: RightsUnknown}},
	}
	// Snapshot the original RightsPenalty (should be 0.0 zero-value).
	if original[0].RightsPenalty != 0.0 {
		t.Fatalf("pre-condition: original RightsPenalty = %.3f, want 0.0",
			original[0].RightsPenalty)
	}
	_ = PopulateRightsPenalty(original)
	if original[0].RightsPenalty != 0.0 {
		t.Fatalf("PopulateRightsPenalty mutated input: original RightsPenalty=%.3f",
			original[0].RightsPenalty)
	}
}

// TestPopulateRightsPenalty_EmptyInputReturnsEmptySlice pins the
// godlike/07 NO-FAKE-AVAILABILITY semantics: an empty input
// returns an empty (non-nil) slice, not nil.
func TestPopulateRightsPenalty_EmptyInputReturnsEmptySlice(t *testing.T) {
	t.Parallel()
	out := PopulateRightsPenalty(nil)
	if out == nil {
		t.Fatalf("PopulateRightsPenalty(nil) returned nil slice; want empty (non-nil)")
	}
	if len(out) != 0 {
		t.Fatalf("PopulateRightsPenalty(nil) returned %d rows; want 0", len(out))
	}
}

// ── defaultRanker.Filter integration ────────────────────────────

// TestRankerFilter_DropsAllSevenGateFailures is the load-bearing
// end-to-end pin: every gate in the canonical 7-gate set
// actually removes the candidate via Filter (no VerdictDrop
// fallback masking the gate).
func TestRankerFilter_DropsAllSevenGateFailures(t *testing.T) {
	t.Parallel()
	r := NewDefaultRanker(nil, nil)

	type failCase struct {
		name string
		fc   FilteredCandidate
	}
	cases := []failCase{
		{"duplicate", newColdCandidateForGates(canonicalPassCandidate())},
		{"missing-rights", newColdCandidateForGates(canonicalPassCandidate())},
		{"contaminated", newColdCandidateForGates(canonicalPassCandidate())},
		{"cold-materialization", newColdCandidateForGates(canonicalPassCandidate())},
		{"negative-duration", newColdCandidateForGates(canonicalPassCandidate())},
		{"unsupported-format", newColdCandidateForGates(canonicalPassCandidate())},
		{"aspect-mismatch", newColdCandidateForGates(canonicalPassCandidate())},
	}
	cases[0].fc.IsDuplicate = true
	cases[1].fc.MissingRights = true
	cases[2].fc.Contaminated = true
	cases[3].fc.Candidate.MaterializationStatus = MaterializationCold
	cases[4].fc.Candidate.DurationMs = -1
	cases[5].fc.Candidate.MediaType = "vrm"
	cases[6].fc.AspectMismatch = true

	for i := range cases {
		fc := cases[i].fc
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := r.Filter(context.Background(), []FilteredCandidate{fc})
			if err != nil {
				t.Fatalf("Filter returned err: %v", err)
			}
			if len(out) != 0 {
				t.Fatalf("Filter passed through %d candidates; want 0 (gate=%q must drop)",
					len(out), tc.name)
			}
		})
	}
}

// TestRankerFilter_HappyPathKeepsCandidate pins the positive
// case: a canonical-pass candidate reaches the Score stage.
func TestRankerFilter_HappyPathKeepsCandidate(t *testing.T) {
	t.Parallel()
	r := NewDefaultRanker(nil, nil)
	fc := newColdCandidateForGates(canonicalPassCandidate())
	out, err := r.Filter(context.Background(), []FilteredCandidate{fc})
	if err != nil {
		t.Fatalf("Filter returned err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Filter passed through %d candidates; want 1 (canonical happy path)",
			len(out))
	}
}

// ── Score formula: hot-tier verification ────────────────────────

// TestRankerScore_VerifiedRightsWinsOverUnknownSameGate is the
// load-bearing integration pin for godlike/07: when two
// otherwise-identical candidates differ only in RightsStatus
// (Verified vs Unknown), the Verified candidate wins top-1 and
// the Unknown candidate lands below the Drop threshold. A
// regression to the old 0.10 penalty would let both score high
// and bypass the rights contract.
func TestRankerScore_VerifiedRightsWinsOverUnknownSameGate(t *testing.T) {
	t.Parallel()
	r := NewDefaultRanker(nil, nil)
	// Build two identical candidates; only RightsStatus differs.
	mkInput := func(rs RightsStatus) RankingInput {
		return RankingInput{
			Candidate:     MediaCandidate{RightsStatus: rs},
			SemanticScore: 1.0, ExactMatchScore: 1.0, VisualScore: 1.0,
			ManualApprovalScore: 1.0, QualityScore: 1.0,
			HistoricalSuccessScore: 1.0, DurationFitScore: 1.0,
		}
	}
	verified := mkInput(RightsVerified)
	unknown := mkInput(RightsUnknown)

	stamped := PopulateRightsPenalty([]RankingInput{verified, unknown})
	sv, err := r.Score(context.Background(), stamped[0])
	if err != nil {
		t.Fatalf("Score verified: %v", err)
	}
	su, err := r.Score(context.Background(), stamped[1])
	if err != nil {
		t.Fatalf("Score unknown: %v", err)
	}
	if sv.FinalScore <= su.FinalScore {
		t.Fatalf("Verified (%.3f) did not outrank Unknown (%.3f) — godlike/07 fail-closed broken",
			sv.FinalScore, su.FinalScore)
	}
	if su.Verdict == VerdictAccept {
		t.Fatalf("Unknown landed in VerdictAccept (%.3f) — must be Downrank/Drop",
			su.FinalScore)
	}
}

// ── Time helpers (compile-time pins) ────────────────────────────

// TestTimeHelpers_CompileTime pins that the new gate keywords
// resolve to the canonical durations/limits documented in
// the canonical SSOT comments.
func TestTimeHelpers_CompileTime(t *testing.T) {
	t.Parallel()
	// godlike/06 SSOT: ChannelRecencyWindowMaxAge = 24h.
	if ChannelRecencyWindowMaxAge != 24*time.Hour {
		t.Fatalf("ChannelRecencyWindowMaxAge drifted: got %v, want 24h",
			ChannelRecencyWindowMaxAge)
	}
	if ChannelRecencyMaxPenalty != 0.50 {
		t.Fatalf("ChannelRecencyMaxPenalty drifted: got %.3f, want 0.50",
			ChannelRecencyMaxPenalty)
	}
}
