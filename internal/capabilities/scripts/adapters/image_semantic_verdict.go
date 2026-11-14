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
	if status == entitycatalog.CandidateSemanticAccepted {
		// A matching query/entity label and adequate dimensions are discovery
		// metadata, not a verdict about the pixels. Older catalog rows used that
		// metadata-only reason as `accepted`; force those rows through visual
		// verification instead of treating them as a reusable identity image.
		if strings.Contains(reason, "pinned by media_manifest.v1: offline sha256 verify") ||
			strings.Contains(reason, "manual visual identity review confirmed") {
			return true
		}
		return strings.Contains(reason, "ollama visual identity verification confirmed")
	}
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

// filterVerifiedPersonImageCandidates is the fail-closed boundary for a
// person-identity search. Discovery metadata and a successful image download
// are not evidence that the pixels depict the requested person.
func filterVerifiedPersonImageCandidates(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticAccepted) &&
			ReusablePersonImageVerdict(candidate) {
			out = append(out, candidate)
		}
	}
	return out
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
