package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// TestIndexingHandler_PayloadParseIsTerminal locks in QDRANT-002
// item G: a malformed JSON payload is classified as a terminal
// error by the Pool's IsTerminal classifier. Without this wrapper
// the Pool would burn max_attempts on a payload the producer
// cannot repair — turning a real config bug into a noisy retry
// storm in dead_letter with no signal.
//
// Companion test to outboxevents/errors_test.go::TestIsTerminal_TypedError.
func TestIndexingHandler_PayloadParseIsTerminal(t *testing.T) {
	h := &outboxhandlers.IndexingHandler{}
	evt := outboxevents.Event{
		ID:           100,
		EventType:    outboxevents.EventAssetIndexRequested,
		AggregateID:  "asset-parse-fail",
		PayloadJSON:  `{ not json`,
		AttemptCount: 1,
	}

	err := h.Handle(context.Background(), evt)
	if err == nil {
		t.Fatal("expected error on malformed JSON; got nil — caller cannot dead-letter")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("malformed payload must be classified as terminal "+
			"(preventing max_attempts retry storm); got non-terminal error: %v", err)
	}
}

// TestIndexingHandler_EmptyAssetIDIsTerminal verifies the second
// half of the contract: a syntactically valid payload with no
// asset_id is terminal for the same reason — retrying won't bring
// the asset_id into existence.
func TestIndexingHandler_EmptyAssetIDIsTerminal(t *testing.T) {
	h := &outboxhandlers.IndexingHandler{}
	evt := outboxevents.Event{
		ID:           101,
		EventType:    outboxevents.EventAssetIndexRequested,
		AggregateID:  "asset-empty-id",
		PayloadJSON:  `{}`,
		AttemptCount: 1,
	}

	err := h.Handle(context.Background(), evt)
	if err == nil {
		t.Fatal("expected error on empty asset_id; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("empty asset_id must be classified as terminal; got: %v", err)
	}
}

// TestIndexingHandler_PreservesWrappedCause verifies the typed
// *TerminalError's Unwrap chain is intact: log greps and operator
// runbooks searching for the underlying json.SyntaxError /
// underlying cause must continue to work after the terminal wrap.
//
// The probe uses errors.As with the canonical underlying type
// emitted by encoding/json (json.SyntaxError). If a future PR
// breaks the wrap — e.g. by switching to %s instead of %w in
// fmt.Errorf, or by replacing the typed wrap with a plain string —
// the assertion fails.
func TestIndexingHandler_PreservesWrappedCause(t *testing.T) {
	h := &outboxhandlers.IndexingHandler{}
	evt := outboxevents.Event{
		EventType:   outboxevents.EventAssetIndexRequested,
		AggregateID: "asset-cause",
		PayloadJSON: `{ this is not valid json at all`,
	}
	err := h.Handle(context.Background(), evt)
	if err == nil {
		t.Fatal("expected error")
	}
	// Chain probe: errors.As must reach json.SyntaxError. This is
	// the canonical underlying type encoding/json emits on a
	// malformed payload (the handler wraps it via fmt.Errorf %w).
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("errors.As(err, *json.SyntaxError) failed; " +
			"Unwrap chain is broken — terminal wrap must preserve the inner cause for log greppability")
	}
	if msg := err.Error(); !strings.Contains(msg, "payload parse") {
		t.Errorf("error message lost the underlying cause hint: %q", msg)
	}
}
