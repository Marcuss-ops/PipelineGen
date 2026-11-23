// Package imagegate owns the ONE eligibility decision for an image slot, and it
// owns nothing else.
//
// Why a package and not a function in `scripts/adapters`: the entity catalog, the
// scene-image lane, the MediaResolver binding and the person-image scorer all
// need the same verdict, and `scripts/adapters` needs the catalog — so a single
// implementation living in `adapters` forces every future module that wants the
// verdict to import the whole adapter root (a cycle, the moment that module is
// itself imported by the root). This package is a leaf: it depends only on the
// script candidate DTO and the entity-catalog status constants, so it can be
// imported from anywhere, including the extraction of `entitycatalog`,
// `imagediscovery` and `materialization` (BON-02 plan, step 0).
//
// The rule it exists for: discovery metadata — a matching label, adequate
// dimensions, a successful download — is NOT evidence that the pixels depict the
// requested person. Older catalog rows wrote `accepted` with exactly that
// metadata-only reason, so the gate must force those rows through visual
// verification instead of reusing them as an identity image.
package imagegate

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
			strings.Contains(reason, "manual visual identity review confirmed") ||
			strings.Contains(reason, "deterministic identity verification confirmed") {
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

// CandidateEligibleForSlot is the canonical semantic gate for an image slot.
// Both the PERSON entity lane and the scene-image lane use it; a caller that
// must certify the depicted identity passes requireVisualIdentity=true. It never
// infers a verdict from discovery metadata alone.
func CandidateEligibleForSlot(candidate scriptpkg.SegmentAssetCandidate, requireVisualIdentity bool) bool {
	if !strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), entitycatalog.CandidateSemanticAccepted) {
		return false
	}
	return !requireVisualIdentity || ReusablePersonImageVerdict(candidate)
}

// FilterVerifiedPersonImageCandidates is the fail-closed boundary for a
// person-identity search. Discovery metadata and a successful image download
// are not evidence that the pixels depict the requested person.
func FilterVerifiedPersonImageCandidates(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if CandidateEligibleForSlot(candidate, true) {
			out = append(out, candidate)
		}
	}
	return out
}

// AllCandidatesHaveReusableVerdict reports whether every candidate already
// carries a definitive pixel verdict, so the caller can skip the download and
// the VLM pass entirely.
func AllCandidatesHaveReusableVerdict(candidates []scriptpkg.SegmentAssetCandidate) bool {
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
