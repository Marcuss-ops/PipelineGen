// Package mediamemory — ranker_types.go is the canonical home
// for the ranker's input/output wire-shapes: RankingInput (per-
// candidate scoring surface produced by the resolver), RankingOutput
// (per-candidate verdict produced by the ranker), RankingVerdict
// (closed-set enum: Accept/Downrank/Drop), and FilteredCandidate
// (pre-rank input shape carrying resolver-set flags + the binding
// envelope for lossless projection).
//
// godlike/06 SSOT (single canonical home per concern): every
// input/output surface that crosses the ranker port boundary
// lives in this file so the wire-shape vocabulary is one
// grep-able home. Tests pin the canonical fields below; drift
// here surfaces as a ranker-side regression.
//
// godlike/06 SSOT (lossless binding projection): the
// FilteredCandidate.Binding field carries the originating
// MediaBinding alongside the MediaCandidate so the ranker can
// pull operator-curated scores (ManualScore / SemanticScore /
// QualityScore / SuccessScore) AND the binding window
// (StartMs / EndMs) without losing information at the
// projection boundary.
//
// File split ownership (godlike/06 SSOT):
//   - ranker.go               : Ranker port + defaultRanker + NewDefaultRanker + Score + Filter  ← canonical implementation
//   - ranker_types.go         : RankingInput + RankingOutput + RankingVerdict + FilteredCandidate  ← this file
//   - ranker_weights.go       : DefaultRankWeights + Weights() + VerdictThresholds + DefaultVerdictThresholds + classifyScore + verdictReason
//   - ranker_gates.go         : PreRankGate + 7 gates + RunMandatoryGates + HasAvailableMedia/HasValidDuration/HasSupportedFormat/mediaCandidateIsWellFormed + ComputeDurationFit + PopulateRightsPenalty
//   - ranker_repetition.go    : AntiRepetitionHistoryLimit + 4 channel-recency/ceiling consts + DiversityFinalScoreDelta + RepetitionPenaltyWeights + DefaultRepetitionPenaltyWeights + PopulateRepetitionPenalty + PickTopFromRose + extractCandidateVideoID/ChannelID
package mediamemory

// ── Inputs / outputs ──────────────────────────────────────────────

// RankingInput is the per-candidate scoring surface. Each seat is
// pre-computed by the resolver; the ranker composes the final
// score from them.
// values are in [0,1] except for the penalties which may push the
// final score negative (godlike/07 fail-closed: a sub-zero
// candidate is dropped at the diversity stage).
type RankingInput struct {
	Binding                MediaBinding
	Candidate              MediaCandidate
	SemanticScore          float64
	ExactMatchScore        float64
	VisualScore            float64
	ManualApprovalScore    float64
	QualityScore           float64
	HistoricalSuccessScore float64
	DurationFitScore       float64
	RepetitionPenalty      float64
	RightsPenalty          float64
}

// RankingOutput is the scored candidate envelope. The resolver
// consumes FinalScore + Verdict to build SceneVisualPlan layers.
type RankingOutput struct {
	FinalScore float64
	Verdict    RankingVerdict
	Reason     string
}

// RankingVerdict is the canonical post-rank verdict (godlike/06
// closed set). Verdict == Drop MUST short-circuit the resolver:
// no SceneVisualPlan layer for this candidate.
type RankingVerdict string

const (
	VerdictAccept   RankingVerdict = "accept"
	VerdictDownrank RankingVerdict = "downrank"
	VerdictDrop     RankingVerdict = "drop"
)

// FilteredCandidate is the pre-rank input shape. Filter() removes
// rows that fail the mandatory gates (license, duration, format,
// aspect, corrupt-detection, dedup).
//
// godlike/06 SSOT extension (binding envelope): the Binding field
// carries the originating binding alongside the candidate so the
// ranker can pull operator-curated scores (ManualScore,
// SemanticScore, QualityScore, SuccessScore) and the binding
// window (StartMs, EndMs) without losing information at the
// projection boundary.
type FilteredCandidate struct {
	Candidate      MediaCandidate
	Binding        MediaBinding
	IsDuplicate    bool
	MissingRights  bool
	AspectMismatch bool
	Contaminated   bool
}
