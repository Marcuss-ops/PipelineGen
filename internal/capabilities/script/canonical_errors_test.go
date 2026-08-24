// Package script (test) — canonical_errors_test.go
//
// TDD coverage for the canonical-error-mapper added in
// PHASE-9-BUG-REMEDIATION-2026-07-04 RED-6 (SCRIPT-T03-001).
//
// Each typed error branch is exercised as a bare sentinel (errors.Is),
// as a typed envelope (errors.As), and wrapped via fmt.Errorf %w to
// lock the wrap-chain handling (production callers wrap with
// `fmt.Errorf("script enqueue: %w", err)` and the mapper MUST still
// classify correctly).
package script

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Typed-sentinel branches (errors.Is + errors.As) ───────────────────────

func TestCanonicalHTTPStatus_PlanInvalid_TypedEnvelope_Returns400(t *testing.T) {
	err := &domainScript.PlanInvalidError{ItemID: "item-123", Details: []string{"no source text"}}
	if got := CanonicalHTTPStatus(err); got != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", got)
	}
	want := `generation: plan invalid: item="item-123"; no source text`
	if got := CanonicalErrorMessage(err); got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestCanonicalHTTPStatus_PlanInvalid_BareSentinel_Returns400(t *testing.T) {
	if got := CanonicalHTTPStatus(domainScript.ErrPlanInvalid); got != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", got)
	}
}

func TestCanonicalHTTPStatus_NoSource_TypedEnvelope_Returns400(t *testing.T) {
	err := &domainScript.NoSourceError{ItemID: "item-456", Reason: "empty source spec"}
	if got := CanonicalHTTPStatus(err); got != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", got)
	}
	if !strings.Contains(CanonicalErrorMessage(err), "no source") {
		t.Fatalf("expected msg to surface reason; got %q", CanonicalErrorMessage(err))
	}
}

func TestCanonicalHTTPStatus_SourceResolutionFailed_TypedEnvelope_Returns400(t *testing.T) {
	err := &domainScript.SourceResolutionError{
		SourceType:  domainScript.SourceClips,
		Query:       "indie ambient",
		ResultCount: 0,
	}
	if got := CanonicalHTTPStatus(err); got != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", got)
	}
}

// ── Unknown / 5xx branches (no leak) ──────────────────────────────────────

func TestCanonicalHTTPStatus_GenerationError_Returns500_Obfuscated(t *testing.T) {
	err := &domainScript.GenerationError{ItemID: "item-789", Phase: "ollama", Inner: errors.New("upstream 502")}
	// ErrGenerationFailed stays 5xx because Ollama is a server-side concern;
	// the client didn't do anything wrong.
	if got := CanonicalHTTPStatus(err); got != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", got)
	}
	if got := CanonicalErrorMessage(err); got != "internal server error" {
		t.Fatalf("want obfuscated message; got %q (would leak %q)", got, err.Error())
	}
}

func TestCanonicalHTTPStatus_PostprocessError_Returns500_Obfuscated(t *testing.T) {
	err := &domainScript.PostprocessError{ItemID: "item-abc", Processor: "voiceover", Inner: errors.New("tts down")}
	if got := CanonicalHTTPStatus(err); got != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", got)
	}
	if got := CanonicalErrorMessage(err); got != "internal server error" {
		t.Fatalf("want obfuscated message; got %q", got)
	}
}

func TestCanonicalHTTPStatus_GenericError_Returns500_Obfuscated(t *testing.T) {
	if got := CanonicalHTTPStatus(errors.New("db disconnect")); got != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", got)
	}
	if got := CanonicalErrorMessage(errors.New("db disconnect")); got != "internal server error" {
		t.Fatalf("want obfuscated message; got %q", got)
	}
}

// ── Wrap-chain (production: errors wrapped via fmt.Errorf %w) ─────────────
//
// Production callers wrap with fmt.Errorf("script enqueue: %w", err) to
// add location / phase context. The mapper MUST still classify correctly.
// Per godlike/07 typed-error contract, errors.As + errors.Is both walk
// the wrap chain via the canonical Unwrap() method on each typed
// envelope. Locking the traversal for ALL THREE 4xx sentinels (not just
// PlanInvalid) protects against future regressions where a maintainer
// drops one disjunct from the switch.

func TestCanonicalHTTPStatus_PlanInvalid_Wrapped_Returns400(t *testing.T) {
	wrapped := fmt.Errorf("script enqueue: %w",
		&domainScript.PlanInvalidError{ItemID: "item-wrap", Details: []string{"impossible sizing"}})
	if got := CanonicalHTTPStatus(wrapped); got != http.StatusBadRequest {
		t.Fatalf("want 400 (wrapped envelope), got %d", got)
	}
	if !strings.Contains(CanonicalErrorMessage(wrapped), "impossible sizing") {
		t.Fatalf("wrapped message should surface typed detail")
	}
}

func TestCanonicalHTTPStatus_NoSource_Wrapped_Returns400(t *testing.T) {
	wrapped := fmt.Errorf("script enqueue: %w",
		&domainScript.NoSourceError{ItemID: "item-wrap", Reason: "empty source spec"})
	if got := CanonicalHTTPStatus(wrapped); got != http.StatusBadRequest {
		t.Fatalf("want 400 (wrapped envelope), got %d", got)
	}
	if !strings.Contains(CanonicalErrorMessage(wrapped), "no source specified") {
		t.Fatalf("wrapped message should surface typed detail")
	}
}

func TestCanonicalHTTPStatus_SourceResolutionFailed_Wrapped_Returns400(t *testing.T) {
	wrapped := fmt.Errorf("script enqueue: %w",
		&domainScript.SourceResolutionError{
			SourceType:  domainScript.SourceClips,
			Query:       "indie ambient",
			ResultCount: 0,
		})
	if got := CanonicalHTTPStatus(wrapped); got != http.StatusBadRequest {
		t.Fatalf("want 400 (wrapped envelope), got %d", got)
	}
	if !strings.Contains(CanonicalErrorMessage(wrapped), "source resolution failed") {
		t.Fatalf("wrapped message should surface typed detail")
	}
}

// ── Nil (defensive) ───────────────────────────────────────────────────────

func TestCanonicalHTTPStatus_Nil_Returns200_EmptyMessage(t *testing.T) {
	if got := CanonicalHTTPStatus(nil); got != http.StatusOK {
		t.Fatalf("want 200, got %d", got)
	}
	if got := CanonicalErrorMessage(nil); got != "" {
		t.Fatalf("want empty message, got %q", got)
	}
}
