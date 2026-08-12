package scriptgeneration

import "strings"

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

	// Check for known error patterns in the error message.
	// This is a lightweight heuristic; a future improvement could
	// use typed error interfaces (e.g. RetryableError, TransientError).
	switch {
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

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
