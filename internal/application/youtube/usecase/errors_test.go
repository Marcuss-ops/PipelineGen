// Package usecase — errors_test.go pins the Commit 2/6
// Correttezza #9 typed error taxonomy. The verdict's P1 #14
// explicitly bans the legacy `strings.Contains`-based
// retryable-error classifier in the job handler; this file
// ships the canonical typed alternative.
//
// What this test asserts:
//  1. *ExtractionError is a typed error with Code, Retryable,
//     Message, and Cause fields.
//  2. errors.As can extract a *ExtractionError from a wrapped
//     error chain (the canonical job-handler invocation shape).
//  3. IsTransientExtractionError uses the typed Code + Retryable
//     fields when an *ExtractionError is present (no string match).
//  4. IsTransientExtractionError falls back to substring matching
//     for raw port errors (the legacy fallback path).
//  5. FailureCode values are stable string literals (the
//     Prometheus counter + log-scraper contract).
package usecase

import (
	"errors"
	"fmt"
	"testing"
)

// TestExtractionError_CarriesCodeAndRetryable pins the typed
// error shape: Code + Retryable + Message + Cause.
func TestExtractionError_CarriesCodeAndRetryable(t *testing.T) {
	cause := errors.New("disk full")
	ee := NewExtractionError(FailureCodeHashFailed, false, "md5 failed", cause)

	if ee.Code != FailureCodeHashFailed {
		t.Errorf("Code: want %q got %q", FailureCodeHashFailed, ee.Code)
	}
	if ee.Retryable {
		t.Errorf("Retryable: want false got true")
	}
	if ee.Message != "md5 failed" {
		t.Errorf("Message: want %q got %q", "md5 failed", ee.Message)
	}
	if !errors.Is(ee, cause) {
		t.Errorf("errors.Is must unwrap to cause (via Unwrap)")
	}
}

// TestExtractionError_FormatIncludesCodeAndCause pins the
// Error() string contract: "<Code>: <Message>: <Cause>".
// Used by log scrapers + Prometheus label generators.
func TestExtractionError_FormatIncludesCodeAndCause(t *testing.T) {
	cause := errors.New("permission denied")
	ee := NewExtractionError(FailureCodeDriveUploadFailed, true, "upload to drive failed", cause)

	got := ee.Error()
	want := "drive_upload_failed: upload to drive failed: permission denied"
	if got != want {
		t.Errorf("Error() format: want %q got %q", want, got)
	}
}

// TestIsTransientExtractionError_TypedPath pins the canonical
// retryable classifier: a *ExtractionError with Retryable=true
// must classify as transient; a *ExtractionError with
// Retryable=false must classify as terminal.
func TestIsTransientExtractionError_TypedPath(t *testing.T) {
	transient := NewExtractionError(FailureCodeVideoProcessingFailed, true, "transient", nil)
	terminal := NewExtractionError(FailureCodeEmptyLocalPath, false, "terminal", nil)

	if !IsTransientExtractionError(transient) {
		t.Errorf("typed Retryable=true MUST classify as transient")
	}
	if IsTransientExtractionError(terminal) {
		t.Errorf("typed Retryable=false MUST classify as terminal (Correttezza #9 — typed-path must NOT fall through to substring match)")
	}
}

// TestIsTransientExtractionError_WrappedTypedPath pins the
// wrapped case: the typed error is wrapped via fmt.Errorf %w
// and errors.As must still find it. The job handler unwraps
// via errors.As in the canonical shape.
func TestIsTransientExtractionError_WrappedTypedPath(t *testing.T) {
	inner := NewExtractionError(FailureCodeWriterFailed, false, "writer err", nil)
	wrapped := fmt.Errorf("commit: %w", inner)

	if IsTransientExtractionError(wrapped) {
		t.Errorf("wrapped Retryable=false MUST classify as terminal (errors.As traversal)")
	}
	if !errors.Is(wrapped, inner) {
		t.Errorf("errors.Is must traverse the wrap chain to find inner")
	}
}

// TestIsTransientExtractionError_NonTypedIsTerminal pins the
// FASE 6 Cut 6.1.D invariant: a raw port error that reaches
// IsTransientExtractionError WITHOUT an *ExtractionError envelope
// classifies as NON-transient. The substring fallback to
// pkg/retry.IsTransient was REMOVED; the typed path is the only
// authoritative classifier. A raw 429 / 503 / "rate limit" error
// that slipped past the use case wrap (canonical remediation:
// wrap it in NewExtractionError upstream) is classified as
// terminal to make the gap visible at the classification site
// rather than silently retrying via a substring heuristic.
func TestIsTransientExtractionError_NonTypedIsTerminal(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// All-non-typed: every shape is terminal, even when the
		// message *looks* transient to a substring classifier.
		// This is the Correttezza #9 + FASE 6 Cut 6.1.D property:
		// typed-path strict; no fallback layer.
		{"timeout", errors.New("request timeout after 30s"), false},
		{"429", errors.New("HTTP error 429: rate limit exceeded"), false},
		{"503", errors.New("service unavailable: 503"), false},
		{"connection refused", errors.New("dial tcp: connection refused"), false},
		{"rate limit", errors.New("rate limit exceeded"), false},
		{"temporarily unavailable", errors.New("backend temporarily unavailable"), false},
		// Terminal-shapes stay terminal.
		{"terminal-permission", errors.New("permission denied"), false},
		{"terminal-not-found", errors.New("file not found"), false},
		{"terminal-validation", errors.New("invalid input"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransientExtractionError(tc.err); got != tc.want {
				t.Errorf("IsTransientExtractionError(%q): want %v got %v (FASE 6 Cut 6.1.D: raw-typed-only classifier; substring messages must NOT classify as transient)", tc.err.Error(), tc.want, got)
			}
		})
	}
}

// TestIsTransientExtractionError_NilError pins the nil case.
func TestIsTransientExtractionError_NilError(t *testing.T) {
	if IsTransientExtractionError(nil) {
		t.Errorf("nil error MUST classify as non-transient")
	}
}

// TestFailureCode_StableStringLiterals pins the canonical
// literal values. These are the contract surface for log
// scrapers + Prometheus counters; renaming them is a breaking
// change.
func TestFailureCode_StableStringLiterals(t *testing.T) {
	codes := map[FailureCode]string{
		FailureCodeEmptyLocalPath:        "empty_local_path",
		FailureCodeInvalidLocalArtifact:  "invalid_local_artifact",
		FailureCodeHashFailed:            "hash_failed",
		FailureCodeDurationOutOfRange:    "duration_out_of_range",
		FailureCodeInvalidTimestamp:      "invalid_timestamp",
		FailureCodeVideoProcessingFailed: "video_processing_failed",
		FailureCodeDriveUploadFailed:     "drive_upload_failed",
		FailureCodeWriterFailed:          "writer_failed",
	}
	for code, want := range codes {
		if string(code) != want {
			t.Errorf("FailureCode %q: want literal %q (breaking-change for log scrapers + counters)", code, want)
		}
	}
}
