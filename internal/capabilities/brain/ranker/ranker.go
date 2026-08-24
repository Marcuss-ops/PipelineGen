// Package ranker is the canonical home of the CandidateRanker port
// for the Brain capability.
//
// godlike/06 SSOT: the CandidateRanker is the single owner of the
// (candidates + intent -> ordered candidates) transformation. It
// performs no IO and depends only on the brain types and stdlib.
//
// The concrete implementation is the MediaMemory-backed adapter wired
// in the composition root (internal/app); the previous toy
// defaultRanker has been removed.
package ranker

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/brain"
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
	// DiversityPolicyVersion returns the version of the diversity /
	// anti-repetition policy applied by this ranker. The ranker is
	// the canonical owner of this version; the Brain reads it only
	// for the decision fingerprint.
	DiversityPolicyVersion() string
}
