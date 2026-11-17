package rustexec

import (
	"context"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"go.uber.org/zap"
)

// TestSemanticEngineRealBinaryLocalizesIdentities proves the Go adapter and the
// Rust worker agree on the wire contract. It skips when the installed binary
// predates the operation, so a pinned release can be bumped independently.
func TestSemanticEngineRealBinaryLocalizesIdentities(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine release binary not installed; run make rust-fetch")
	}
	executor := NewExecutor(binary, resolveFFmpegBinary(), zap.NewNop())
	probe, _, err := executor.Run(context.Background(), []byte(`{"operation":"lexicon_profile","language":"it"}`))
	if err != nil || strings.Contains(string(probe), "unknown operation") {
		t.Skip("semantic-engine binary predates lexicon_profile/localize_identities")
	}
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	text := "To opowieść o Mike’a Tysona."
	matches, err := adapter.MatchLocalizedIdentities(context.Background(), "pl", text, []scriptgen.LocalizedSourceEntity{
		{Kind: "PERSON", CanonicalName: "Mike Tyson", Text: "Mike Tyson"},
	})
	if err != nil {
		t.Fatalf("MatchLocalizedIdentities: %v", err)
	}
	if len(matches) != 1 || matches[0].Surface != "Mike’a Tysona" {
		t.Fatalf("matches = %+v, want the Polish inflected surface", matches)
	}

	completed, err := adapter.CompleteLocalizedIdentities(
		context.Background(), "ru",
		"Майк Тайсон встретил Ивана Петрова.",
		[]scriptgen.LocalizedSourceEntity{
			{Kind: "PERSON", CanonicalName: "Mike Tyson", Text: "Mike Tyson"},
			{Kind: "PERSON", CanonicalName: "Ivan Petrov", Text: "Ivan Petrov"},
		},
		[]string{"Майк Тайсон", "Ивана Петрова"},
		nil,
	)
	if err != nil {
		t.Fatalf("CompleteLocalizedIdentities: %v", err)
	}
	if len(completed) != 2 || completed[0].SourceIndex != 0 || completed[1].SourceIndex != 1 {
		t.Fatalf("completed = %+v, want two ordered source identities", completed)
	}
}
