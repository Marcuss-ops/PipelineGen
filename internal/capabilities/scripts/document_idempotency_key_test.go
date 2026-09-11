package scriptgeneration

import "testing"

// TestDocumentIdempotencyKeys_Parity pins the two Drive idempotency-key
// conventions used by the two live document publication paths (durable runner
// and post-processor). If either separator ever changes, an existing Google Doc
// is re-keyed on the next publication and a duplicate is created — so this test
// makes any such change a deliberate, reviewed act instead of a silent edit.
func TestDocumentIdempotencyKeys_Parity(t *testing.T) {
	const runID = "run-123"
	const lang Language = "it"

	if got, want := DocumentIdempotencyKey(runID, lang), "run-123:it"; got != want {
		t.Fatalf("DocumentIdempotencyKey = %q, want %q (durable-runner Drive key convention)", got, want)
	}
	if got, want := DocumentPostProcessorIdempotencyKey(runID, string(lang)), "run-123-it"; got != want {
		t.Fatalf("DocumentPostProcessorIdempotencyKey = %q, want %q (post-processor Drive key convention)", got, want)
	}
}

// TestDocumentIdempotencyKeys_DeterministicAcrossRestarts proves neither path
// can re-key an existing document: for the same logical document the derivation
// is stable, trimming cannot produce a second key, and a different language
// (a different document) never collides with it.
func TestDocumentIdempotencyKeys_DeterministicAcrossRestarts(t *testing.T) {
	runnerFirst := DocumentIdempotencyKey("run-1", "it")
	if runnerRetry := DocumentIdempotencyKey("run-1", "it"); runnerRetry != runnerFirst {
		t.Fatalf("runner key not stable: %q != %q", runnerFirst, runnerRetry)
	}
	if DocumentIdempotencyKey(" run-1 ", " it ") != runnerFirst {
		t.Fatalf("runner key must be trim-stable: %q", DocumentIdempotencyKey(" run-1 ", " it "))
	}
	if DocumentIdempotencyKey("run-1", "en") == runnerFirst {
		t.Fatal("distinct languages must not share a durable-runner Drive idempotency key")
	}

	processorFirst := DocumentPostProcessorIdempotencyKey("run-1", "it")
	if processorRetry := DocumentPostProcessorIdempotencyKey("run-1", "it"); processorRetry != processorFirst {
		t.Fatalf("post-processor key not stable: %q != %q", processorFirst, processorRetry)
	}
	if DocumentPostProcessorIdempotencyKey("run-1", "en") == processorFirst {
		t.Fatal("distinct languages must not share a post-processor Drive idempotency key")
	}
}

// TestResolveDocumentIdempotencyKey_ExplicitOverrideWins pins that a caller with
// an established convention keeps it (never silently re-keyed), and that a blank
// override falls back to the canonical runner derivation.
func TestResolveDocumentIdempotencyKey_ExplicitOverrideWins(t *testing.T) {
	override := DocumentInput{RunID: "run-1", Language: "it", IdempotencyKey: "legacy-key"}
	if got := ResolveDocumentIdempotencyKey(override); got != "legacy-key" {
		t.Fatalf("ResolveDocumentIdempotencyKey = %q, want the explicit override", got)
	}

	blank := DocumentInput{RunID: "run-1", Language: "it", IdempotencyKey: "   "}
	if got, want := ResolveDocumentIdempotencyKey(blank), DocumentIdempotencyKey("run-1", "it"); got != want {
		t.Fatalf("blank override must fall back to the canonical runner key: got %q want %q", got, want)
	}

	none := DocumentInput{RunID: "run-1", Language: "it"}
	if got := ResolveDocumentIdempotencyKey(none); got != "run-1:it" {
		t.Fatalf("ResolveDocumentIdempotencyKey = %q, want %q", got, "run-1:it")
	}
}
