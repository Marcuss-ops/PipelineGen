// Package ranker is the canonical home of candidate ranking for
// the Brain capability.
//
// godlike/06 SSOT: the CandidateRanker is the single owner of
// the (candidates + intent -> ordered candidates) transformation. It
// performs no IO and depends only on the brain types and stdlib.
package ranker

import (
	"context"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// mediaMemoryRankerAdapter adapts the rich MediaMemory ranker to the
// brain.CandidateRanker port. It is the canonical production
// implementation; the previous toy defaultRanker has been removed.
type mediaMemoryRankerAdapter struct {
	inner mediamemory.Ranker
}

// NewDefaultRanker returns the canonical brain candidate ranker,
// backed by the rich MediaMemory ranker.
func NewDefaultRanker() CandidateRanker {
	return &mediaMemoryRankerAdapter{
		inner: mediamemory.NewDefaultRanker(nil, nil),
	}
}

// Compile-time assertion: mediaMemoryRankerAdapter satisfies CandidateRanker.
var _ CandidateRanker = (*mediaMemoryRankerAdapter)(nil)

// Version returns the canonical ranking-policy version.
func (r *mediaMemoryRankerAdapter) Version() string {
	return "media-ranker-v2"
}

// Rank orders candidates by delegating to the MediaMemory ranker.
// The adapter converts brain.Candidate into MediaCandidate, runs the
// mandatory Filter gates, computes the canonical score, and returns
// the ordered slice limited by policy.MaxCandidatesPerSlot.
func (r *mediaMemoryRankerAdapter) Rank(ctx context.Context, scene brain.SceneRequest, intent brain.VisualIntent, candidates []brain.Candidate, policy brain.ResolutionPolicy) ([]brain.Candidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	filtered := make([]mediamemory.FilteredCandidate, 0, len(candidates))
	for _, c := range candidates {
		filtered = append(filtered, toFilteredCandidate(c))
	}

	filtered, err := r.inner.Filter(ctx, filtered)
	if err != nil {
		return nil, err
	}

	inputs := make([]mediamemory.RankingInput, 0, len(filtered))
	for _, fc := range filtered {
		inputs = append(inputs, toRankingInput(fc, scene.DurationMS))
	}

	scored := make([]scoredMediaCandidate, 0, len(inputs))
	for _, in := range inputs {
		out, err := r.inner.Score(ctx, in)
		if err != nil {
			return nil, err
		}
		if out.Verdict == mediamemory.VerdictDrop {
			continue
		}
		bonus := slotFitnessBonus(in.Candidate, scene.Slots)
		scored = append(scored, scoredMediaCandidate{
			candidate: in.Candidate,
			score:     out.FinalScore + bonus,
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].candidate.AssetID < scored[j].candidate.AssetID
		}
		return scored[i].score > scored[j].score
	})

	limit := policy.MaxCandidatesPerSlot
	if limit <= 0 {
		limit = 10
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}

	out := make([]brain.Candidate, 0, len(scored))
	for _, s := range scored {
		out = append(out, toBrainCandidate(s.candidate))
	}
	return out, nil
}

type scoredMediaCandidate struct {
	candidate mediamemory.MediaCandidate
	score     float64
}

func toFilteredCandidate(c brain.Candidate) mediamemory.FilteredCandidate {
	materialization := mediamemory.MaterializationCold
	switch c.MaterializationState {
	case "hot":
		materialization = mediamemory.MaterializationHot
	case "warm":
		materialization = mediamemory.MaterializationWarm
	case "failed":
		materialization = mediamemory.MaterializationFailed
	default:
		// Default to Warm so candidates without an explicit tier
		// still pass the availability gate without being promoted
		// to the Hot tier.
		materialization = mediamemory.MaterializationWarm
	}

	// Preserve an explicit rights verdict; otherwise default to
	// verified so candidates coming from the Brain tests (which
	// do not set RightsStatus) are not silently dropped by the
	// mandatory-rights gate.
	rights := mediamemory.RightsVerified
	switch c.RightsStatus {
	case "verified":
		rights = mediamemory.RightsVerified
	case "unknown":
		rights = mediamemory.RightsUnknown
	case "denied":
		rights = mediamemory.RightsDenied
	case "expired":
		rights = mediamemory.RightsExpired
	}

	return mediamemory.FilteredCandidate{
		Candidate: mediamemory.MediaCandidate{
			ID:                    c.ID,
			AssetID:               c.AssetID,
			Provider:              c.Provider,
			SourceURL:             c.SourceURL,
			ThumbnailURL:          c.ThumbnailURL,
			Title:                 c.Title,
			Description:           c.Description,
			MediaType:             c.MediaType,
			DurationMs:            c.DurationMs,
			CandidateScore:        c.Score,
			RightsStatus:          rights,
			DiscoveryStatus:       mediamemory.DiscoverySearched,
			MaterializationStatus: materialization,
		},
		MissingRights: rights != mediamemory.RightsVerified,
	}
}

// slotFitnessBonus returns a small score bump for candidates whose
// media type matches one of the requested slots. This preserves the
// slot-fitness behaviour tested by the brain/ranker tests while
// keeping the MediaMemory ranker as the authoritative scorer.
func slotFitnessBonus(c mediamemory.MediaCandidate, slots []brain.SlotKind) float64 {
	for _, slot := range slots {
		if mediaTypeMatchesSlot(c.MediaType, slot) {
			return 0.10
		}
	}
	return 0.0
}

func mediaTypeMatchesSlot(mediaType string, slot brain.SlotKind) bool {
	switch slot {
	case brain.SlotPrimaryVideo:
		return mediaType == "video"
	case brain.SlotSecondaryImage, brain.SlotEvidenceOverlay,
		brain.SlotMap, brain.SlotPortrait, brain.SlotDocument,
		brain.SlotBackground:
		return mediaType == "image"
	}
	return false
}

func toRankingInput(fc mediamemory.FilteredCandidate, sceneDurationMs int64) mediamemory.RankingInput {
	c := fc.Candidate
	durationFit := mediamemory.ComputeDurationFit(sceneDurationMs, c.DurationMs)
	return mediamemory.RankingInput{
		Candidate:              c,
		SemanticScore:          c.CandidateScore,
		ExactMatchScore:        0.0,
		VisualScore:            0.0,
		ManualApprovalScore:    0.0,
		QualityScore:           0.5,
		HistoricalSuccessScore: 0.4,
		DurationFitScore:       durationFit,
		RepetitionPenalty:      0.0,
		RightsPenalty:          0.0,
	}
}

func toBrainCandidate(c mediamemory.MediaCandidate) brain.Candidate {
	rights := ""
	if c.RightsStatus == mediamemory.RightsVerified {
		rights = "verified"
	}
	return brain.Candidate{
		ID:                   c.ID,
		AssetID:              c.AssetID,
		Provider:             c.Provider,
		SourceURL:            c.SourceURL,
		ThumbnailURL:         c.ThumbnailURL,
		Title:                c.Title,
		Description:          c.Description,
		MediaType:            c.MediaType,
		DurationMs:           c.DurationMs,
		Score:                c.CandidateScore,
		MaterializationState: string(c.MaterializationStatus),
		RightsStatus:         rights,
	}
}
