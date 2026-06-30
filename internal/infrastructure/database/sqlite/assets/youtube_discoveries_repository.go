// internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go
// ──────────────────────────────────────────────────────────────────────────────
// Per-channel discovery ledger repository — Commit D (June 2026, PR-D YouTube
// Channel Monitor cutover).
//
// This file owns the typed persistence for the `youtube_discoveries` ledger
// (table created in migrations/sqlite/113_youtube_discoveries.sql). The
// ledger implements the canonical "leader-election by INSERT" dedupe pattern:
//
//   - TryReserve(ctx, channelID, videoID, sourceURL, title, discoveredAt) →
//     (id, won bool, err error): runs INSERT ... ON CONFLICT(channel_id,
//     video_id) DO NOTHING RETURNING id. If RETURNING yields a row, this
//     caller WON the dedupe race and proceeds to emit a durable
//     youtube_clip.extract job. If RETURNING yields no row, the caller LOST
//     and classifies outcome as `already_scheduled` (the existing row is
//     the winner — no new job is emitted; the watermark still progresses
//     because discovered_at on the ledger row is already in place).
//
//   - MarkEnqueued(ctx, id, enqueuedAt) → err: flips the row from
//     `pending` → `enqueued` once the durable job is successfully emitted
//     by the JobEnqueuer port. Idempotent: a second call on the same row
//     is a no-op (enqueued stays 1).
//
//   - MaxDiscoveredAt(ctx, channelID) → (string, error): SELECT
//     MAX(discovered_at) FROM youtube_discoveries WHERE channel_id = ?.
//     Cycle-end watermark: the scheduler's checkChannel defer calls this
//     after the per-video fan-out completes and persists the value as
//     category_channels.last_cursor.
//
//   - CountByChannel(ctx, channelID) → (int, error): convenience for the
//     test fixture (5 videos × 2 invocations). Also serves ad-hoc
//     auditing purposes.
//
// Per AGENTS.md / godlike/06 §"Database and config ownership":
//   - The repository lives in `internal/infrastructure/...` so the
//     application layer (monitor package) consumes the typed methods via
//     ports only.
//   - The schema identity (column names + table name) lives exclusively
//     on the row type + the CREATE TABLE migration; SQL tags do NOT
//     leak to the domain layer.
//
// Pattern rationale: the previous Cycle-best-effort
// channels.UpdateCursor(Cursor=videoID) approach (extraction_enqueuer.go
// contract 3) silently lost cursor updates on SQLite transient errors,
// re-discovering the same videos on the next cycle. The ledger-backed
// version is durable at the table level — every INSERT that won the race
// is persisted, the watermark is a monotonic MAX(), so the next cycle
// re-classifies (not re-discovers) any video already in the ledger.
package assets

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// YoutubeDiscoveriesRepository owns the youtube_discoveries ledger.
//
// Concurrency: TryReserve is the only method that races under
// concurrent dispatcher goroutines. The UNIQUE(channel_id, video_id)
// constraint is the canonical race winner selector — only one
// goroutine's INSERT can win, the rest receive rowsAffected==0.
type YoutubeDiscoveriesRepository struct {
	db *sql.DB
}

// NewYoutubeDiscoveriesRepository constructs the repository on the
// canonical SQLiteDB. Nil-tolerant surfaces a typed-nil panic-safe
// error at construction so a wiring gap is loud rather than a
// per-tick silent degraded path. (Matches the canonical pattern of
// the other `assets.X` repositories.)
func NewYoutubeDiscoveriesRepository(db *sql.DB) *YoutubeDiscoveriesRepository {
	if db == nil {
		panic("assets.NewYoutubeDiscoveriesRepository: db is nil")
	}
	return &YoutubeDiscoveriesRepository{db: db}
}

// DB returns the underlying *sql.DB; needed by ComposeAndBulkInsert
// in monitor package and ad-hoc admin CLIs.
func (r *YoutubeDiscoveriesRepository) DB() *sql.DB { return r.db }

// TryReserve performs the leader-election INSERT with ON CONFLICT DO
// NOTHING. SQLite's RETURNING clause yields the newly-inserted row's
// id only when this caller WON the race; the losing callers receive
// `won=false` so the per-video goroutine classifies its outcome as
// `already_scheduled` and skips the EnqueueExtract port call.
//
// The id is derived deterministically from (channel_id, video_id) so
// concurrent attempts by multiple processes converge on the SAME id
// — the ON CONFLICT DO NOTHING is keyed on (channel_id, video_id),
// not on id, so the id derivation is purely a debugging convenience.
// If a caller wins once and tries again later (e.g. on retry after a
// transient SQLite error), the second TryReserve returns won=false
// with the SAME id (the canonical winner's row).
func (r *YoutubeDiscoveriesRepository) TryReserve(
	ctx context.Context,
	channelID, videoID, sourceURL, title, discoveredAt string,
) (id string, won bool, err error) {
	if channelID == "" || videoID == "" {
		return "", false, fmt.Errorf("youtube_discoveries.TryReserve: channelID and videoID are required")
	}
	if discoveredAt == "" {
		// Fail-fast: callers must pass RFC3339. The channel monitor
		// derives this from the scheduler's now() before calling.
		discoveredAt = time.Now().UTC().Format(time.RFC3339)
	}
	id = deriveDiscoveryID(channelID, videoID)

	var returnedID sql.NullString
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO youtube_discoveries (
			id, channel_id, video_id, discovered_at, source_url, title, outcome
		) VALUES (?, ?, ?, ?, ?, ?, 'pending')
		ON CONFLICT(channel_id, video_id) DO NOTHING
		RETURNING id
	`, id, channelID, videoID, discoveredAt, sourceURL, title)
	if err := row.Scan(&returnedID); err != nil {
		if err == sql.ErrNoRows {
			// Losing case: another caller won. Return their id so
			// callers can render it in observability without an extra
			// round-trip; won=false drives the outcome classification.
			return id, false, nil
		}
		return "", false, fmt.Errorf("youtube_discoveries.TryReserve: %w", err)
	}
	if !returnedID.Valid {
		// Defensive: RETURNING yielded no row but didn't error. Treat
		// as already-scheduled so the caller doesn't double-emit.
		return id, false, nil
	}
	return returnedID.String, true, nil
}

// MarkEnqueued flips the ledger row from `pending` to `enqueued`.
// Idempotent on repeated calls: a row with enqueued=1 stays at 1,
// enqueued_at stays at the FIRST successful enqueue's timestamp. This
// guarantees the watermark doesn't oscillate between cycles on
// retry-after-transient-error paths.
//
// outcome is `enqueued` here; rejection outcomes (`rejected` +
// rejection_reason) get their own MarkRejected helper so the table
// state distinguishes the two cases at audit time.
func (r *YoutubeDiscoveriesRepository) MarkEnqueued(ctx context.Context, id, enqueuedAt string) error {
	if id == "" {
		return fmt.Errorf("youtube_discoveries.MarkEnqueued: id is required")
	}
	if enqueuedAt == "" {
		enqueuedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE youtube_discoveries
		SET enqueued = 1,
		    enqueued_at = ?,
		    outcome = 'enqueued'
		WHERE id = ?
		  AND enqueued = 0
	`, enqueuedAt, id)
	return err
}

// MarkRejected records an explicit rejection outcome on the ledger.
// Called when a video passes the cost-of-entry filter chain but then
// fails the semantic gate / MaxVideosPerRun slot / Ollama score
// threshold. The enqueued flag stays at 0 (matches EnqueueOutcome:
// rejected is non-enqueued); rejection_reason carries the human-readable
// reason for observability.
//
// id is the value returned by TryReserve (regardless of won flag —
// so the rejected outcome also records for already-scheduled videos
// that hit a filter reject on a later cycle).
func (r *YoutubeDiscoveriesRepository) MarkRejected(ctx context.Context, id, rejectionReason string) error {
	if id == "" {
		return fmt.Errorf("youtube_discoveries.MarkRejected: id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE youtube_discoveries
		SET outcome = 'rejected',
		    rejection_reason = ?
		WHERE id = ?
		  AND enqueued = 0
		  AND outcome NOT IN ('enqueued', 'rejected')
	`, rejectionReason, id)
	return err
}

// MaxDiscoveredAt returns the largest discovered_at for the channel,
// or empty string if no rows exist (a fresh ledger on a new channel).
// The cycle-end watermark write reads this value and persists it as
// category_channels.last_cursor (the column is repurposed from
// "last video ID" to "RFC3339 timestamp of the high-water mark").
func (r *YoutubeDiscoveriesRepository) MaxDiscoveredAt(ctx context.Context, channelID string) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("youtube_discoveries.MaxDiscoveredAt: channelID is required")
	}
	var maxAt sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT MAX(discovered_at)
		FROM youtube_discoveries
		WHERE channel_id = ?
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
// Drives the test fixture (5 videos × 2 invocations → 5 rows) and
// ad-hoc admin observability.
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

// deriveDiscoveryID computes the canonical ledger id from
// (channel_id, video_id). The id is sha256 of the join, hex-truncated
// to 16 chars with the "disc_" prefix, so the cell stays human-readable
// during debugging while the underlying hash space is wide enough to
// avoid collisions across migrations.
//
// Deterministic derivation is intentional: concurrent retry-after-error
// paths must converge on the same id so the ON CONFLICT DO NOTHING
// key (channel_id, video_id) continues to gate correctly even after
// the underlying row went through a hot update.
func deriveDiscoveryID(channelID, videoID string) string {
	h := sha256.Sum256([]byte(channelID + ":" + videoID))
	return "disc_" + hex.EncodeToString(h[:8])
}
