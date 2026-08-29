// Package ports defines the CandidateReranker port: the small-model semantic
// rerank over planned candidate windows. The reranker orders candidates by
// meaning; the final selection score stays deterministic and lives with the
// ranking policy in the adapters layer.
package ports

import (
	"context"
	"errors"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// CandidateRerankRequest carries one segment's planned candidate windows plus
// the semantic understanding used to judge them. The small model sees only
// meaning (segment text, topic, transcript excerpt); it never sees provider
// plumbing and never initiates a download.
type CandidateRerankRequest struct {
	// SegmentID bounds the rerank to one segment (cache isolation).
	SegmentID string
	// Text is the segment voiceover text the windows must match.
	Text string
	// Topic is the segment topic from the semantic profile (may be empty).
	Topic string
	// TargetDurationMs is the beat's timing budget for the window.
	TargetDurationMs int64
	// Candidates are the planned windows to rerank, in provider order.
	Candidates []scriptpkg.SegmentAssetCandidate
}

// CandidateRerankResult is the reranker's verdict for one candidate window.
// SemanticScore is the small model's meaning match in [0, 1]; Reason is a
// short human-readable justification.
type CandidateRerankResult struct {
	CandidateID   string  `json:"candidate_id"`
	SemanticScore float64 `json:"semantic_score"`
	Reason        string  `json:"reason,omitempty"`
}

// CandidateReranker is the small-model semantic rerank over planned
// candidate windows. Implementations return exactly one result per
// candidate, keyed by CandidateID (the candidate's AssetID slot in the
// same order as the request when the backend has no native identity).
type CandidateReranker interface {
	Rerank(ctx context.Context, req CandidateRerankRequest) ([]CandidateRerankResult, error)
}

// ErrCandidateRerankUnavailable marks reranker failures the caller may
// degrade from: the deterministic final score still ranks without the
// small-model semantic component.
var ErrCandidateRerankUnavailable = errors.New("candidate reranker unavailable")
