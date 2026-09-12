// Package event — outbox_status.go: the canonical outbox lifecycle and
// scheduling vocabulary.
//
// An outbox ROW is a different identity fact from an outbox event NAME, but it
// is the same class of fact: it is provider-scoped across BOTH storage engines
// (SQLite `outbox_events` and PostgreSQL `outbox_events`), so it cannot be
// declared once per engine adapter without inviting drift. The two adapters
// used to shadow each other with independent declarations:
//
//	internal/platform/sqlite/outboxevents/repository_write.go → SupersedeStatus = "superseded"
//	internal/platform/postgres/media/outbox.go                → SupersedeStatus = "superseded"
//	internal/platform/sqlite/outboxevents/repository.go        → PriorityNormal/High = 5/10
//	internal/platform/postgres/media/outbox.go                 → PriorityNormal/High = 5/10
//
// and FOUR independent `isTerminalOutboxStatus` predicates classified the same
// rows — two of them disagreeing on the legacy `dead` spelling. This file is
// the single owner: the adapters re-export (compile-time reference), and
// IsTerminalOutboxStatus is the one predicate.
//
// The literals here are NOT registered in Canonical(): `Canonical()` is the
// event-NAME identity registry consumed by percheck_identity_ssot, which
// matches by literal. Short, generic spellings like "pending" and "completed"
// are legitimately re-declared by unrelated domains (asset.ProcessingStatus,
// job stage status, …), so registering them there would drown the gate in
// false positives. The gate that protects THIS vocabulary is the re-export
// discipline itself: the engine adapters alias these constants instead of
// re-declaring the strings.
package event

import "strings"

// Canonical outbox_events.status lifecycle values.
//
// The lifecycle is a strict progression; `superseded` is terminal-success
// ("a newer version of the same aggregate obsoleted this event"), distinct
// from `dead_letter` ("the producer/handler is broken").
const (
	// OutboxStatusPending is a row waiting to be claimed.
	OutboxStatusPending = "pending"
	// OutboxStatusProcessing is a row claimed by a worker under a lease.
	OutboxStatusProcessing = "processing"
	// OutboxStatusCompleted is terminal success.
	OutboxStatusCompleted = "completed"
	// OutboxStatusDeadLetter is terminal failure (attempts exhausted, or a
	// terminal error reported by the handler).
	OutboxStatusDeadLetter = "dead_letter"
	// OutboxStatusSuperseded is terminal success-by-obsoletion: a newer
	// version of the same aggregate made this event a no-op.
	OutboxStatusSuperseded = "superseded"
	// OutboxStatusDeadLegacy is the pre-migration spelling of
	// OutboxStatusDeadLetter, still present in historical rows. It is
	// recognised as terminal so a legacy row can never be re-enqueued.
	OutboxStatusDeadLegacy = "dead"
)

// OutboxLifecycleStatuses returns the operator-facing lifecycle buckets in
// display order. The slice is built fresh on each call so a caller cannot
// mutate the registry.
func OutboxLifecycleStatuses() []string {
	return []string{
		OutboxStatusPending,
		OutboxStatusProcessing,
		OutboxStatusCompleted,
		OutboxStatusDeadLetter,
		OutboxStatusSuperseded,
	}
}

// OutboxErrorStatuses returns the buckets a diagnostic error sweep must
// inspect: any row can carry a `last_error` from an earlier attempt, including
// terminal ones. Omitting a bucket here means the operator dashboard silently
// hides those errors.
func OutboxErrorStatuses() []string {
	return OutboxLifecycleStatuses()
}

// IsTerminalOutboxStatus reports whether status is a terminal lifecycle state.
// It normalises case and surrounding whitespace, matching the SQL that writes
// the column.
func IsTerminalOutboxStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case OutboxStatusCompleted, OutboxStatusDeadLetter, OutboxStatusSuperseded, OutboxStatusDeadLegacy:
		return true
	default:
		return false
	}
}

// Canonical outbox scheduling priorities (migration 186).
//
// The claim query orders by (priority DESC, next_attempt_at ASC, id ASC).
const (
	// OutboxPriorityNormal is the default for producers that do not stamp
	// an explicit priority (bulk folder sync / catalog re-sync).
	OutboxPriorityNormal = 5
	// OutboxPriorityHigh is the script-required index priority: assets a
	// script-generation job is blocked on must be claimed first.
	OutboxPriorityHigh = 10
)
