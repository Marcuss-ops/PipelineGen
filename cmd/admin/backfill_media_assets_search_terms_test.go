package main

import (
	"strings"
	"testing"
)

// ── parseBackfillArgs ──────────────────────────────────────────────────
//
// Positive tests pin the happy-path flag broadcast; negative tests pin
// the error contract for malformed args. The Source field is intentionally
// recorded verbatim (no enum validation) because the backfill SQL filter
// silently degrades an unrecognized value to zero rows — operators see
// the gap in the dry-run counter rather than an error, which is the
// "be liberal in what you accept" stance documented in the cmd header.
// SourceRecordedVerbatim pins that behavior so a future enum-validation
// patch breaks loudly instead of regressing quietly.

func TestParseBackfillArgs_Defaults(t *testing.T) {
	t.Parallel()

	deps, err := parseBackfillArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deps.Apply {
		t.Fatalf("Apply=true; default false expected")
	}
	if deps.JSON {
		t.Fatalf("JSON=true; default false expected")
	}
	if deps.Limit != 0 {
		t.Fatalf("Limit=%d; default 0 expected", deps.Limit)
	}
	if deps.BatchSize != defaultBackfillBatchSize {
		t.Fatalf("BatchSize=%d; default %d expected", deps.BatchSize, defaultBackfillBatchSize)
	}
	if deps.Source != "" {
		t.Fatalf("Source=%q; default empty expected", deps.Source)
	}
}

func TestParseBackfillArgs_AllFlags(t *testing.T) {
	t.Parallel()

	deps, err := parseBackfillArgs([]string{
		"--apply", "--json",
		"--limit=100", "--batch=200",
		"--source=artlist",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deps.Apply {
		t.Fatalf("Apply=false; want true")
	}
	if !deps.JSON {
		t.Fatalf("JSON=false; want true")
	}
	if deps.Limit != 100 {
		t.Fatalf("Limit=%d; want 100", deps.Limit)
	}
	if deps.BatchSize != 200 {
		t.Fatalf("BatchSize=%d; want 200", deps.BatchSize)
	}
	if deps.Source != "artlist" {
		t.Fatalf("Source=%q; want artlist", deps.Source)
	}
}

func TestParseBackfillArgs_SourceRecordedVerbatim(t *testing.T) {
	t.Parallel()

	// --source=garbage is intentionally NOT validated against the
	// {artlist, youtube, stock, image} enum. The SQL filter will simply
	// return zero rows; the operator sees the gap in the dry-run counter.
	// This test pins that behavior so a future enum-validation patch
	// breaks loudly instead of silently.
	deps, err := parseBackfillArgs([]string{"--source=garbage"})
	if err != nil {
		t.Fatalf("unexpected error on verbatim source; got %v", err)
	}
	if deps.Source != "garbage" {
		t.Fatalf("verbatim pass-through failed; got %q want %q", deps.Source, "garbage")
	}
}

func TestParseBackfillArgs_NegativeLimit(t *testing.T) {
	t.Parallel()

	_, err := parseBackfillArgs([]string{"--limit=-1"})
	if err == nil {
		t.Fatalf("expected error on --limit=-1")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("error text should mention non-negative; got %v", err)
	}
}

func TestParseBackfillArgs_NonNumericLimit(t *testing.T) {
	t.Parallel()

	_, err := parseBackfillArgs([]string{"--limit=abc"})
	if err == nil {
		t.Fatalf("expected error on --limit=abc")
	}
	if !strings.Contains(err.Error(), "integer") {
		t.Fatalf("error text should mention integer; got %v", err)
	}
}

func TestParseBackfillArgs_NegativeBatch(t *testing.T) {
	t.Parallel()

	_, err := parseBackfillArgs([]string{"--batch=-100"})
	if err == nil {
		t.Fatalf("expected error on --batch=-100")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("error text should mention non-negative; got %v", err)
	}
}

func TestParseBackfillArgs_ZeroBatchFallsBackToDefault(t *testing.T) {
	t.Parallel()

	// --batch=0 is caught by the post-loop guard in parseBackfillArgs;
	// the parser silently coerces 0 back to defaultBackfillBatchSize
	// rather than rejecting the arg. Operators who pass --batch=0 usually
	// mean "use the server default" — accept it.
	deps, err := parseBackfillArgs([]string{"--batch=0"})
	if err != nil {
		t.Fatalf("unexpected error on --batch=0; got %v", err)
	}
	if deps.BatchSize != defaultBackfillBatchSize {
		t.Fatalf("BatchSize=%d; default %d expected on zero", deps.BatchSize, defaultBackfillBatchSize)
	}
}
