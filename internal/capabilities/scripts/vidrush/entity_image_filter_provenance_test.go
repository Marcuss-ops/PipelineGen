package vidrush

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestPersonImageSourceNameMismatchRejectsMultiNameFilename(t *testing.T) {
	got := personImageSourceNameMismatch("John Gotti", "https://images.example/john-gotti-sammy-gravano.jpg")
	if got == "" {
		t.Fatal("a filename naming a second person must not pass as John Gotti")
	}
}

func TestPersonImageSourceNameMismatchAllowsCanonicalQualifiedFilename(t *testing.T) {
	for _, source := range []string{
		"https://images.example/john-gotti-photo-2020.jpg",
		"https://images.example/john-gotti-1990.jpg",
		"https://images.example/john-gotti-portrait.jpg",
	} {
		if got := personImageSourceNameMismatch("John Gotti", source); got != "" {
			t.Fatalf("canonical filename %q rejected: %s", source, got)
		}
	}
	if got := personImageSourceNameMismatch("José María Aznar", "https://www.21news.be/wp-content/uploads/2026/02/Jose-Maria-Aznar-scaled.jpg"); got != "" {
		t.Fatalf("compound surname filename rejected: %s", got)
	}
	if got := personImageSourceNameMismatch("Conan O'Brien", "https://www.peopleandmedia.com/wp-content/uploads/2026/05/conan-obrien-portrait.jpg"); got != "" {
		t.Fatalf("apostrophe surname filename rejected: %s", got)
	}
}

func TestPersonImageSourceIdentityPenaltyKeepsMultiPersonCandidateRankable(t *testing.T) {
	const source = "https://images.example/john-gotti-sammy-gravano.jpg"
	if penalty := PersonImageSourceIdentityPenalty("John Gotti", source); penalty <= 0 {
		t.Fatalf("multi-person filename must carry a ranking penalty, got %v", penalty)
	}
	if penalty := PersonImageSourceIdentityPenalty("John Gotti", "https://images.example/john-gotti-portrait.jpg"); penalty != 0 {
		t.Fatalf("single-person filename must not be penalised, got %v", penalty)
	}
	identity := entitycatalog.PersonIdentity{CanonicalName: "John Gotti", CanonicalEntityID: "person:john-gotti"}
	accepted, score, _, reason := evaluatePersonImageCandidate(identity, scriptpkg.SegmentAssetCandidate{SourceURL: source})
	if !accepted {
		t.Fatalf("multi-person candidate must stay eligible for pixel verification: %s", reason)
	}
	if score >= 0 || !strings.Contains(reason, "ranked below single-person sources") {
		t.Fatalf("expected negative score and ranking reason, got score=%v reason=%q", score, reason)
	}
}
