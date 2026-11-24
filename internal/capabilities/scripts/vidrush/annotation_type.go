package vidrush

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// normalizeAnnotationType folds annotation spellings onto the canonical
// uppercase entity kind.
func normalizeAnnotationType(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// MaxSemanticVerificationCandidates bounds how many provider candidates one
// semantic query sends to the pixel/VLM verifier.
const MaxSemanticVerificationCandidates = 8

func capSemanticVerificationCandidates(in []scriptpkg.SegmentAssetCandidate, limit int) []scriptpkg.SegmentAssetCandidate {
	if limit <= 0 || len(in) <= limit {
		return in
	}
	return in[:limit]
}
