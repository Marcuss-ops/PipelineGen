// internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go
// ──────────────────────────────────────────────────────────────────────────────
// Per-channel discovery ledger repository — Commit 3/6 (June 2026, PR-C
// YouTube cutover, P1 #5 + #6 + #7).
//
// This file owns the typed persistence for the `youtube_discoveries` ledger
// (table created in migrations/sqlite/114_youtube_discoveries_v2.sql; the
// v2 schema REPLACES the original 113 schema with a retryable state
// machine + policy_version gate). The ledger implements the canonical
// "leader-election by INSERT" dedupe pattern through TryReserve +
// tryReserveConflict.
//
// State-transition methods (MarkEnqueued, MarkRejected, MarkReclaimByLease)
// live in youtube_discoveries_transitions.go. Pure helpers
// (deriveDiscoveryID, ComputeRetryBackoffSeconds, ResolveDateAfter) live in
// youtube_discoveries_helpers.go.
//
// Backoff formula (canonical): backoff(attempt_count) =
// min(30s * 2^(attempt_count-1), 300s). attempt_count starts at 1
// for the first retry, so the FIRST retry fires after ~30s and the
// TWELFTH retry still fires within 300s (capped).
package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrStateConflict is returned by state-transition methods (MarkEnqueued,
// MarkRejected) when the row's current state does not match the expected
// source state(s). The caller can use errors.Is(err, ErrStateConflict) to
// distinguish a genuine state-precondition failure from a transient SQLite
// I/O error.
//
// Blocco 2 (July 2026) — audit P0 #3: every state transition now checks
// RowsAffected; a zero-row UPDATE (excluding explicitly idempotent paths)
// surfaces this sentinel so the caller can decide to retry, dead-letter,
// or reconcile.
var ErrStateConflict = errors.New("youtube_discoveries: state conflict — row state does not match expected source state")

// ErrNotFound is returned by state-transition methods when the row does
// not exist (0 rows match the id filter). Distinct from ErrStateConflict
// which means the row EXISTS but its state is incompatible with the
// requested transition.
//
// Blocco 2 (July 2026) — FASE 1.3 typed-transition audit: pre-fix,
// MarkEnqueued wrapped sql.ErrNoRows in ErrStateConflict, conflating
// "row not found" with "row in wrong state". The two sentinels are
// now separate so callers can distinguish not-found (missing data)
// from conflict (data exists, state is incompatible).
var ErrNotFound = errors.New("youtube_discoveries: entity not found")

// ErrAlreadyApplied is returned by idempotent state-transition methods
// when the transition was already applied in a prior call. This is NOT
// an error — it signals the caller that the desired state is already
// reached, and no further action is needed.
//
// Blocco 2 (July 2026) — FASE 1.3: separated from nil-success so
// callers can distinguish "I just applied the transition" from
// "someone else already applied it (idempotent)".
var ErrAlreadyApplied = errors.New("youtube_discoveries: transition already applied")

// RetryableBackoffCapSeconds is the canonical cap for the backoff
// formula. Exposed so monitor package and tests can lock the value
// without re-reasoning it.
const RetryableBackoffCapSeconds = 300

// DefaultLeaseDurationSeconds is the lease_until offset written into
// every new TryReserve row. A pending row with an expired lease is
// reclaimable by the next cycle's TryReserve gate. Set to 5 min —
// long enough for EnqueueExtract + MarkEnqueued to complete under
// normal load, short enough that a stuck row won't block the channel
// for an entire scheduler cycle. Blocco 3b (July 2026).
const DefaultLeaseDurationSeconds = 300

// YoutubeDiscoveriesRepository owns the youtube_discoveries ledger.
//
// Concurrency: TryReserve is the only method that races under
// concurrent dispatcher goroutines. The UNIQUE(channel_id, video_id,
// policy_version) constraint is the canonical race winner selector
// — only one goroutine's INSERT can win; the rest re-classify as
// already_scheduled, retryable, or lease-reclaim-eligible per the
// rules above.
type YoutubeDiscoveriesRepository struct {
	db  *sql.DB
	now func() time.Time // injectable clock for tests; production = time.Now
}

// NewYoutubeDiscoveriesRepository constructs the repository on the
// canonical SQLiteDB. Nil db surfaces a typed panic at construction
// so a wiring gap is loud rather than a per-tick silent degraded
// path. (Matches the canonical pattern of the other `assets.X`
// repositories.)
func NewYoutubeDiscoveriesRepository(db *sql.DB) *YoutubeDiscoveriesRepository {
	if db == nil {
		panic("assets.NewYoutubeDiscoveriesRepository: db is nil")
	}
	return &YoutubeDiscoveriesRepository{db: db, now: time.Now}
}

// SetNowForTests replaces the internal clock with the supplied function.
// Intended for test fixtures that need to simulate time passing (lease
// expiry, crash recovery) without real-time waits. Call with nil to
// restore the production default (time.Now).
//
// FASE 1.1 (July 2026): injectable clock so TestTryReserve_CrashAfter
// Reservation_Reclaimable can advance the clock past the lease without
// waiting 5 real minutes.
func (r *YoutubeDiscoveriesRepository) SetNowForTests(fn func() time.Time) {
	if fn == nil {
		r.now = time.Now
		return
	}
	r.now = fn
}

// DB returns the underlying *sql.DB; needed by ad-hoc admin CLIs.
func (r *YoutubeDiscoveriesRepository) DB() *sql.DB { return r.db }

// TryReserve performs the leader-election INSERT + retry-eligibility
// UPDATE for the v2 retryable state machine.
//
// Returns:
//
//   - (id, won=true,  attempt+1, nil) — caller WON the lease: emits
//     the durable broker job for this video.
//   - (id, won=false, attempt,    nil) — caller LOST to a peer:
//     either another cycle won the same (key, policy_version) or
//     the row already reached a terminal state ('enqueued' /
//     'completed' / 'already_scheduled'). Caller classifies this as
//     OutcomeAlreadyScheduled and skips EnqueueExtract.
//
// policyVersion is REQUIRED for new callers but accepts "" →
// defaulted to "v1" (matches the column DEFAULT). Already-scheduled
// callers' stale ids have v1 stamped automatically.
func (r *YoutubeDiscoveriesRepository) TryReserve(
	ctx context.Context,
	channelID, videoID, policyVersion, sourceURL, title, discoveredAt string,
) (id string, won bool, attempt int, err error) {
	if channelID == "" || videoID == "" {
		return "", false, 0, fmt.Errorf("youtube_discoveries.TryReserve: channelID and videoID are required")
	}
	if discoveredAt == "" {
		discoveredAt = r.now().UTC().Format(time.RFC3339)
	}
	if policyVersion == "" {
		policyVersion = "v1"
	}
	id = deriveDiscoveryID(channelID, videoID, policyVersion)
	nowStr := r.now().UTC().Format(time.RFC3339)
	leaseUntil := r.now().UTC().Add(time.Duration(DefaultLeaseDurationSeconds) * time.Second).Format(time.RFC3339)

	// Step 1 — leader-election INSERT. UNIQUE(channel_id, video_id,
	// policy_version) gates the candidate row; ON CONFLICT DO NOTHING
	// + RETURNING yields the id only when this caller WON.
	//
	// Blocco 3b (July 2026): lease_until is set at INSERT time so
	// a pending row with an expired lease is reclaimable by the next
	// cycle's TryReserve gate. Pre-fix the INSERT created pending
	// rows without a lease, so a MarkEnqueued failure left the row
	// permanently stuck in 'pending' (not reclaimable, not terminal).
	var returnedID sql.NullString
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO youtube_discoveries (
			id, channel_id, video_id, policy_version, state,
			discovered_at, source_url, title, lease_until,
			attempt_count, updated_at
		) VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, ?, 1, ?)
		ON CONFLICT(channel_id, video_id, policy_version) DO NOTHING
		RETURNING id
	`, id, channelID, videoID, policyVersion, discoveredAt, sourceURL, title, leaseUntil, nowStr)
	if scanErr := row.Scan(&returnedID); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			// Conflict: existing row. Step 2 — eligibility check.
			return r.tryReserveConflict(ctx, channelID, videoID, policyVersion, id, nowStr)
		}
		return "", false, 0, fmt.Errorf("youtube_discoveries.TryReserve: insert: %w", scanErr)
	}
	// Won the leader-election on a fresh row.
	return returnedID.String, true, 1, nil
}

// tryReserveConflict handles the ON CONFLICT path. Returns the same
// 4-value tuple the outer TryReserve returns.
//
// The eligibility rules mirror the doc-comment on TryReserve (a/b/c).
//
// Blocco 1 (July 2026) — atomic compare-and-swap rewrite:
// The pre-fix shape (SELECT state/lease/attempt → decide in Go →
// UPDATE without checking old values) was a classic TOCTOU race:
// two goroutines could both read an expired lease, both UPDATE the
// same row, and both return won=true. The fix uses atomic
// UPDATE...WHERE...RETURNING: the WHERE clause pins the old state +
// lease; only ONE row matches the predicate per (channel_id,
// video_id, policy_version). A RETURNING id on zero matching rows
// surfaces sql.ErrNoRows → won=false. lease_until is now SET to a
// NEW lease (not NULL) so a MarkEnqueued failure doesn't leave the
// row permanently stuck in 'pending' — the next cycle's TryReserve
// can reclaim it after the new lease expires.
func (r *YoutubeDiscoveriesRepository) tryReserveConflict(
	ctx context.Context,
	channelID, videoID, policyVersion, id, nowStr string,
) (string, bool, int, error) {
	leaseUntil := r.now().UTC().Add(time.Duration(DefaultLeaseDurationSeconds) * time.Second).Format(time.RFC3339)

	// (a) Atomic reclaim: pending + expired lease → retry with NEW lease.
	// The WHERE clause compares lease_until against nowStr — if another
	// goroutine already reclaimed (and wrote a fresh lease_until in the
	// future), this UPDATE matches zero rows → ErrNoRows → won=false.
	// lease_until is SET to a fresh value (NOT NULL) so the row remains
	// reclaimable on the next cycle if MarkEnqueued never fires.
	var returnedID sql.NullString
	var newAttempt sql.NullInt64
	row := r.db.QueryRowContext(ctx, `
		UPDATE youtube_discoveries
		SET state = 'pending',
		    lease_owner = NULL,
		    lease_until = ?,
		    attempt_count = attempt_count + 1,
		    discovered_at = ?,
		    updated_at = ?
		WHERE channel_id = ? AND video_id = ? AND policy_version = ?
		  AND state = 'pending'
		  AND lease_until IS NOT NULL
		  AND lease_until < ?
		RETURNING id, attempt_count
	`, leaseUntil, nowStr, nowStr, channelID, videoID, policyVersion, nowStr)
	if err := row.Scan(&returnedID, &newAttempt); err != nil {
		if err != sql.ErrNoRows {
			return "", false, 0, fmt.Errorf("youtube_discoveries.TryReserve: lease-reclaim: %w", err)
		}
		// Zero rows matched → try (b).
	} else {
		return returnedID.String, true, int(newAttempt.Int64), nil
	}

	// (b) Atomic reclaim: rejected_retryable + retry-eligible → retry
	// with NEW lease. Same compare-and-swap shape as (a): the WHERE
	// clause includes the old state + next_retry_at gate; only one
	// goroutine's UPDATE wins the row.
	row = r.db.QueryRowContext(ctx, `
		UPDATE youtube_discoveries
		SET state = 'pending',
		    next_retry_at = NULL,
		    lease_until = ?,
		    attempt_count = attempt_count + 1,
		    discovered_at = ?,
		    updated_at = ?
		WHERE channel_id = ? AND video_id = ? AND policy_version = ?
		  AND state = 'rejected_retryable'
		  AND next_retry_at IS NOT NULL
		  AND next_retry_at <= ?
		RETURNING id, attempt_count
	`, leaseUntil, nowStr, nowStr, channelID, videoID, policyVersion, nowStr)
	if err := row.Scan(&returnedID, &newAttempt); err != nil {
		if err != sql.ErrNoRows {
			return "", false, 0, fmt.Errorf("youtube_discoveries.TryReserve: retry-retryable: %w", err)
		}
		// Zero rows matched → already-scheduled (c).
	} else {
		return returnedID.String, true, int(newAttempt.Int64), nil
	}

	// (c) Already-scheduled: row exists but is not reclaimable (active
	// lease, terminal state, or retry not yet due). Return the derived
	// id + the row's current attempt_count so the caller can log it.
	var attemptCount int
	if err := r.db.QueryRowContext(ctx, `
		SELECT attempt_count FROM youtube_discoveries
		WHERE channel_id = ? AND video_id = ? AND policy_version = ?
	`, channelID, videoID, policyVersion).Scan(&attemptCount); err != nil {
		if err == sql.ErrNoRows {
			// Row was deleted between INSERT conflict and now — treat
			// as already-scheduled with the derived id.
			return id, false, 0, nil
		}
		return "", false, 0, fmt.Errorf("youtube_discoveries.TryReserve: final lookup: %w", err)
	}
	return id, false, attemptCount, nil
}

// MaxDiscoveredAt returns the largest discovered_at for
// the channel across ALL terminal states (Cycle-end watermark, Commit 3/6, P1 #10).
//
// Excludes 'pending'+'analyzing' so an in-progress cycle (with
// non-terminal rows stamped recently) doesn't leak a watermark
// that would cause the next cycle's watermark-driven filter to
// drop actually-new videos.
//
// Returns ("", nil) when no terminal-state rows exist (fresh
// ledger on a new channel).
//
// Naming: surface name is `MaxDiscoveredAt` (no `Watermark` suffix)
// to match the typed port YoutubeDiscoveriesPort.MaxDiscoveredAt
// and the call site in discovery.go::recordCycleEndWatermark.
func (r *YoutubeDiscoveriesRepository) MaxDiscoveredAt(ctx context.Context, channelID string) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("youtube_discoveries.MaxDiscoveredAt: channelID is required")
	}
	var maxAt sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT MAX(discovered_at)
		FROM youtube_discoveries
		WHERE channel_id = ?
		  AND state IN ('enqueued', 'already_scheduled', 'completed',
		                'rejected_terminal', 'rejected_retryable')
	`, channelID).Scan(&maxAt)
	if err != nil {
		return "", fmt.Errorf("youtube_discoveries.MaxDiscoveredAt: %w", err)
	}
	if !maxAt.Valid {
		return "", nil
	}
	return maxAt.String, nil
}

// CountByChannel returns the number of ledger rows for the channel.
// Drives the test fixture (5 videos × 2 invocations → 5 rows per
// cycle) and ad-hoc admin observability.
func (r *YoutubeDiscoveriesRepository) CountByChannel(ctx context.Context, channelID string) (int, error) {
	if channelID == "" {
		return 0, fmt.Errorf("youtube_discoveries.CountByChannel: channelID is required")
	}
	var n int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM youtube_discoveries WHERE channel_id = ?
	`, channelID).Scan(&n)
	return n, err
}
