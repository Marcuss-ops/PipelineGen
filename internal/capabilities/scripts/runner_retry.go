package scriptgeneration

import (
	"errors"
	"strings"
)

// deriveErrorCode extracts a stable machine-readable error code from
// the error chain and the failing stage. Returns a canonical string
// suitable for persisting as GenerationRun.ErrorCode.
//
// P0 verdetto: error codes must be stable so clients (retry bots,
// dashboards, monitoring) can branch on them reliably.
func deriveErrorCode(err error, stage Stage) string {
	if err == nil {
		return string(stage) + "_FAILED"
	}
	errStr := err.Error()

	// Typed errors take precedence over message heuristics so the durable
	// error code remains stable even when provider details change.
	if errors.Is(err, ErrMediaPreflight) {
		return "MEDIA_PREFLIGHT_FAILED"
	}
	// A remote render that is still running is a DEFERRAL, not an incident: the
	// code is stable so a retry bot can tell "come back later" apart from a real
	// failure. It must precede the message heuristics below, which would
	// otherwise classify the wrapped transport timeout as PROVIDER_TIMEOUT.
	if errors.Is(err, ErrFinalJobPending) {
		return "REMOTE_RENDER_PENDING"
	}

	// Check for known error patterns in the error message.
	// This is a lightweight heuristic; a future improvement could
	// use typed error interfaces (e.g. RetryableError, TransientError).
	switch {
	case containsAny(errStr, "INCOMPLETE_RENDER_SET"):
		return "INCOMPLETE_RENDER_SET"
	// The semantic certification verdict (CERTIFIED=false) is the most actionable
	// terminal class in the failure log — IMAGE FANOUT / CROSS-SCENE REUSE name
	// the exact fix — but the generic heuristics below could never see it: the
	// "render" substring inside the message routed it to ENQUEUE_FAILED, hiding
	// the editorial root cause under an infrastructure label. It must be
	// classified BEFORE those heuristics for the same reason the typed errors
	// above are.
	case containsAny(errStr, "CERTIFIED=false"):
		return "SEMANTIC_CERTIFICATION_FAILED"
	case containsAny(errStr, "timeout", "deadline exceeded", "context deadline"):
		return "PROVIDER_TIMEOUT"
	case containsAny(errStr, "unavailable", "not configured", "not initialized", "not found", "connection refused"):
		return "PROVIDER_UNAVAILABLE"
	case containsAny(errStr, "invalid response", "malformed", "decode failed", "parse error"):
		return "PROVIDER_BAD_RESPONSE"
	case containsAny(errStr, "empty", "zero", "no scenes", "no results"):
		return "EMPTY_RESULT"
	case containsAny(errStr, "generate scene text failed"):
		return "TEXT_GENERATION_FAILED"
	case containsAny(errStr, "translate"):
		return "TRANSLATION_FAILED"
	case containsAny(errStr, "voiceover"):
		return "VOICEOVER_FAILED"
	case containsAny(errStr, "document", "upsert", "google doc"):
		return "DOCUMENT_FAILED"
	case containsAny(errStr, "enqueue", "render", "worker"):
		return "ENQUEUE_FAILED"
	default:
		return string(stage) + "_FAILED"
	}
}

// isPermanentMediaPreflightFailure reports a static VidRush registration
// failure that cannot be repaired by retrying within the same frozen runtime
// composition. Other preflight failures retain the normal retry policy.
func isPermanentMediaPreflightFailure(err error) bool {
	var preflightErr *MediaPreflightError
	if !errors.As(err, &preflightErr) || preflightErr == nil {
		return false
	}
	for _, failure := range preflightErr.Result.Failures {
		if failure.Category == "vidrush_provider" {
			return true
		}
	}
	return false
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
