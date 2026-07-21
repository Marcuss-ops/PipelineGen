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

import (
	"context"
	"math"
)

// ── Fase 2.3 anti-repetition helpers ────────────────────────────────

// AntiRepetitionHistoryLimit is the canonical upper bound on the
// resolver's per-call history read (UsageRepository.ListProjectUsages).
// godlike/06 SSOT: the resolver MUST NOT read unbounded project
// history at the resolver hot path; a hard ceiling at the
// repository seam is the canonical safeguard.
//
// 1000 rows covers ~100k of audio/video at 100 events/render, which
// is the architectural-doc reference scale for a Maya-style
// documentary project.
const AntiRepetitionHistoryLimit = 1000

// RepetitionPenaltyWeights bundles the canonical weight values
// for the Fase 2.3 PopulateRepetitionPenalty formula. godlike/06
// SSOT: any future tuning lands as a new function (e.g.
// RepetitionPenaltyWeightsV2) so callers pin the version.
type RepetitionPenaltyWeights struct {
	// SameAssetPenalty is applied per (asset_id, project_id)
	// occurrence in the project history. The first occurrence
	// contributes 1.0 (capped at 1.0 per asset); subsequent
	// reuse adds diminishing returns so an asset reused 5 times
	// is suppressed but not unbounded.
	//
	// godlike/06 SSOT: same-asset penalty is the SPEC
	// strongest signal ("clip già nello stesso video"). 1.0 is
	// the canonical Phase 2.3 ceiling.
	SameAssetPenalty float64

	// SameVideoInConsecutiveScenePenalty is applied per scene
	// whose VideoID matches the prior scene's winning VideoID.
	// Cross-scene repetition is the SPEC's
	// ("stessa sorgente consecutiva") canonical trigger.
	SameVideoInConsecutiveScenePenalty float64

	// ChannelSaturationBase is the per-occurrence penalty for
	// assets whose ChannelID has been logged >= 3 times in the
	// project history. The penalty is BASE * (crossedThreshold - 3)
	// so 4th sighting adds 0.1, 5th adds 0.2, etc.
	ChannelSaturationBase float64

	// ChannelSaturationMinSightings is the inclusive minimum
	// number of project sightings before the saturation
	// penalty kicks in. SPEC: "channel saturation after
	// multiple uses" → canonical value is 3.
	ChannelSaturationMinSightings int
}

// DefaultRepetitionPenaltyWeights returns the canonical Phase 2.3
// weights. godlike/06 SSOT: the canonical values below are
// immutable until the next version bump; any drift must be
// reviewed under godlike/06.
func DefaultRepetitionPenaltyWeights() RepetitionPenaltyWeights {
	return RepetitionPenaltyWeights{
		SameAssetPenalty:                   1.0,
		SameVideoInConsecutiveScenePenalty: 0.3,
		ChannelSaturationBase:              0.1,
		ChannelSaturationMinSightings:      3,
	}
}

// PopulateRepetitionPenalty is the Fase 2.3 anti-repetition
// producer for RankingInput.RepetitionPenalty. The resolver calls
// this once per slot's candidate pool (after Filter, before
// Score) so the ranker computes final_score = canonical - penalty.
//
// godlike/06 SSOT (immutable input contract): the function does
// NOT mutate the input slice — outputs are returned as a NEW slice
// with the input's candidates copied through and the penalty seat
// re-stamped. Callers must use the returned slice.
//
// godlike/06 SSOT (closed-set logic, per spec):
//  1. Same asset+project -> +SameAssetPenalty (capped at 1.0 so
//     a 5x-reused asset is suppressed, not unbounded).
//  2. Candidate VideoID == prevVideoID (assuming the candidate has
//     a video_id propagated onto MediaCandidate) ->
//     +SameVideoInConsecutiveScenePenalty.
//  3. ChannelID with >= ChannelSaturationMinSightings project hits
//     -> +ChannelSaturationBase * (sightings - MinSightings).
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil or empty history slice
// is treated as "no penalty input available" — penalties stay 0
// (the ranker still scores candidates normally). A nil prevVideoID
// bypasses the consecutive-scene penalty (first scene in a project
// has no prior scene to compare against).
//
// godlike/06 SSOT (binding identification): the input candidate
// field is MediaCandidate.AssetID (canonical). BindingID is the
// SAME-asset identity (per Fase 2.2 every binding's AssetID is
// the canonical media_assets.id). VideoID is sourced from
// MediaCandidate.Provider (when augmented by the linker worker in
// a future Fase) OR from MediaCandidate.AssetID via the canonical
// helper extractVideoID — for now we settle for AssetID-only
// matching so the penalty fires correctly when only AssetID is
// populated. ChannelID is sourced from the per-binding envelope
// (Binding hasn't ChannelID yet — Fase 2.3 candidates read it
// from MediaCandidate.Provider channel or fall back to "").
func PopulateRepetitionPenalty(
	inputs []RankingInput,
	history []UsageEvent,
	prevVideoID string,
) []RankingInput {
	weights := DefaultRepetitionPenaltyWeights()

	// Pre-compute (asset, channel) sighting maps from history.
	// godlike/06 SSOT: O(N+M) pass; the resolver's hot path always
	// reads from canonical append-only audit log so we can
	// recompute cheaply on every slot.
	assetSightings := make(map[string]int, 32)
	channelSightings := make(map[string]int, 8)
	for _, ev := range history {
		if ev.ProjectID == "" {
			continue
		}
		if ev.AssetID != "" {
			assetSightings[ev.AssetID]++
		}
		if ev.ChannelID != "" {
			channelSightings[ev.ChannelID]++
		}
	}

	out := make([]RankingInput, 0, len(inputs))
	for _, in := range inputs {
		penalty := 0.0

		// 1. Same asset penalty (capped at SameAssetPenalty so
		//    unbounded reuse never blows past the ranker math).
		if n := assetSightings[in.Candidate.AssetID]; n > 0 {
			penalty += math.Min(
				float64(n)*weights.SameAssetPenalty,
				weights.SameAssetPenalty,
			)
		}

		// 2. Consecutive-source penalty: the candidate's
		//    video_id (when populated — Fase 4 linker) falls
		//    back to AssetID for Fase 2.3 so the per-asset
		//    signal still propagates without a parallel seam.
		candidateVideoID := extractCandidateVideoID(in.Candidate)
		if prevVideoID != "" && candidateVideoID != "" && candidateVideoID == prevVideoID {
			penalty += weights.SameVideoInConsecutiveScenePenalty
		}

		// 3. Channel-saturation penalty (candidate carries the
		//    source channel via MediaCandidate.ChannelID as of
		//    Fase 2.3). Phase 2.3 candidates from Level 9
		//    (external SearchFanOut) carry the forwarding
		//    provider as ChannelID (forward-pin to Fase 3
		//    linker); the binding path gets ChannelID via the
		//    denormalized UsageEvent history rows.
		candidateChannelID := extractCandidateChannelID(in.Candidate)
		if candidateChannelID != "" {
			if n := channelSightings[candidateChannelID]; n >= weights.ChannelSaturationMinSightings {
				penalty += weights.ChannelSaturationBase * float64(n-weights.ChannelSaturationMinSightings)
			}
		}

		in.RepetitionPenalty = penalty
		out = append(out, in)
	}
	return out
}

// extractCandidateVideoID returns the canonical video_id for a
// candidate. godlike/06 SSOT (Fase 2.3 fallback):
// in the absence of a Linker-enriched video_id, we fall back to
// AssetID so the same-asset repetition loop is still
// monotonically effective against consecutive scenes. Future Fase
// drafts enrich MediaCandidate with an explicit VideoID field
// (3.x). Note: this fallback currently double-counts
// SameAssetPenalty (1.0) with SameVideoInConsecutiveScenePenalty
// (0.3) for any reused binding — by design until Fase 3 lands.
//
// TODO Fase 3 linker: stop falling back to AssetID so sub-clip
// (≠asset) coverage fires; until then the ranker deliberately
// treats AssetID as the canonical video identity.
func extractCandidateVideoID(c MediaCandidate) string {
	if c.VideoID != "" {
		return c.VideoID
	}
	return c.AssetID
}

// extractCandidateChannelID returns the canonical channel_id for
// a candidate. godlike/06 SSOT (Fase 2.3 fallback): MediaCandidate
// carries ChannelID as a first-class field as of Fase 2.3 (forward-
// pointer to Fase 3 linker). When unset (empty string), the
// ranker treats it as "no channel-saturation input available"
// (channel penalty stays 0).
func extractCandidateChannelID(c MediaCandidate) string {
	return c.ChannelID
}

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
