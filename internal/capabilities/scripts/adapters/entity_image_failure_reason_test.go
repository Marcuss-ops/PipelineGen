package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestEntityImageFailureReasonDistinguishesNoQueryAndProviderEmpty(t *testing.T) {
	entity := scriptpkg.AnnotatedEntity{CanonicalName: "John Gotti", Type: "PERSON"}
	noQuery := scriptpkg.VidRushSegmentResult{SegmentID: "s1"}
	if got := entityImageFailureReason(entity, noQuery); got != "NO_QUERY" {
		t.Fatalf("no-query reason = %q, want NO_QUERY", got)
	}
	providerEmpty := scriptpkg.VidRushSegmentResult{
		SegmentID: "s1",
		Insights:  scriptpkg.SegmentInsights{ImageQueries: []string{"John Gotti"}},
	}
	if got := entityImageFailureReason(entity, providerEmpty); got != "PROVIDER_EMPTY" {
		t.Fatalf("provider-empty reason = %q, want PROVIDER_EMPTY", got)
	}
}
