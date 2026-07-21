// Package mediamemory — ranker.go is the canonical SSOT for the
// ranking formula and its weights.
//
// godlike/06 SSOT (Phase 1 of the architecture doc, section 11):
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
// Mandatory pre-rank filters (godlike/07):
//
//   - licenza valida
//   - media disponibile
//   - durata valida
//   - formato supportato
//   - aspect ratio compatibile
//   - nessun file corrotto
//   - nessun duplicato
//
// godlike/06 SSOT (extension point): new ranking modes plug in via
// RankingStrategy registered in registry.go. The default weights
// below are the canonical Phase 1.x values; future tuning knobs
// (time-aware, channel-aware, ...) attach via RankerConfig and
// RankingStrategy.Rank(...).
package mediamemory

import "context"

// Ranker is the canonical port that turns candidate+context into a
// deterministic ordering. Concrete impl is DefaultRanker below.
type Ranker interface {
	// Score computes the final score for a (binding, candidate,
	// context) triple. Implementations MUST honour the canonical
	// formula unless a custom RankingStrategy is registered.
	Score(ctx context.Context, in RankingInput) (RankingOutput, error)

	// Filter applies the mandatory pre-rank filters (license,
	// availability, duration, format, ...) and REMOVES failing
	// candidates. The output slice is strictly a subset of `in`.
	Filter(ctx context.Context, in []FilteredCandidate) ([]FilteredCandidate, error)
}

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
type FilteredCandidate struct {
	Candidate      MediaCandidate
	IsDuplicate    bool
	MissingRights  bool
	AspectMismatch bool
	Contaminated   bool
}

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

// ── Default implementation (skeleton) ─────────────────────────────

// defaultRanker is the canonical implementation of Ranker. Phase
// 1.x wires the formula + filter; Phase 2 adds anti-repetition and
// diversity-aware re-ordering.
type defaultRanker struct {
	weights    DefaultRankWeights
	validators []RankingStrategy
	log        Logger
}

// NewDefaultRanker returns a default ranker using the canonical
// weights. validators register on top, MAY augment (NOT replace)
// the canonical formula until the next version bump.
func NewDefaultRanker(validators []RankingStrategy, log Logger) *defaultRanker {
	if log == nil {
		log = NoopLogger()
	}
	return &defaultRanker{
		weights:    Weights(),
		validators: validators,
		log:        log,
	}
}

// Compile-time assertion: defaultRanker satisfies Ranker.
var _ Ranker = (*defaultRanker)(nil)

// Score computes the canonical formula. Phase 1.x: skeleton stub;
// Phase 2 wires the validator chain.
func (r *defaultRanker) Score(_ context.Context, _ RankingInput) (RankingOutput, error) {
	return RankingOutput{}, errNotImplemented("mediamemory: defaultRanker.Score not yet implemented (Phase 1.x)")
}

// Filter applies the seven mandatory gates (license, availability,
// duration, format, aspect, corrupt-detection, dedup).
//
// godlike/07 NO-FAKE-AVAILABILITY: this stub returns a fail-closed
// envelope rather than a silent identity pass-through. A naive
// "return in, nil" would falsely advertise that gates ran; callers
// must short-circuit on errNotImplemented (Phase 2 fills the per-
// gate hooks).
func (r *defaultRanker) Filter(_ context.Context, _ []FilteredCandidate) ([]FilteredCandidate, error) {
	return nil, errNotImplemented("mediamemory: defaultRanker.Filter not yet implemented (Phase 2)")
}
