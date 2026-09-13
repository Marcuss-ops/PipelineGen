package job

// payload_match.go owns the canonical PAYLOAD-SCOPED CLAIM contract.
//
// Why it exists: a job's phase sometimes travels inside its payload (for
// example clip.render carries render_phase=submit|settle) because the
// continuation must be the SAME canonical job type as the job it resumes. That
// makes type-based worker capabilities unable to give the two phases separate
// budgets: a worker advertising clip.render claims both. A PayloadMatch narrows
// a claim to the jobs whose payload carries specific key=value pairs, so one
// job type can be split across independently budgeted pools WITHOUT a second
// job type, a new job id, or a migration.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PayloadMatch narrows a claim to jobs whose payload carries every listed
// key=value pair. An empty (or nil) matcher means "no payload restriction" and
// is byte-identical to the historical claim behaviour.
type PayloadMatch map[string]string

// PayloadScopedClaimer is the OPTIONAL store capability a claim loop uses when a
// PayloadMatch is configured. It is deliberately NOT part of the Store
// interface: a store that cannot scope claims simply does not implement it, and
// a scoped claim against such a store fails closed (never silently returns an
// unscoped job).
type PayloadScopedClaimer interface {
	ClaimNextMatching(ctx context.Context, workerID string, leaseTTL time.Duration, types []string, match PayloadMatch) (*Job, error)
}

// ValidatePayloadMatch fails closed on an unusable matcher: every key must be
// non-empty. Values may be empty (they match an empty JSON string). An empty
// matcher is valid.
func ValidatePayloadMatch(match PayloadMatch) error {
	for key := range match {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("payload match key must be non-empty")
		}
	}
	return nil
}

// MatchesPayload reports whether payload carries every key=value pair in match.
// Values are compared through their canonical scalar projection (string,
// number, boolean), so render_phase="settle" and attempt=1 both match their
// JSON forms. An empty matcher matches any payload; a malformed or empty
// payload matches only an empty matcher.
func MatchesPayload(payload json.RawMessage, match PayloadMatch) bool {
	if len(match) == 0 {
		return true
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return false
	}
	for key, want := range match {
		got, ok := fields[key]
		if !ok {
			return false
		}
		if payloadScalar(got) != want {
			return false
		}
	}
	return true
}

// payloadScalar projects a decoded JSON scalar onto its stable text form. Nested
// objects/arrays stringify via fmt, which never equals a caller-declared scalar
// in practice; that is intentional (a match is an equality assertion, not a
// structural search).
func payloadScalar(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}
