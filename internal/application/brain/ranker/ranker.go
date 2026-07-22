// Package ranker is the canonical home of candidate ranking for
// the Brain capability.
//
// godlike/06 SSOT: the CandidateRanker is the single owner of the
// (candidates + intent -> ordered candidates) transformation. It
// performs no IO and depends only on the brain types and stdlib.
//
// The concrete implementation lives in adapter.go and is backed by the
// rich MediaMemory ranker; the previous toy defaultRanker has been
// removed.
package ranker

import (
	"context"

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
