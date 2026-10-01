// Package adapters — image_semantic_gate.go: the semantic acceptance gate for
// discovered internet-image candidates.
//
// Discovery is a recall stage: it must return everything plausibly relevant.
// Precision is a second, explicit stage: a scorer decides whether a candidate
// actually depicts what the segment is about. That scorer needs the embedding
// sidecar (SigLIP image vector vs. the segment's text vector) and is therefore
// infrastructure, not an adapter.
//
// The gate below consumes a scorer's verdict. It NEVER synthesizes a score:
// when no scorer is wired, or a candidate carries no verdict, the candidate is
// kept. A missing sidecar degrades precision, not availability.
package adapters

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MinInternetImageSemanticScore is the acceptance floor applied to a candidate
// that carries an explicit semantic score. It is deliberately low: the gate
// exists to drop clearly off-topic results, not to second-guess a working
// scorer with a second ranking authority.
const MinInternetImageSemanticScore = 0.35

// SemanticCandidateScorer annotates discovered image candidates with a
// semantic verdict by setting SemanticStatus / SemanticScore (and optionally
// RelevanceScore / QualityReason). Implementations belong to infrastructure;
// returning an error is treated as "scoring unavailable" and the unscored
// candidates flow through unchanged.
type SemanticCandidateScorer interface {
	ScoreImageCandidates(ctx context.Context, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error)
}

// applyInternetImageSemanticGate drops only candidates a scorer explicitly
// rejected or scored below MinInternetImageSemanticScore. A candidate with no
// verdict (empty SemanticStatus, zero SemanticScore) is kept, so an unwired or
// failing scorer cannot empty a run.
func applyInternetImageSemanticGate(in []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(in))
	for _, candidate := range in {
		if strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticRejected) {
			continue
		}
		if candidate.SemanticScore > 0 && candidate.SemanticScore < MinInternetImageSemanticScore {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// scoreInternetImageCandidates runs the optional scorer over a query's
// candidates. A nil scorer, an empty batch, or a scorer error leaves the
// candidates untouched; the returned error is surfaced so the caller can warn
// without failing discovery.
func scoreInternetImageCandidates(ctx context.Context, scorer SemanticCandidateScorer, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if scorer == nil || len(candidates) == 0 {
		return candidates, nil
	}
	scored, err := scorer.ScoreImageCandidates(ctx, query, candidates)
	if err != nil {
		return candidates, err
	}
	if scored == nil {
		return candidates, nil
	}
	return scored, nil
}
