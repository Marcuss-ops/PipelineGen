// Package mediamemory — ranker_weights.go is the canonical home
// for the ranker's WEIGHT SURFACE: DefaultRankWeights + Weights()
// (the canonical Phase 1.x scoring formula weights), the
// VerdictThresholds + DefaultVerdictThresholds (closed-form
// verdict bands), and the math-only verdict classifier
// classifyScore + verdictReason (used by defaultRanker.Score).
//
// godlike/06 SSOT (single canonical home per concern): tuning
// lands via this file. ANY drift in the canonical weights is a
// godlike/06 SSOT review (typing of new knobs is allowed but
// the canonical defaults are immutable until the next version
// bump — bumping mirrors PhraseFingerprint so the cache
// invalidates cleanly).
//
// godlike/06 SSOT (canonical formula, Phase 1.x of section 11):
//
//	final_score = semantic_score     * 0.30
//	            + exact_match_score  * 0.20
//	            + visual_score       * 0.15
//	            + manual_approval_score * 0.15
//	            + quality_score      * 0.10
//	            + historical_success_score * 0.05
//	            + duration_fit_score * 0.05
//	            - repetition_penalty
//	            - rights_penalty
//
// godlike/06 SSOT (verdict thresholds, canonical Phase 1.x):
//
//	final_score >  +0.05   VerdictAccept   (ranker promotes into top-K)
//	0 ≤ final ≤ +0.05      VerdictDownrank (kept but de-prioritized at diversity stage)
//	final_score <   0      VerdictDrop     (resolver short-circuits, no Layer)
//
// File split ownership (godlike/06 SSOT):
//   - ranker.go               : Ranker port + defaultRanker + NewDefaultRanker + Score + Filter
//   - ranker_types.go         : RankingInput + RankingOutput + RankingVerdict + FilteredCandidate
//   - ranker_weights.go       : DefaultRankWeights + Weights() + VerdictThresholds + DefaultVerdictThresholds + classifyScore + verdictReason  ← this file
//   - ranker_gates.go         : PreRankGate + 7 gates + RunMandatoryGates + HasAvailableMedia/HasValidDuration/HasSupportedFormat/mediaCandidateIsWellFormed + ComputeDurationFit + PopulateRightsPenalty
//   - ranker_repetition.go    : AntiRepetitionHistoryLimit + 4 channel-recency/ceiling consts + DiversityFinalScoreDelta + RepetitionPenaltyWeights + DefaultRepetitionPenaltyWeights + PopulateRepetitionPenalty + PickTopFromRose + extractCandidateVideoID/ChannelID
package mediamemory

// ── Default weights (canonical) ─────────────────────────────────

// DefaultRankWeights is the canonical Phase 1.x weighting. Each
// field is the canonical value per the architecture doc section 11.
// ANY change requires a godlike/06 SSOT review (typing of new
// knobs is allowed, but the canonical defaults are immutable
// until the next version bump — bumping mirrors PhraseFingerprint
// so the cache invalidates cleanly).
type DefaultRankWeights struct {
	Semantic          float64 // 0.30
	ExactMatch        float64 // 0.20
	Visual            float64 // 0.15
	ManualApproval    float64 // 0.15
	Quality           float64 // 0.10
	HistoricalSuccess float64 // 0.05
	DurationFit       float64 // 0.05
}

// Weights returns the canonical Phase 1.x weights. Tests may
// construct a custom DefaultRankWeights struct in fixtures but
// production wiring reads ONLY from this function.
func Weights() DefaultRankWeights {
	return DefaultRankWeights{
		Semantic:          0.30,
		ExactMatch:        0.20,
		Visual:            0.15,
		ManualApproval:    0.15,
		Quality:           0.10,
		HistoricalSuccess: 0.05,
		DurationFit:       0.05,
	}
}

// VerdictThresholds bundles the canonical post-rank verdict
// thresholds. Open via struct for future tuning without changing
// the constants in Weights().
type VerdictThresholds struct {
	Accept float64 // > Accept → Accept
	Drop   float64 // < Drop   → Drop (between is Downrank)
}

// DefaultVerdictThresholds returns the canonical Phase 1.x
// thresholds: Accept > 0.05, Drop < 0.0 (downrank fills the gap).
func DefaultVerdictThresholds() VerdictThresholds {
	return VerdictThresholds{Accept: 0.05, Drop: 0.0}
}

// ── Verdict classifier (math-only) ───────────────────────────────

// classifyScore maps a final score to the canonical verdict via
// the supplied thresholds. Open function so tests + future
// per-strategy Rankers can plug their own thresholds in.
func classifyScore(final float64, t VerdictThresholds) RankingVerdict {
	switch {
	case final > t.Accept:
		return VerdictAccept
	case final < t.Drop:
		return VerdictDrop
	default:
		return VerdictDownrank
	}
}

// verdictReason returns the canonical godlike/06 reason string
// for a verdict. Surface stable for log scans + dashboard audit.
func verdictReason(v RankingVerdict) string {
	switch v {
	case VerdictAccept:
		return "score above accept threshold"
	case VerdictDownrank:
		return "score within downrank band"
	case VerdictDrop:
		return "score below drop threshold (penalty or malformed)"
	default:
		return "unknown verdict"
	}
}
