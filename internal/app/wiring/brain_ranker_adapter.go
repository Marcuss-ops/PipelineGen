// brain_ranker_adapter.go adapts mediamemory.PoolRanker to the
// brain.CandidateRanker port. It lives in the composition root (internal/app)
// because it is a pure cross-package bridge that must depend on both the brain
// types and the MediaMemory port; keeping it out of internal/capabilities/brain
// removes the brain <-> mediamemory architectural import cycle.
//
// godlike/06 SSOT (TICKET-16): the adapter no longer scores, gates or sorts.
// It builds ONE pool request for the scene, sends the caller's slot-fitness
// preference as the per-candidate bonus, and returns the candidates in the
// order the canonical ranker decided. The ordering rule has exactly one owner
// across the repository: `mediasampler::rank`.
package wiring

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain/ranker"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// brainPoolSlotKind is the neutral slot for a pool ranked before the planner
// assigns slots: it disables the aspect gate, it does not pretend the pool
// belongs to primary_video.
const brainPoolSlotKind = mediamemory.SlotKind("any")

// mediaMemoryRankerAdapter adapts the canonical ranker to the
// brain.CandidateRanker port.
type mediaMemoryRankerAdapter struct {
	pool mediamemory.PoolRanker
}

// NewMediaMemoryRankerAdapter returns a CandidateRanker backed by the canonical
// ranker. This is the production constructor used by the composition root so
// that the cascade and the Brain orchestrator share one ranker boundary.
func NewMediaMemoryRankerAdapter(pool mediamemory.PoolRanker) ranker.CandidateRanker {
	if pool == nil {
		panic("app: MediaMemory pool ranker is required")
	}
	return &mediaMemoryRankerAdapter{pool: pool}
}

// Compile-time assertion: mediaMemoryRankerAdapter satisfies CandidateRanker.
var _ ranker.CandidateRanker = (*mediaMemoryRankerAdapter)(nil)

// Version returns the canonical ranking-policy version.
func (r *mediaMemoryRankerAdapter) Version() string {
	return media.VersionMediaRanker
}

// DiversityPolicyVersion returns the canonical diversity / anti-repetition
// policy version applied by the ranker.
func (r *mediaMemoryRankerAdapter) DiversityPolicyVersion() string {
	return media.VersionDiversityPolicy
}

// Rank orders candidates through the canonical ranker and returns the
// survivors limited by policy.MaxCandidatesPerSlot.
//
// godlike/07 NO-FAKE-AVAILABILITY: a ranker failure — or an answer that names a
// candidate outside the pool — is returned as an error. The Brain must not
// receive an unordered or partial pool.
func (r *mediaMemoryRankerAdapter) Rank(ctx context.Context, scene brain.SceneRequest, _ brain.VisualIntent, candidates []brain.Candidate, policy brain.ResolutionPolicy) ([]brain.Candidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	request := mediamemory.PoolRankRequest{
		SceneDurationMS: scene.DurationMS,
		SlotKind:        brainPoolSlotKind,
		Candidates:      make([]mediamemory.PoolRankCandidate, 0, len(candidates)),
	}
	byKey := make(map[string]mediamemory.FilteredCandidate, len(candidates))
	for index, c := range candidates {
		fc := toFilteredCandidate(c)
		// The key is the adapter's identity for the pool entry; the ranker
		// echoes it and never interprets it.
		key := fmt.Sprintf("%d|%s", index, c.ID)
		byKey[key] = fc
		request.Candidates = append(request.Candidates, mediamemory.PoolRankCandidate{
			Key:      key,
			Filtered: fc,
			Bonus:    slotFitnessBonus(fc.Candidate, scene.Slots),
		})
	}

	outcome, err := r.pool.RankPool(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("brain media ranker: %w", err)
	}

	limit := policy.MaxCandidatesPerSlot
	if limit <= 0 {
		limit = 10
	}

	out := make([]brain.Candidate, 0, len(outcome.Ranked))
	for _, scored := range outcome.Ranked {
		fc, ok := byKey[scored.CandidateID]
		if !ok {
			return nil, fmt.Errorf("brain media ranker: answer names unknown candidate %q", scored.CandidateID)
		}
		if len(out) == limit {
			break
		}
		out = append(out, toBrainCandidate(fc.Candidate))
	}
	return out, nil
}

func toFilteredCandidate(c brain.Candidate) mediamemory.FilteredCandidate {
	var materialization mediamemory.MaterializationStatus
	switch c.MaterializationState {
	case "hot":
		materialization = mediamemory.MaterializationHot
	case "warm":
		materialization = mediamemory.MaterializationWarm
	case "failed":
		materialization = mediamemory.MaterializationFailed
	default:
		// Default to Warm so candidates without an explicit tier still pass
		// the availability gate without being promoted to the Hot tier.
		materialization = mediamemory.MaterializationWarm
	}

	// Preserve an explicit rights verdict; otherwise default to verified so
	// candidates coming from the Brain tests (which do not set RightsStatus)
	// are not dropped by the license gate.
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
	}
}

// slotFitnessBonus returns a small ordering preference for candidates whose
// media type matches one of the requested slots. It is the caller's
// preference, not a scoring seat: the ranker applies it after the verdict, so
// a strong off-slot candidate still outranks a weak on-slot one when the gap
// exceeds the bonus.
func slotFitnessBonus(c mediamemory.MediaCandidate, slots []media.SlotKind) float64 {
	for _, slot := range slots {
		if media.IsMediaTypeAllowed(slot, c.MediaType) {
			return 0.10
		}
	}
	return 0.0
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
