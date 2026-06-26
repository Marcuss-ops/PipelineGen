// Package idempotency — concrete SQLite adapter for the
// IdempotencyStore port (PR8, internal/application/middleware/idempotency_store.go).
//
// The repository owns the canonical idempotency_keys table
// (migration 095_create_idempotency_keys.sql). It implements the
// typed port (TryInsert / Complete / Get / DeleteExpired) and is
// consumed by the Gin middleware
// (internal/api/middleware/idempotency.go).
//
// Schema invariants enforced here:
//   - PRIMARY KEY on `key` enables INSERT-or-FAIL atomic acquisition.
//     We rely on the standard `database/sql` error surface:
//   - SQLite returns "UNIQUE constraint failed: idempotency_keys.key"
//     when the INSERT collides. We parse for that string in
//     tryInsert's error mapper.
//   - Body_hash defaults to "" for multipart bypass (Content-Type
//     containing "multipart/form-data"). The port does NOT enforce
//     shape — the middleware handles validation.
//   - Timestamps are RFC3339Nano (consistent with the existing
//     canonical style in migrations 092 and 094).
//   - The 24h TTL is absolute (`created_at + 24h`), not sliding.
//     DeleteExpired enforces it.
package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	mw "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SQLiteRepository implements middleware.IdempotencyStore backed
// by the canonical `idempotency_keys` table.
type SQLiteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository constructs the concrete repo. The db handle
// must be the canonical *sql.DB from storage.SQLiteDB.DB (PR9
// layering rule — pkg-handle on the boundary, no infrastructure
// type leaks upward).
//
// Compile-time assertion that the concrete type satisfies the
// port — Pattern 0 from AGENTS.md (June 2026).
func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

// Compile-time assertion that the concrete satisfies the port.
var _ mw.IdempotencyStore = (*SQLiteRepository)(nil)

// ttl is the absolute TTL applied at TryInsert time. A row's
// expires_at is `now + ttl`. The middleware's cleanup ticker
// calls DeleteExpired with `now` to remove rows past this bound.
const ttl = 24 * time.Hour

// TryInsert inserts a new in_flight row. Returns (nil, true, nil)
// when the row already exists, (record, false, nil) on success,
// (nil, false, err) on storage errors.
//
// Body_acquisition concurrency: PRIMARY KEY constraint guarantees
// that only one TryInsert for a given key wins. The other caller
// sees AlreadyExists=true and can route to 409 (in_flight) or
// replay (completed). Neither caller observes a partial state —
// the INSERT is single-statement atomic under SQLite WAL mode.
func (r *SQLiteRepository) TryInsert(ctx context.Context, key, bodyHash string) (*mw.IdempotencyRecord, bool, error) {
	if key == "" {
		return nil, false, fmt.Errorf("idempotency.TryInsert: key is required")
	}
	if len(key) > 255 {
		return nil, false, fmt.Errorf("idempotency.TryInsert: key exceeds 255 chars (got %d)", len(key))
	}
	now := time.Now()
	createdAtStr := timeutil.FormatRFC3339(now)
	expiresAtStr := timeutil.FormatRFC3339(now.Add(ttl))

	const q = `INSERT INTO idempotency_keys
	  (key, body_hash, status, response_status, response_body, response_content_type, created_at, expires_at, last_replayed_at)
	  VALUES (?, ?, 'in_flight', 0, '', '', ?, ?, '')`
	_, err := r.db.ExecContext(ctx, q, key, bodyHash, createdAtStr, expiresAtStr)
	if err != nil {
		if isUniqueConstraintError(err) {
			// Row already exists — caller will Get() to inspect status.
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("idempotency.TryInsert(%q): %w", key, err)
	}
	return &mw.IdempotencyRecord{
		Key:       key,
		BodyHash:  bodyHash,
		Status:    "in_flight",
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}, false, nil
}

// Complete transitions in_flight → completed and attaches the
// response payload. Idempotent: republishing the same response is
// a no-op write but bumps last_replayed_at to the current time.
func (r *SQLiteRepository) Complete(ctx context.Context, key string, responseStatus int, responseBody []byte, responseContentType string) error {
	if key == "" {
		return fmt.Errorf("idempotency.Complete: key is required")
	}
	const q = `UPDATE idempotency_keys
	  SET status = 'completed',
	      response_status = ?,
	      response_body = ?,
	      response_content_type = ?,
	      last_replayed_at = ?
	  WHERE key = ? AND status = 'in_flight'`
	now := time.Now()
	nowStr := timeutil.FormatRFC3339(now)
	res, err := r.db.ExecContext(ctx, q, responseStatus, string(responseBody), responseContentType, nowStr, key)
	if err != nil {
		return fmt.Errorf("idempotency.Complete(%q): %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("idempotency.Complete(%q) rows: %w", key, err)
	}
	if n == 0 {
		// Either the row doesn't exist, or it isn't in_flight (already
		// completed by a prior request). Distinguish via Get.
		rec, gerr := r.Get(ctx, key)
		if gerr != nil {
			if errors.Is(gerr, mw.ErrIdempotencyKeyNotFound) {
				return fmt.Errorf("idempotency.Complete(%q): %w", key, mw.ErrIdempotencyKeyNotFound)
			}
			return fmt.Errorf("idempotency.Complete(%q) inspect: %w", key, gerr)
		}
		if rec.Status != "in_flight" {
			return fmt.Errorf("idempotency.Complete(%q): %w (current status=%q)", key, mw.ErrIdempotencyKeyNotInFlight, rec.Status)
		}
		// Row was in_flight when Get ran but not at UPDATE time — concurrent
		// Complete. Surface as not-in-flight so caller can no-op.
		return fmt.Errorf("idempotency.Complete(%q): race, no longer in_flight", key)
	}
	return nil
}

// Get returns the current row for key. ErrIdempotencyKeyNotFound
// when no row exists.
func (r *SQLiteRepository) Get(ctx context.Context, key string) (*mw.IdempotencyRecord, error) {
	if key == "" {
		return nil, fmt.Errorf("idempotency.Get: key is required")
	}
	const q = `SELECT key, body_hash, status, response_status, response_body, response_content_type, created_at, expires_at, last_replayed_at
	  FROM idempotency_keys
	  WHERE key = ?`
	row := r.db.QueryRowContext(ctx, q, key)

	var (
		rec          mw.IdempotencyRecord
		bodyStr      string
		createdAtStr string
		expiresAtStr string
		lastReplayAt string
	)
	err := row.Scan(
		&rec.Key,
		&rec.BodyHash,
		&rec.Status,
		&rec.ResponseStatus,
		&bodyStr,
		&rec.ResponseCT,
		&createdAtStr,
		&expiresAtStr,
		&lastReplayAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("idempotency.Get(%q): %w", key, mw.ErrIdempotencyKeyNotFound)
		}
		return nil, fmt.Errorf("idempotency.Get(%q): %w", key, err)
	}
	rec.ResponseBody = []byte(bodyStr)
	createdAt := timeutil.ParseRFC3339(createdAtStr)
	if createdAt.IsZero() && createdAtStr != "" {
		return nil, fmt.Errorf("idempotency.Get(%q) created_at: zero-value parse", key)
	}
	rec.CreatedAt = createdAt
	expiresAt := timeutil.ParseRFC3339(expiresAtStr)
	if expiresAt.IsZero() && expiresAtStr != "" {
		return nil, fmt.Errorf("idempotency.Get(%q) expires_at: zero-value parse", key)
	}
	rec.ExpiresAt = expiresAt
	if lastReplayAt != "" {
		rp := timeutil.ParseRFC3339(lastReplayAt)
		if !rp.IsZero() {
			rec.LastReplayedAt = rp
		}
	}
	return &rec, nil
}

// DeleteExpired removes all rows with expires_at < now. Returns
// the count of rows deleted. Called by the middleware's background
// cleanup ticker (every 15 minutes).
func (r *SQLiteRepository) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	nowStr := timeutil.FormatRFC3339(now)
	res, err := r.db.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE expires_at < ?`, nowStr)
	if err != nil {
		return 0, fmt.Errorf("idempotency.DeleteExpired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("idempotency.DeleteExpired rows: %w", err)
	}
	return int(n), nil
}

// sqliteUniqueConstraintRe matches the standard SQLite UNIQUE
// constraint error string. The pattern matches both 3.x syntax
// (`UNIQUE constraint failed: ...`) and the older 2.x format.
var sqliteUniqueConstraintRe = regexp.MustCompile(`(?i)UNIQUE constraint failed`)

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return sqliteUniqueConstraintRe.MatchString(err.Error()) ||
		strings.Contains(err.Error(), "constraint failed") ||
		strings.Contains(err.Error(), "code: 2067") || // SQLITE_CONSTRAINT_UNIQUE
		strings.Contains(err.Error(), "code: 1555") // SQLITE_CONSTRAINT_PRIMARYKEY
}
