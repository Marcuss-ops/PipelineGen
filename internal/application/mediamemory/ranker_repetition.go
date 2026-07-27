// Package mediamemory — ranker_repetition.go is the canonical
// home for the Fase 2.2/2.3 anti-repetition surface:
// AntiRepetitionHistoryLimit (canonical project-history ceiling),
// the four channel-recency/penalty/diversity/ceiling constants,
// the RepetitionPenaltyWeights struct + DefaultRepetitionPenaltyWeights
// (canonical Phase 2.3 formula weights), PopulateRepetitionPenalty
// (the per-candidate penalty stamp), PickTopFromRose (the
// deterministic-but-diversified top-1 picker), and the two
// extraction helpers (extractCandidateVideoID / extractCandidateChannelID).
//
// godlike/06 SSOT (single canonical home per concern): every
// anti-repetition decision lives here so the canonical formula
// + channel-recency math + diversity knob are one grep-able
// home. Tests in ranker_test.go + ranker_recency_test.go pin
// the constants + the formula; drift here is a ranker-side
// regression per godlike/06.
//
// File split ownership (godlike/06 SSOT):
//   - ranker.go               : Ranker port + defaultRanker + NewDefaultRanker + Score + Filter
//   - ranker_types.go         : RankingInput + RankingOutput + RankingVerdict + FilteredCandidate
//   - ranker_weights.go       : DefaultRankWeights + Weights() + VerdictThresholds + DefaultVerdictThresholds + classifyScore + verdictReason
//   - ranker_gates.go         : PreRankGate + 7 gates + RunMandatoryGates + HasAvailableMedia/HasValidDuration/HasSupportedFormat/mediaCandidateIsWellFormed + ComputeDurationFit + PopulateRightsPenalty
//   - ranker_repetition.go    : AntiRepetitionHistoryLimit + 4 channel-recency/ceiling consts + DiversityFinalScoreDelta + RepetitionPenaltyWeights + DefaultRepetitionPenaltyWeights + PopulateRepetitionPenalty + PickTopFromRose + extractCandidateVideoID/ChannelID  ← this file
package mediamemory

import (
	"math"
	"time"
)

// ── Fase 2.2/2.3 anti-repetition constants / weights ────────────────

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
		// from routinely forcing VerdictDrop on otherwise
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
//
// godlike/06 SSOT (rankedCandidate shared with resolver_scoring.go):
// the ranker package helper takes / returns rankedCandidate. Same-
// package visibility: resolver_scoring.go's rankedCandidate type
// is the canonical one and is referenced here without
// re-exporting.
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
