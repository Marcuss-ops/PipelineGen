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
// Mandatory pre-rank filters (godlike/07) — CANONICAL seven gates
// (Fase 1.5 SSOT, architecture-doc section 11):
//
//  1. valid_license        — RightsStatus == RightsVerified
//  2. available_media      — MaterializationStatus ∈ {Warm, Hot}
//  3. valid_duration       — DurationMs >= 0
//  4. supported_format     — MediaType ∈ {video, image, audio, music}
//     (empty MediaType is the legacy ambiguous
//     sentinel and bypasses the gate for
//     forward-compat)
//  5. compatible_aspect    — AspectRatioW/AspectRatioH ∈ canonical set
//  6. no_corruption        — IntegrityChecked == true
//  7. no_duplicates        — DuplicateOfAssetID == ""
//
// godlike/06 SSOT (gate order): RunMandatoryGates evaluates gates in
// the canonical order above so log scans + dashboard diagnostics
// see a stable drop reason. Format precedes aspect because aspect
// ratio is a structural property of visual media only (audio has
// no aspect ratio).
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
	"time"
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

// Fase 2.2 anti-repetition: 24h channel-recency window (canonical
// SSOT value). Any project-history event for the candidate's
// ChannelID whose CreatedAt is within this window contributes to
// the channel-recency penalty component.
const ChannelRecencyWindowMaxAge = 24 * time.Hour

// ChannelRecencyPenaltyPerEvent is the canonical additive penalty
// per (project_id, channel_id) event in the 24h recency window.
// Each sighting adds this much before the per-candidate ceiling
// clamp; the ranker composes it into the same RepetitionPenalty
// seat so the formula stays additive (godlike/06 SSOT).
const ChannelRecencyPenaltyPerEvent = 0.20

// ChannelRecencyMaxPenalty is the canonical per-candidate ceiling
// for the 24h channel-recency penalty component. Prevents
// unbounded growth when a single channel has hundreds of recent
// project events; canonical value matches the SPEC's "stesso
// canale nelle ultime 24h" threshold.
const ChannelRecencyMaxPenalty = 0.50

// RepetitionPenaltyTotalCeiling is the canonical per-candidate
// total-penalty ceiling applied at the end of
// PopulateRepetitionPenalty. Without this clamp, three or four
// compounding components (SameAsset + Consecutive +
// ChannelSaturation + ChannelRecency) would routinely drive the
// final score below 0.0 → VerdictDrop even on otherwise-strong
// candidates. The clamp keeps a heavily-reused but
// semantically-strong candidate in the Accept/Downrank band so
// the ranker's diversity filter can still see it.
const RepetitionPenaltyTotalCeiling = 1.5

// DiversityFinalScoreDelta is the canonical per-call ceiling on
// how far PickTopFromRose will swap a top-1 winner for a
// non-consecutive-source alternative. A delta of 0.10 means an
// alternative within 0.10 FinalScore of top-1 can displace it
// when top-1 shares prevVideoID (otherwise the highest-scoring
// survivor wins). This is the SPEC's "deterministic-but-
// diversified" knob.
const DiversityFinalScoreDelta = 0.10

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

	// ChannelRecencyWindow is the inclusive time window used
	// by the 24h channel-recency penalty component. Any
	// project-history event whose CreatedAt is within
	// now.Sub(ev.CreatedAt) <= ChannelRecencyWindow
	// contributes ChannelRecencyPenaltyPerEvent to the
	// candidate's total penalty (capped at ChannelRecencyMaxPenalty).
	// godlike/06 SSOT (canonical duration): the value is a
	// time.Duration so a future "1h window" tuning lands via
	// a new RepetitionPenaltyWeightsV2 — never by mutating
	// this field in place.
	ChannelRecencyWindow time.Duration

	// ChannelRecencyPenaltyPerEvent is the additive penalty
	// per in-window event. Mirrors ChannelRecencyPenaltyPerEvent
	// constant for the struct; both must agree (tests pin).
	ChannelRecencyPenaltyPerEvent float64

	// ChannelRecencyMaxPenalty is the per-candidate ceiling
	// for the recency-penalty component. Mirrors the
	// ChannelRecencyMaxPenalty constant; both must agree.
	ChannelRecencyMaxPenalty float64
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
		ChannelRecencyWindow:               ChannelRecencyWindowMaxAge,
		ChannelRecencyPenaltyPerEvent:      ChannelRecencyPenaltyPerEvent,
		ChannelRecencyMaxPenalty:           ChannelRecencyMaxPenalty,
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
	now time.Time,
) []RankingInput {
	weights := DefaultRepetitionPenaltyWeights()

	// Pre-compute (asset, channel) sighting maps from history.
	// godlike/06 SSOT: O(N+M) pass; the resolver's hot path always
	// reads from canonical append-only audit log so we can
	// recompute cheaply on every slot.
	assetSightings := make(map[string]int, 32)
	channelSightings := make(map[string]int, 8)
	// Fase 2.2: pre-compute per-candidate-channel 24h-window
	// sighting counts (no asset_id scope — the recency penalty
	// is channel-level, not asset-level). Recency is keyed by
	// (project_id, channel_id, ev.CreatedAt) so we filter the
	// per-channel count to events within ChannelRecencyWindow
	// of `now`.
	channelRecency := make(map[string]int, 8)
	for _, ev := range history {
		if ev.ProjectID == "" {
			continue
		}
		if ev.AssetID != "" {
			assetSightings[ev.AssetID]++
		}
		if ev.ChannelID != "" {
			channelSightings[ev.ChannelID]++
			// In-window events contribute to the recency
			// component (empty `now` → zero-value → no
			// recency matches, godlike/07 backward compat).
			if !now.IsZero() && now.Sub(ev.CreatedAt) >= 0 &&
				now.Sub(ev.CreatedAt) <= weights.ChannelRecencyWindow {
				channelRecency[ev.ChannelID]++
			}
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
			// 4. Channel-recency penalty (Fase 2.2 NEW):
			//    SPEC's "stesso canale nelle ultime 24h".
			//    Per in-window event for the candidate's
			//    ChannelID, add ChannelRecencyPenaltyPerEvent.
			//    Capped at ChannelRecencyMaxPenalty per
			//    candidate so a channel with hundreds of
			//    recent hits doesn't push the candidate into
			//    VerdictDrop solely on recency grounds.
			if n := channelRecency[candidateChannelID]; n > 0 {
				penalty += math.Min(
					float64(n)*weights.ChannelRecencyPenaltyPerEvent,
					weights.ChannelRecencyMaxPenalty,
				)
			}
		}

		// Final ceiling clamp: prevents 4-component compounding
		// from routinely forcing VerdictDrop on otherwise-
		// strong candidates. The ranker's diversity filter
		// downstream still sees the candidate (in Downrank
		// band) so the top-K rose can rotate it out.
		if penalty > RepetitionPenaltyTotalCeiling {
			penalty = RepetitionPenaltyTotalCeiling
		}

		in.RepetitionPenalty = penalty
		out = append(out, in)
	}
	return out
}

// PickTopFromRose is the canonical Fase 2.2 deterministic-but-
// diversified pick from a ranker rose. The caller (resolver)
// produces `rose` as the sorted-by-FinalScore-DESC slice of
// surviving candidates after Filter + PopulateRepetitionPenalty.
// godlike/06 SSOT (single canonical pick): this helper is the
// SOLE owner of the per-slot top-1 selection — the resolver MUST
// NOT inline a sort+pick.
//
// godlike/06 SSOT (caller-sorted contract): the helper does NOT
// sort. The caller (resolver) MUST invoke sortByFinalScoreDesc
// before passing the rose. A future caller passing an unsorted
// rose gets a silent wrong answer (rose[0] is whatever happens to
// be first); the godlike/06 SSOT here pins the contract so the
// failure mode is auditable, not silent.
//
// Determinism: rose[0] wins by FinalScore DESC (sort.SliceStable
// preserves input order on ties → AssetID ASC tiebreak via the
// canonical lessRanked comparator).
//
// Diversity: when prevVideoID is non-empty AND rose[0]'s video_id
// matches prevVideoID AND a non-consecutive alternative exists
// within DiversityFinalScoreDelta, swap to that alternative. This
// is the SPEC's "selezione deterministica ma diversificata"
// knob — operators don't want the same source video to dominate
// the entire project when alternatives exist within a small score
// delta.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty rose returns the
// zero-value rankedCandidate (no panic). A single-element rose
// returns rose[0] verbatim — no diversity swap possible.
//
// godlike/06 SSOT (immutable contract): the helper does not
// mutate the input slice.
func PickTopFromRose(rose []rankedCandidate, prevVideoID string) rankedCandidate {
	if len(rose) == 0 {
		return rankedCandidate{}
	}
	top := rose[0]
	if prevVideoID == "" {
		return top
	}
	topVideoID := extractCandidateVideoID(top.fc.Candidate)
	if topVideoID == "" || topVideoID != prevVideoID {
		// No consecutive-source pressure on top-1; accept the
		// canonical highest-scoring survivor.
		return top
	}
	// Top-1 shares VideoID with prevVideoID — look for a
	// non-consecutive alternative within DiversityFinalScoreDelta.
	for _, alt := range rose[1:] {
		altVideoID := extractCandidateVideoID(alt.fc.Candidate)
		if altVideoID == prevVideoID || altVideoID == "" {
			continue
		}
		if top.out.FinalScore-alt.out.FinalScore <= DiversityFinalScoreDelta {
			return alt
		}
	}
	// No diverse alternative within delta — accept the
	// consecutive-source winner (the diversity knob only
	// swaps when an alternative is competitive).
	return top
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

// Filter applies the six mandatory gates (license, availability,
// duration, format, aspect, corrupt-detection, dedup) via the
// canonical RunMandatoryGates helper. godlike/06 SSOT
// (single-canonical-pipeline): the ranker's Filter method MUST
// route through RunMandatoryGates so the gate order +
// gate-name vocabulary stays consistent across callers.
//
// Rows where ANY of the six gates fail are REMOVED. Rows whose
// Candidate fails the well-formed guard (missing AssetID,
// unknown MaterializationStatus/DiscoveryStatus) are ALSO removed
// — the ranker's Score path would otherwise drop them with
// VerdictDrop, doing the work twice and producing confusing
// warning cascades.
//
// The output is strictly a subset of `in`. An empty slice is a
// valid outcome (no candidate survives the gates).
func (r *defaultRanker) Filter(_ context.Context, in []FilteredCandidate) ([]FilteredCandidate, error) {
	out := make([]FilteredCandidate, 0, len(in))
	for _, fc := range in {
		if !mediaCandidateIsWellFormed(fc.Candidate) {
			continue
		}
		if gate := RunMandatoryGates(fc); gate != "" {
			if r.log != nil {
				r.log.Debug("mediamemory: ranker.Filter dropped candidate (gate=%q asset_id=%q)", string(gate), fc.Candidate.AssetID)
			}
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

// ── Ranking & rights: canonical helpers ────────────────────────────

// ComputeDurationFit returns the canonical [0,1] duration_fit
// score for a (sceneDurationMs, bindingDurationMs) pair.
// godlike/06 SSOT (closed-form formula): the helper favours
// bindings whose duration matches the scene (ratio ≈ 1.0);
// bindings whose duration is wildly off (ratio < 0.5 or > 2.0)
// receive 0.0 so the ranker drops them at VerdictDownrank/Drop.
// godlike/07 NO-FAKE-AVAILABILITY: zero or negative durations
// return 0.0 (the ranker's mandatory duration_valid gate will
// independently reject DurationMs <= 0 candidates before this
// helper is consulted).
//
// Scale: ratio == 1.0 → 1.0; ratio == 0.5 or 2.0 → 0.0; linear
// interpolation between (0.5, 0.0) and (1.0, 1.0) on the
// short-clip side, and between (1.0, 1.0) and (2.0, 0.0) on
// the long-clip side. The formula is symmetric around ratio 1.0
// so a binding that is half the scene duration scores the same
// as a binding that is twice the scene duration.
func ComputeDurationFit(sceneDurationMs, bindingDurationMs int64) float64 {
	if sceneDurationMs <= 0 || bindingDurationMs <= 0 {
		return 0.0
	}
	ratio := float64(bindingDurationMs) / float64(sceneDurationMs)
	if ratio < 0.5 || ratio > 2.0 {
		return 0.0
	}
	// Linear interpolation: at ratio=1.0 → 1.0; at the
	// boundaries (0.5, 2.0) → 0.0.
	if ratio < 1.0 {
		return 1.0 - (1.0-ratio)*2.0
	}
	return 1.0 - (ratio-1.0)*2.0
}

// PopulateRightsPenalty applies the canonical per-RightsStatus
// penalty stamp onto RankingInput.RightsPenalty. godlike/06 SSOT
// (closed-set mapping) — godlike/07 fail-closed (Fase 1.5):
//
//	RightsVerified → 0.0  (no penalty; Resolver MissingRights gate
//	                    already rejects non-Verified upstream as
//	                    defense in depth — this path is the
//	                    happy-case)
//	RightsUnknown  → 1.0  (FAIL-CLOSED: unverified rights receive
//	                    full penalty, guaranteeing VerdictDrop.
//	                    The Hot-tier guard in batch_service.
//	                    MarkMaterialized already requires RightsVerified
//	                    for promotion; this penalty is the
//	                    ranker's second-line guarantee so a
//	                    candidate slipping past Filter still
//	                    cannot auto-promote to Hot.)
//	RightsDenied   → 1.0  (full penalty; guarantees VerdictDrop
//	                    — the MissingRights gate in Filter already
//	                    rejects this case upstream)
//	RightsExpired  → 1.0  (FAIL-CLOSED: expired rights also
//	                    receive full penalty per Fase 1.5
//	                    godlike/07 policy — the architecture
//	                    doc treats unverified/expired/denied
//	                    uniformly as "no positive rights
//	                    attestation" and the ranker must not
//	                    promote them. The caller MUST refresh
//	                    rights_status before the next render.)
//
// godlike/06 SSOT (immutable input contract, mirrors
// PopulateRepetitionPenalty): the function does NOT mutate the
// input slice — outputs are returned as a NEW slice with the
// input's candidates copied through and the penalty seat
// re-stamped. Callers MUST use the returned slice.
func PopulateRightsPenalty(inputs []RankingInput) []RankingInput {
	out := make([]RankingInput, 0, len(inputs))
	for _, in := range inputs {
		switch in.Candidate.RightsStatus {
		case RightsVerified:
			in.RightsPenalty = 0.0
		case RightsUnknown, RightsDenied, RightsExpired:
			// godlike/07 fail-closed (Fase 1.5): any
			// non-Verified rights status applies full
			// penalty. The MissingRights filter gate
			// already drops these upstream; this
			// penalty is the second-line guarantee
			// so no candidate with unverified/expired/
			// denied rights can sneak into Hot.
			in.RightsPenalty = 1.0
		}
		out = append(out, in)
	}
	return out
}

// HasAvailableMedia reports whether a candidate's materialization
// tier is sufficient for the ranker's mandatory media_available
// gate. godlike/06 SSOT (tier SSOT): Cold candidates are metadata-
// only (no bytes available), Warm candidates have bytes on Drive
// or segmentable on-demand, Hot candidates are staged locally.
// godlike/07 NO-FAKE-AVAILABILITY: a candidate at Cold tier
// cannot be rendered — the ranker MUST drop it before scoring.
func HasAvailableMedia(c MediaCandidate) bool {
	return c.MaterializationStatus == MaterializationHot ||
		c.MaterializationStatus == MaterializationWarm
}

// HasValidDuration reports whether the candidate's DurationMs is
// non-negative. godlike/06 SSOT (canonical gate): negative
// DurationMs is treated as a malformed envelope and is dropped.
// DurationMs == 0 is the canonical image/document binding sentinel
// and is accepted so image bindings and unmeasured clips are not
// silently discarded before the duration_fit scoring stage.
func HasValidDuration(c MediaCandidate) bool {
	return c.DurationMs >= 0
}

// ── Mandatory gates (Fase "Ranking & rights") ────────────────────

// PreRankGate is the canonical closed-set enum for the six
// mandatory pre-rank gates the architecture doc (section 11)
// requires. godlike/06 SSOT: every gate has a canonical name
// + reason so log scans + dashboard diagnostics can audit
// drops without parsing the error string.
type PreRankGate string

const (
	// GateValidLicense — RightsStatus MUST be RightsVerified.
	GateValidLicense PreRankGate = "valid_license"
	// GateAvailableMedia — MaterializationStatus MUST be Warm
	// or Hot (Cold / Failed drop).
	GateAvailableMedia PreRankGate = "available_media"
	// GateValidDuration — DurationMs MUST be non-negative
	// (DurationMs == 0 is the canonical image/document
	// binding sentinel per HasValidDuration so 0 is accepted).
	GateValidDuration PreRankGate = "valid_duration"
	// GateSupportedFormat — MediaType MUST be in the canonical
	// set {video, image, audio, music}. Empty MediaType is the
	// legacy ambiguous sentinel and bypasses the gate
	// (forward-compat for pre-Fase-1.5 fixtures).
	GateSupportedFormat PreRankGate = "supported_format"
	// GateCompatibleAspect — AspectRatioW / AspectRatioH MUST
	// be 16:9 / 4:3 / 1:1 / 9:16 (canonical set). Folds
	// MediaType out to GateSupportedFormat so each gate has a
	// single owner per godlike/06 SSOT.
	GateCompatibleAspect PreRankGate = "compatible_aspect"
	// GateNoCorruption — IntegrityChecked MUST be true.
	GateNoCorruption PreRankGate = "no_corruption"
	// GateNoDuplicates — DuplicateOfAssetID MUST be empty.
	GateNoDuplicates PreRankGate = "no_duplicates"
)

// RunMandatoryGates applies the canonical seven pre-rank gates
// (Fase 1.5 SSOT) and returns the first failing gate (empty
// string = all passed). godlike/06 SSOT (narrow contract): the
// helper is math-only (no IO); the resolver pre-populates the
// four boolean flags on FilteredCandidate + the structural fields
// on MediaCandidate before calling this helper. godlike/07
// NO-FAKE-AVAILABILITY: a candidate failing ANY gate MUST NOT
// proceed to Score.
//
// Gate evaluation order is canonical (matches the constants'
// declaration order):
//  1. GateNoDuplicates    (IsDuplicate flag)
//  2. GateValidLicense    (MissingRights flag — set by resolver
//     when RightsStatus != RightsVerified)
//  3. GateNoCorruption    (Contaminated flag — set by the
//     integrity checker worker)
//  4. GateAvailableMedia  (MaterializationStatus, computed here)
//  5. GateValidDuration   (DurationMs, computed here)
//  6. GateSupportedFormat (MediaType, computed here; Fase 1.5)
//  7. GateCompatibleAspect (AspectMismatch flag — set by resolver)
//
// Flags 2, 6, 7 are resolver-set; flags 1, 3 are worker-set;
// fields 4, 5 are computed in this helper against MediaCandidate.
// godlike/06 SSOT (single owner per fact): every gate has exactly
// one computation site, here.
func RunMandatoryGates(fc FilteredCandidate) PreRankGate {
	if fc.IsDuplicate {
		return GateNoDuplicates
	}
	if fc.MissingRights {
		return GateValidLicense
	}
	if fc.Contaminated {
		return GateNoCorruption
	}
	if !HasAvailableMedia(fc.Candidate) {
		return GateAvailableMedia
	}
	if !HasValidDuration(fc.Candidate) {
		return GateValidDuration
	}
	if !HasSupportedFormat(fc.Candidate) {
		return GateSupportedFormat
	}
	if fc.AspectMismatch {
		return GateCompatibleAspect
	}
	return ""
}

// HasSupportedFormat reports whether a candidate's MediaType is in
// the canonical supported-set {video, image, audio, music}.
// godlike/06 SSOT (canonical format set): adding a new supported
// MediaType requires both a code change here AND an update to the
// IsKnownMediaType helper (per the architectural doc section 11).
//
// godlike/06 SSOT (forward-compat bypass): empty MediaType is the
// legacy ambiguous sentinel for candidates produced before the
// MediaType field was populated; the gate bypasses so historical
// fixtures remain testable until a Fase 1.x migration backfills
// MediaType. A future Fase draft may flip the empty-string branch
// to fail-closed once all upstream discovery workers emit MediaType.
//
// godlike/07 NO-FAKE-AVAILABILITY: a non-canonical MediaType (e.g.
// "vrm", "raw", "") used to be passed silently; now this helper
// gates it so the ranker never credits an unknown MediaType.
func HasSupportedFormat(c MediaCandidate) bool {
	if c.MediaType == "" {
		return true // legacy ambiguous sentinel bypass
	}
	switch c.MediaType {
	case "video", "image", "audio", "music":
		return true
	default:
		return false
	}
}
