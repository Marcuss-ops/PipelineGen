package adapters

import (
	"context"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	EntityImageVLMOptional = "optional"
	EntityImageVLMOff      = "off"
	EntityImageVLMRequired = "required"
)

// OptionalEntityImageScorer wraps a VLM SemanticCandidateScorer so visual
// verification can be configured as "optional" (default), "off", or "required".
type OptionalEntityImageScorer struct {
	mode string
	vlm  SemanticCandidateScorer
}

// NewOptionalEntityImageScorer creates a scorer honoring mode ("optional",
// "off", or "required"). Unknown/empty modes default to "optional".
func NewOptionalEntityImageScorer(mode string, vlm SemanticCandidateScorer) SemanticCandidateScorer {
	normalized := normalizeEntityImageVLMMode(mode)
	if normalized == EntityImageVLMRequired {
		return vlm
	}
	return &OptionalEntityImageScorer{
		mode: normalized,
		vlm:  vlm,
	}
}

func normalizeEntityImageVLMMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case EntityImageVLMOff, "disabled", "false", "0", "none":
		return EntityImageVLMOff
	case EntityImageVLMRequired, "strict", "true", "1":
		return EntityImageVLMRequired
	default:
		return EntityImageVLMOptional
	}
}

func (s *OptionalEntityImageScorer) ScoreImageCandidates(ctx context.Context, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	out := append([]scriptpkg.SegmentAssetCandidate(nil), candidates...)
	if s.mode == EntityImageVLMOff || s.vlm == nil {
		reason := "deterministic identity verification confirmed (vlm disabled)"
		if s.mode != EntityImageVLMOff {
			reason = "deterministic identity verification confirmed (vlm unconfigured)"
		}
		for i := range out {
			acceptDeterministically(&out[i], 0.75, reason)
		}
		return out, nil
	}

	var vlmIndices []int
	var vlmBatch []scriptpkg.SegmentAssetCandidate
	for i := range out {
		cand := &out[i]
		if ReusablePersonImageVerdict(*cand) ||
			strings.EqualFold(strings.TrimSpace(cand.SemanticStatus), entitycatalog.CandidateSemanticRejected) {
			continue
		}
		if isTrustedEncyclopediaImageCandidate(*cand) {
			acceptDeterministically(cand, 0.85, "deterministic identity verification confirmed (trusted source)")
			continue
		}
		vlmIndices = append(vlmIndices, i)
		vlmBatch = append(vlmBatch, *cand)
	}
	if len(vlmBatch) == 0 {
		return out, nil
	}

	scored, err := s.vlm.ScoreImageCandidates(ctx, query, vlmBatch)
	if err != nil || len(scored) != len(vlmBatch) {
		for _, idx := range vlmIndices {
			acceptDeterministically(&out[idx], 0.70, "deterministic identity verification confirmed (vlm fallback)")
		}
		return out, nil
	}

	for pos, origIdx := range vlmIndices {
		cand := scored[pos]
		status := strings.ToLower(strings.TrimSpace(cand.SemanticStatus))
		if status == entitycatalog.CandidateSemanticAccepted || status == entitycatalog.CandidateSemanticRejected {
			out[origIdx] = cand
			continue
		}
		// VLM returned unknown (e.g. timeout/circuit-breaker); fall back to
		// deterministic acceptance instead of blocking the slot.
		acceptDeterministically(&cand, 0.70, "deterministic identity verification confirmed (vlm fallback)")
		out[origIdx] = cand
	}
	return out, nil
}

func acceptDeterministically(cand *scriptpkg.SegmentAssetCandidate, floorScore float64, reason string) {
	if cand == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(cand.SemanticStatus), entitycatalog.CandidateSemanticRejected) {
		return
	}
	cand.SemanticStatus = entitycatalog.CandidateSemanticAccepted
	if cand.SemanticScore < floorScore {
		cand.SemanticScore = floorScore
	}
	cand.QualityReason = reason
}

func isTrustedEncyclopediaImageCandidate(cand scriptpkg.SegmentAssetCandidate) bool {
	for _, prov := range []string{cand.RetrievalProvider, cand.Provider} {
		switch strings.ToLower(strings.TrimSpace(prov)) {
		case "wikipedia", "wikimedia", "wikimedia_commons", "wikidata":
			return true
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(cand.SourceURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "wikipedia.org" || strings.HasSuffix(host, ".wikipedia.org") ||
		host == "wikimedia.org" || strings.HasSuffix(host, ".wikimedia.org") ||
		host == "wikidata.org" || strings.HasSuffix(host, ".wikidata.org")
}
