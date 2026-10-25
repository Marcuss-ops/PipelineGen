package adapters

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ReusablePersonImageVerdict reports whether a candidate already carries a
// definitive pixel verdict. Discovery is allowed to reuse that verdict without
// downloading the image again; only a transient failure (timeout, unavailable
// scorer, download error) stays retryable on a later run.
func ReusablePersonImageVerdict(candidate scriptpkg.SegmentAssetCandidate) bool {
	status := strings.ToLower(strings.TrimSpace(candidate.SemanticStatus))
	if status != entitycatalog.CandidateSemanticAccepted && status != entitycatalog.CandidateSemanticRejected {
		return false
	}
	reason := strings.ToLower(strings.TrimSpace(candidate.QualityReason))
	if status == entitycatalog.CandidateSemanticRejected {
		// A rejection caused by an outage is not a quality fact. Keep it
		// retryable so a recovered scorer can accept the image later.
		for _, transient := range []string{
			"verification failed",
			"verification unavailable",
			"could not be downloaded",
			"model is not configured",
		} {
			if strings.Contains(reason, transient) {
				return false
			}
		}
	}
	return true
}

func allCandidatesHaveReusableVerdict(candidates []scriptpkg.SegmentAssetCandidate) bool {
	if len(candidates) == 0 {
		return true
	}
	for _, candidate := range candidates {
		if !ReusablePersonImageVerdict(candidate) {
			return false
		}
	}
	return true
}
