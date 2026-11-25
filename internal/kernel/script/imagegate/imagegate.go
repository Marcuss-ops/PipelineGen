// Package imagegate owns the ONE eligibility decision for an image slot, and it
// owns nothing else.
//
// It is a kernel leaf: it depends only on the script candidate DTO and the
// shared semantic verdict vocabulary, so both the scripts capability and
// platform adapters (the Ollama person-image scorer) can import it without a
// cycle or a business-package dependency.
//
// The rule it exists for: discovery metadata — a matching label, adequate
// dimensions, a successful download — is NOT evidence that the pixels depict the
// requested person. Older catalog rows wrote `accepted` with exactly that
// metadata-only reason, so the gate must force those rows through visual
// verification instead of reusing them as an identity image.
package imagegate

import (
	"net/url"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ReusablePersonImageVerdict reports whether a candidate already carries a
// definitive pixel verdict. Discovery is allowed to reuse that verdict without
// downloading the image again; only a transient failure (timeout, unavailable
// scorer, download error) stays retryable on a later run.
func ReusablePersonImageVerdict(candidate scriptpkg.SegmentAssetCandidate) bool {
	status := strings.ToLower(strings.TrimSpace(candidate.SemanticStatus))
	if status != scriptpkg.CandidateSemanticAccepted && status != scriptpkg.CandidateSemanticRejected {
		return false
	}
	reason := strings.ToLower(strings.TrimSpace(candidate.QualityReason))
	if status == scriptpkg.CandidateSemanticAccepted {
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
	return true
}

// CandidateEligibleForSlot is the canonical semantic gate for an image slot.
// Both the PERSON entity lane and the scene-image lane use it; a caller that
// must certify the depicted identity passes requireVisualIdentity=true. It never
// infers a verdict from discovery metadata alone.
func CandidateEligibleForSlot(candidate scriptpkg.SegmentAssetCandidate, requireVisualIdentity bool) bool {
	if !strings.EqualFold(strings.TrimSpace(candidate.SemanticStatus), scriptpkg.CandidateSemanticAccepted) {
		return false
	}
	if CandidateHasWatermark(candidate) {
		return false
	}
	return !requireVisualIdentity || ReusablePersonImageVerdict(candidate)
}

// watermarkHosts are the stock-agency domains whose public URLs normally
// serve comp/preview images with a visible watermark. The deterministic gate
// rejects them before acquisition; the visual verifier's HasWatermark flag is
// the second layer for every other host.
var watermarkHosts = []string{
	"gettyimages.com",
	"istockphoto.com",
	"shutterstock.com",
	"alamy.com",
	"dreamstime.com",
	"123rf.com",
	"depositphotos.com",
	"stock.adobe.com",
}

// CandidateHasWatermark reports whether the candidate carries a watermark
// verdict or is served from a known watermarked stock-preview surface. It is a
// pure, deterministic predicate: it never downloads pixels and never guesses
// identity.
func CandidateHasWatermark(candidate scriptpkg.SegmentAssetCandidate) bool {
	if candidate.HasWatermark {
		return true
	}
	reason := strings.ToLower(strings.TrimSpace(candidate.WatermarkReason + " " + candidate.QualityReason + " " + candidate.LifecycleFailureReason))
	if strings.Contains(reason, "watermark") {
		return true
	}
	for _, raw := range []string{candidate.SourceURL, candidate.SourcePageURL, candidate.PreviewURL} {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Host == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		for _, domain := range watermarkHosts {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return true
			}
		}
		path := strings.ToLower(parsed.Path)
		if strings.Contains(path, "watermark") || strings.Contains(path, "/comp/") || strings.Contains(path, "comp-") {
			return true
		}
	}
	return false
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
