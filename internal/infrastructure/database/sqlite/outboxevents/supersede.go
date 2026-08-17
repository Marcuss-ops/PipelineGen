// Package outboxevents — SupersedeError + IsSupersede classifier.
//
// Handlers can return one of FOUR distinct shapes (extending the PR1
// contract landed alongside this file):
//
//   - nil                                          → MarkCompleted.
//   - retryable error (any non-nil error WITHOUT the  →
//     terminal/supersede classifiers)               MarkFailed with
//     exponential backoff;
//     dead_letter via
//     max_attempts.
//   - *TerminalError (or "(terminal)" breadcrumb)  → MarkDeadLetter immediately.
//   - *SupersedeError                              → MarkSuperseded immediately,
//     status='superseded'.
//
// The supersede path is success-like from the producer's perspective:
// another event already covers the same aggregate at a newer version,
// so re-indexing with the stale payload would burn a Qdrant upsert and
// double-count the index_event metrics for no gain. Pool.processEvent
// routes supersede errors to a distinct terminal status — NOT
// dead_letter — so operator dashboards can tell "the producer is
// broken" apart from "the upstream streamed a fresh update and the old
// events are no-ops".
//
// Ticket reference: QDRANT-002 checklist item F —
// "Verificare che source_version dell'evento sia ancora corrente.
//
//	Se l'evento è obsoleto, marcarlo SUPERSEDED senza indicizzare dati vecchi."
package outboxevents

import (
	"errors"
	"fmt"
)

// SupersedeError signals an event was obsoleted by a newer version of
// the same aggregate. Pool.processEvent recognises it via errors.As
// and routes the row to MarkSuperseded (status='superseded') rather
// than MarkFailed or MarkCompleted.
//
// The pair (AssetID, Current, Expected) is recorded so operators can
// audit the discrepancy without joining into the payload JSON —
// last_error stores the human-readable Reason verbatim via
// MarkSuperseded(... errMsg string).
type SupersedeError struct {
	// AssetID is the canonical media_assets.id the event targeted.
	AssetID string
	// Current is the current canonical index_revision read from the
	// asset (media_assets.metadata_json.$.index_revision; legacy rows
	// fall back to content_hash via SourceVersionFor). Empty when the
	// asset row is absent — the handler may then fall through to a
	// different terminal branch.
	Current string
	// Expected is the index_revision embedded in the event payload (the
	// canonical supersede fingerprint; the legacy source_version alias
	// falls back at parse time).
	Expected string
	// Reason is a human-readable summary; surfaced via err.Error()
	// and persisted into outbox_events.last_error.
	Reason string
}

// Error returns the canonical message. Pool.processEvent only needs
// the .Error() output to write into last_error; the typed *SupersedeError
// itself is the routing signal (IsSupersede uses errors.As).
func (e *SupersedeError) Error() string {
	if e == nil {
		return "outbox superseded"
	}
	if e.Reason != "" {
		return fmt.Sprintf("outbox superseded: asset=%s — %s", e.AssetID, e.Reason)
	}
	return fmt.Sprintf("outbox superseded: asset=%s source_version=%q current=%q",
		e.AssetID, e.Expected, e.Current)
}

// NewSupersede builds a SupersedeError with a canonical formatted
// reason. Returns a non-nil error whenever assetID is non-empty;
// callers only invoke from handler-side type-confirmed branches
// (never from a happy path), so the nil-assetID guard exists to
// surface a programming bug rather than to swallow one.
func NewSupersede(assetID, currentVersion, eventVersion string) error {
	if assetID == "" {
		return errors.New("outboxevents.NewSupersede: empty asset id — handlers must set AssetID before calling NewSupersede")
	}
	return &SupersedeError{
		AssetID:  assetID,
		Current:  currentVersion,
		Expected: eventVersion,
		Reason: fmt.Sprintf("event source_version=%q superseded by current source_version=%q",
			eventVersion, currentVersion),
	}
}

// IsSupersede reports whether err (or any error in its chain) is a
// *SupersedeError. Used by Pool.processEvent to route the row to
// MarkSuperseded rather than retry / dead_letter. Nil-safe.
// SupersedeError does NOT count as terminal (IsTerminal returns false)
// so the two paths remain composable if a future handler wants both
// classifications in one return value (wrap TerminalError around a
// SupersedeError or vice versa).
func IsSupersede(err error) bool {
	if err == nil {
		return false
	}
	var se *SupersedeError
	return errors.As(err, &se)
}
