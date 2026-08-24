package mediamemory

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func errInvalidPhrase(reason string) error {
	return errors.Join(ErrInvalidPhrase, errors.New(reason))
}

// Search implements brain.MediaMemoryResolutionPort by running the
// MediaMemory cascade for the requested media types and returning the
// raw candidate pool. Filtering, scoring and slot assignment are left
// to the Brain orchestrator.
func (r *VisualResolver) Search(ctx context.Context, query brain.SearchQuery) (brain.SearchResult, error) {
	slots := slotsForMediaTypes(query.MediaTypes)
	if len(slots) == 0 {
		return brain.SearchResult{}, nil
	}

	policy := resolvePolicyFromBrainQuery(query)
	scene := SceneSpec{ID: "brain-search", Text: query.Text, Language: query.Language, Slots: slots}
	candidates := make([]brain.Candidate, 0)
	seen := make(map[string]struct{})
	for _, slot := range slots {
		fcs, _, _ := r.candidatesForSlot(ctx, scene, slot, policy)
		for _, fc := range fcs {
			c := toBrainCandidateFromFiltered(fc)
			key := c.AssetID + ":" + c.Provider + ":" + string(slot)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, c)
		}
	}
	return brain.SearchResult{Candidates: candidates}, nil
}

func slotsForMediaTypes(mediaTypes []string) []media.SlotKind {
	var slots []media.SlotKind
	hasVideo, hasImage := false, false
	for _, mt := range mediaTypes {
		hasVideo = hasVideo || mt == "video"
		hasImage = hasImage || mt == "image"
	}
	if hasVideo {
		slots = append(slots, media.SlotPrimaryVideo)
	}
	if hasImage {
		slots = append(slots, media.SlotSecondaryImage, media.SlotEvidenceOverlay, media.SlotMap)
	}
	if len(slots) == 0 {
		slots = []media.SlotKind{media.SlotPrimaryVideo, media.SlotSecondaryImage, media.SlotEvidenceOverlay, media.SlotMap}
	}
	return slots
}

func resolvePolicyFromBrainQuery(query brain.SearchQuery) ResolvePolicy {
	policy := ResolvePolicy{
		PreferApprovedBindings: query.SearchPolicy.PreferApproved,
		AllowExternalSearch:    query.SearchPolicy.AllowExternal,
		MaxCandidatesPerSlot:   query.SearchPolicy.MaxCandidates,
		SearchPolicy:           query.SearchPolicy,
	}
	if policy.MaxCandidatesPerSlot <= 0 && query.Limit > 0 {
		policy.MaxCandidatesPerSlot = query.Limit
	}
	if policy.MaxCandidatesPerSlot <= 0 {
		policy.MaxCandidatesPerSlot = defaultResolverLimit
	}
	return policy
}

func toBrainCandidateFromFiltered(fc FilteredCandidate) brain.Candidate {
	c := fc.Candidate
	materialization := string(c.MaterializationStatus)
	if materialization == "" {
		materialization = "warm"
	}
	rights := string(c.RightsStatus)
	if rights == "" {
		rights = "unknown"
	}
	return brain.Candidate{ID: c.ID, AssetID: c.AssetID, Provider: c.Provider, SourceURL: c.SourceURL,
		ThumbnailURL: c.ThumbnailURL, Title: c.Title, Description: c.Description, MediaType: c.MediaType,
		DurationMs: c.DurationMs, Score: c.CandidateScore, MaterializationState: materialization, RightsStatus: rights}
}
