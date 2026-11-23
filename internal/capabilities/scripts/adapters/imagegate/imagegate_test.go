package imagegate

// imagegate_test.go pins the behaviour of the canonical image-slot gate and of
// the reusable-verdict predicate underneath it.
//
// The rule the gate exists for is the one that has no other guard: discovery
// metadata — a matching label, adequate dimensions, a successful download — is
// NOT evidence that the pixels depict the requested person. Older catalog rows
// wrote `accepted` with exactly that metadata-only reason, so the gate must
// force those rows through visual verification instead of reusing them as an
// identity image (BON-02: one canonical eligibility helper, and it never infers
// a verdict from discovery metadata).

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func semanticCandidate(status, reason string) scriptpkg.SegmentAssetCandidate {
	return scriptpkg.SegmentAssetCandidate{SemanticStatus: status, QualityReason: reason}
}

func TestCandidateEligibleForSlotRejectsEverythingButAccepted(t *testing.T) {
	for _, status := range []string{"", "pending", entitycatalog.CandidateSemanticRejected} {
		if CandidateEligibleForSlot(semanticCandidate(status, "ollama visual identity verification confirmed"), false) {
			t.Fatalf("status %q must never fill a scene slot", status)
		}
		if CandidateEligibleForSlot(semanticCandidate(status, "ollama visual identity verification confirmed"), true) {
			t.Fatalf("status %q must never fill a person slot", status)
		}
	}
	if !CandidateEligibleForSlot(semanticCandidate(entitycatalog.CandidateSemanticAccepted, ""), false) {
		t.Fatal("an accepted candidate must fill a scene slot without a visual-identity requirement")
	}
}

// TestCandidateEligibleForSlotNeverTrustsDiscoveryMetadata is the point of the
// gate: `accepted` alone is not a pixel verdict. A row whose reason only reports
// discovery facts must fail the person lane and pass only the scene lane.
func TestCandidateEligibleForSlotNeverTrustsDiscoveryMetadata(t *testing.T) {
	metadataOnly := []string{
		"",
		"matched the query label with adequate dimensions",
		"downloaded 1200x800 jpeg, dominant subject detected",
		"duckduckgo top hit for \"ada lovelace\"",
	}
	for _, reason := range metadataOnly {
		candidate := semanticCandidate(entitycatalog.CandidateSemanticAccepted, reason)
		if !CandidateEligibleForSlot(candidate, false) {
			t.Fatalf("reason %q: an accepted candidate must still fill a scene slot", reason)
		}
		if CandidateEligibleForSlot(candidate, true) {
			t.Fatalf("reason %q: discovery metadata must not certify a person identity", reason)
		}
	}
}

func TestCandidateEligibleForSlotAcceptsRealPixelVerdicts(t *testing.T) {
	verified := []string{
		"ollama visual identity verification confirmed the requested person and one dominant subject",
		"manual visual identity review confirmed",
		"deterministic identity verification confirmed",
		"pinned by media_manifest.v1: offline sha256 verify",
	}
	for _, reason := range verified {
		candidate := semanticCandidate(entitycatalog.CandidateSemanticAccepted, reason)
		if !CandidateEligibleForSlot(candidate, true) {
			t.Fatalf("reason %q: a pixel verdict must certify the person identity", reason)
		}
	}
}

// TestReusablePersonImageVerdictKeepsOutagesRetryable separates a quality
// rejection from an infrastructure failure: a scorer that was down has not
// judged the pixels, so the row must stay retryable instead of being cached as
// rejected forever.
func TestReusablePersonImageVerdictKeepsOutagesRetryable(t *testing.T) {
	for _, reason := range []string{
		"verification failed: ollama returned 502",
		"verification unavailable",
		"image could not be downloaded",
		"vision model is not configured",
	} {
		candidate := semanticCandidate(entitycatalog.CandidateSemanticRejected, reason)
		if ReusablePersonImageVerdict(candidate) {
			t.Fatalf("reason %q: an outage is not a verdict and must stay retryable", reason)
		}
	}
	if !ReusablePersonImageVerdict(semanticCandidate(entitycatalog.CandidateSemanticRejected, "depicts a different person")) {
		t.Fatal("a definitive rejection is a reusable verdict")
	}
	if ReusablePersonImageVerdict(semanticCandidate("pending", "ollama visual identity verification confirmed")) {
		t.Fatal("an undecided candidate is not a reusable verdict")
	}
}

func TestAllCandidatesHaveReusableVerdict(t *testing.T) {
	if !AllCandidatesHaveReusableVerdict(nil) {
		t.Fatal("an empty candidate list has nothing left to verify")
	}
	mixed := []scriptpkg.SegmentAssetCandidate{
		semanticCandidate(entitycatalog.CandidateSemanticAccepted, "ollama visual identity verification confirmed"),
		semanticCandidate("pending", ""),
	}
	if AllCandidatesHaveReusableVerdict(mixed) {
		t.Fatal("one undecided candidate must force a download")
	}
}

func TestFilterVerifiedPersonImageCandidatesIsFailClosed(t *testing.T) {
	in := []scriptpkg.SegmentAssetCandidate{
		semanticCandidate(entitycatalog.CandidateSemanticAccepted, "ollama visual identity verification confirmed"),
		semanticCandidate(entitycatalog.CandidateSemanticAccepted, "matched the query label"),
		semanticCandidate("pending", ""),
	}
	out := FilterVerifiedPersonImageCandidates(in)
	if len(out) != 1 {
		t.Fatalf("filtered %d candidates, want only the visually verified one: %+v", len(out), out)
	}
	if out[0].QualityReason != "ollama visual identity verification confirmed" {
		t.Fatalf("kept the wrong candidate: %+v", out[0])
	}
}
