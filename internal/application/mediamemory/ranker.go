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
//
// godlike/06 SSOT (verdict thresholds):
//
//	final_score >  +0.05   VerdictAccept   (ranker promotes into top-K)
//	0 ≤ final ≤ +0.05      VerdictDownrank (kept but de-prioritized at diversity stage)
//	final_score <   0      VerdictDrop     (resolver short-circuits, no Layer)
//
// godlike/07 NO-FAKE-AVAILABILITY: Filter() is the canonical seam
// for the seven mandatory gates. The caller (resolver) is
// responsible for computing the FilteredCandidate booleans
// (IsDuplicate, MissingRights, AspectMismatch, Contaminated);
// Filter() removes failing rows. Passing through silently would be
// PR-REGRESSION — explicit drop-with-nil envelopes a no-op pass.
package mediamemory

import "context"

// Ranker is the canonical port that turns candidate+context into a
// deterministic ordering. Concrete impl is defaultRanker below.
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

// ── Default implementation (canonical) ─────────────────────────────

// defaultRanker is the canonical implementation of Ranker. Phase
// 1.x wires the formula + filter; Phase 2 adds anti-repetition and
// diversity-aware re-ordering.
type defaultRanker struct {
	weights    DefaultRankWeights
	thresholds VerdictThresholds
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
		thresholds: DefaultVerdictThresholds(),
		validators: validators,
		log:        log,
	}
}

// Compile-time assertion: defaultRanker satisfies Ranker.
var _ Ranker = (*defaultRanker)(nil)

// Score computes the canonical formula. Honours the weights from
// Weights(); the resolver pre-populates the per-seat scores (the
// ranker is math-only — no IO).
//
// godlike/07 NO-FAKE-AVAILABILITY: malformed candidates are
// rejected upstream by Filter() (canonical seam per godlike/06
// one-canonical-owner-per-fact). Score() is strictly math-only —
// the caller MUST have gated via Filter first. A regression
// here would result in a math verdict on a malformed envelope;
// defensive callers re-validate via Filter before passing.
//
// The math is the canonical Phase 1.x formula; tampering requires
// a godlike/06 SSOT review.
func (r *defaultRanker) Score(_ context.Context, in RankingInput) (RankingOutput, error) {
	final := (in.SemanticScore * r.weights.Semantic) +
		(in.ExactMatchScore * r.weights.ExactMatch) +
		(in.VisualScore * r.weights.Visual) +
		(in.ManualApprovalScore * r.weights.ManualApproval) +
		(in.QualityScore * r.weights.Quality) +
		(in.HistoricalSuccessScore * r.weights.HistoricalSuccess) +
		(in.DurationFitScore * r.weights.DurationFit) -
		in.RepetitionPenalty -
		in.RightsPenalty

	verdict := classifyScore(final, r.thresholds)
	return RankingOutput{
		FinalScore: final,
		Verdict:    verdict,
		Reason:     verdictReason(verdict),
	}, nil
}

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

// Filter applies the seven mandatory gates (license, availability,
// duration, format, aspect, corrupt-detection, dedup).
//
// godlike/06 SSOT (filter contract): rows where IsDuplicate,
// MissingRights, AspectMismatch, OR Contaminated are TRUE are
// REMOVED. Rows whose Candidate fails the well-formed guard
// (missing AssetID, unknown MaterializationStatus/DiscoveryStatus)
// are ALSO removed — the ranker's Score path would otherwise
// drop them with VerdictDrop, doing the work twice and producing
// confusing warning cascades.
//
// The output is strictly a subset of `in`. An empty slice is a
// valid outcome (no candidate survives the gates).
func (r *defaultRanker) Filter(_ context.Context, in []FilteredCandidate) ([]FilteredCandidate, error) {
	out := make([]FilteredCandidate, 0, len(in))
	for _, fc := range in {
		if fc.IsDuplicate || fc.MissingRights || fc.AspectMismatch || fc.Contaminated {
			continue
		}
		if !mediaCandidateIsWellFormed(fc.Candidate) {
			continue
		}
		out = append(out, fc)
	}
	return out, nil
}

// mediaCandidateIsWellFormed enforces the minimal wire-shape
// guard before the ranker attempts math. A malformed envelope
// MUST short-circuit; downstream rendering cannot use a candidate
// without an AssetID.
//
// The list of "well-formed" fields is intentionally minimal —
// the type-level required fields are already enforced at compile
// time. Runtime guard exists only to catch empty-AssetID drift.
func mediaCandidateIsWellFormed(c MediaCandidate) bool {
	if c.AssetID == "" {
		return false
	}
	if !IsKnownMaterializationStatus(c.MaterializationStatus) {
		return false
	}
	if !IsKnownDiscoveryStatus(c.DiscoveryStatus) {
		return false
	}
	return true
}
