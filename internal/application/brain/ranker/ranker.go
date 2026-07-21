// Package ranker is the canonical home of candidate ranking for
// the Brain capability.
//
// godlike/06 SSOT: the CandidateRanker is the single owner of the
// (candidates + intent -> ordered candidates) transformation. It
// performs no IO and depends only on the brain types and stdlib.
package ranker

import (
	"context"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
)

// CandidateRanker is the canonical port that orders a pool of
// candidates for a single scene.
type CandidateRanker interface {
	Rank(
		ctx context.Context,
		scene brain.SceneRequest,
		intent brain.VisualIntent,
		candidates []brain.Candidate,
		policy brain.ResolutionPolicy,
	) ([]brain.Candidate, error)
	Version() string
}

// defaultRanker is the canonical pure implementation. It composes a
// deterministic score from the candidate's intrinsic score, keyword
// overlap with the intent, and media-type fitness for the requested
// slots. No backend is contacted.
type defaultRanker struct{}

// NewDefaultRanker returns the canonical candidate ranker.
func NewDefaultRanker() CandidateRanker {
	return &defaultRanker{}
}

// Compile-time assertion: defaultRanker satisfies CandidateRanker.
var _ CandidateRanker = (*defaultRanker)(nil)

// Version returns the canonical ranking-policy version.
func (r *defaultRanker) Version() string {
	return "media-ranker-v1"
}

// Rank orders candidates by descending composite score. The composite
// score is the sum of:
//   - the candidate's own score;
//   - keyword overlap between intent and candidate title/description;
//   - a small media-type bonus when the candidate matches the scene's
//     requested slots.
func (r *defaultRanker) Rank(ctx context.Context, scene brain.SceneRequest, intent brain.VisualIntent, candidates []brain.Candidate, policy brain.ResolutionPolicy) ([]brain.Candidate, error) {
	_ = ctx

	scored := make([]scoredCandidate, 0, len(candidates))
	for _, c := range candidates {
		scored = append(scored, scoredCandidate{
			candidate: c,
			score:     r.compositeScore(c, intent, scene.Slots),
		})
	}

	// Stable sort by score DESC, then ID ASC for determinism.
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].candidate.ID < scored[j].candidate.ID
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
		out = append(out, s.candidate)
	}
	return out, nil
}

type scoredCandidate struct {
	candidate brain.Candidate
	score     float64
}

func (r *defaultRanker) compositeScore(c brain.Candidate, intent brain.VisualIntent, slots []brain.SlotKind) float64 {
	score := c.Score

	// Keyword overlap bonus.
	text := strings.ToLower(c.Title + " " + c.Description)
	overlap := 0.0
	for _, kw := range intent.Keywords {
		if strings.Contains(text, kw) {
			overlap += 0.05
		}
	}
	score += overlap

	// Media-type fitness bonus.
	for _, slot := range slots {
		if fitsSlot(c, slot) {
			score += 0.10
		}
	}

	return score
}

func fitsSlot(c brain.Candidate, slot brain.SlotKind) bool {
	switch slot {
	case brain.SlotPrimaryVideo:
		return c.MediaType == "video"
	case brain.SlotSecondaryImage, brain.SlotEvidenceOverlay,
		brain.SlotMap, brain.SlotPortrait, brain.SlotDocument,
		brain.SlotBackground:
		return c.MediaType == "image"
	}
	return false
}
