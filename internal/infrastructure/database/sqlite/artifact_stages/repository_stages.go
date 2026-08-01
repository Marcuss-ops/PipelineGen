// internal/infrastructure/database/sqlite/artifact_stages/repository_stages.go —
// saga state machine transitions (MarkPublished / MarkSucceeded /
// MarkFailedPermanent / IncrementAttemptCount + fenced CAS helper).
// Extracted from repository.go; no behavior change.
package artifactstages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── MarkPublished ───────────────────────────────────────────────────────

func (r *Repository) MarkPublished(ctx context.Context, id, publishedLocation string, publishedAt time.Time) error {
	now := r.now()
	publishedAt = publishedAt.UTC()
	res, err := r.db.ExecContext(ctx,
		`UPDATE artifact_stages SET state = 'PUBLISHED', published_location = ?, published_at = ?, updated_at = ? WHERE id = ? AND state NOT IN ('PUBLISHED', 'SUCCEEDED', 'FAILED_PERMANENT')`,
		publishedLocation, timeutil.FormatRFC3339Nano(publishedAt), timeutil.FormatRFC3339Nano(now), id)
	if err != nil {
		return fmt.Errorf("artifact_stages.MarkPublished (id=%s): %w", id, err)
	}
	return r.checkFencedCAS(ctx, res, id, "MarkPublished")
}

// ── MarkSucceeded ───────────────────────────────────────────────────────
func (r *Repository) MarkSucceeded(ctx context.Context, id string) error {
	now := r.now()
	// Fence rationale: PUBLISHED intentionally NOT fenced — transitional state for the finalizer’s PUBLISHED→SUCCEEDED promotion (see repository.go package doc).
	res, err := r.db.ExecContext(ctx,
		`UPDATE artifact_stages SET state = 'SUCCEEDED', updated_at = ? WHERE id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
		timeutil.FormatRFC3339Nano(now), id)
	if err != nil {
		return fmt.Errorf("artifact_stages.MarkSucceeded (id=%s): %w", id, err)
	}
	return r.checkFencedCAS(ctx, res, id, "MarkSucceeded")
}

// ── MarkFailedPermanent ─────────────────────────────────────────────────
func (r *Repository) MarkFailedPermanent(ctx context.Context, id, lastError string) error {
	now := r.now()
	// Fence rationale: PUBLISHED intentionally NOT fenced — transitional state for the finalizer’s PUBLISHED→SUCCEEDED promotion (see repository.go package doc).
	res, err := r.db.ExecContext(ctx,
		`UPDATE artifact_stages SET state = 'FAILED_PERMANENT', last_error = ?, updated_at = ? WHERE id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
		lastError, timeutil.FormatRFC3339Nano(now), id)
	if err != nil {
		return fmt.Errorf("artifact_stages.MarkFailedPermanent (id=%s): %w", id, err)
	}
	return r.checkFencedCAS(ctx, res, id, "MarkFailedPermanent")
}

// ── IncrementAttemptCount ───────────────────────────────────────────────
func (r *Repository) IncrementAttemptCount(ctx context.Context, id string) error {
	now := r.now()
	// Fence rationale: PUBLISHED intentionally NOT fenced — transitional state for the finalizer’s PUBLISHED→SUCCEEDED promotion (see repository.go package doc).
	res, err := r.db.ExecContext(ctx,
		`UPDATE artifact_stages SET attempt_count = attempt_count + 1, updated_at = ? WHERE id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
		timeutil.FormatRFC3339Nano(now), id)
	if err != nil {
		return fmt.Errorf("artifact_stages.IncrementAttemptCount (id=%s): %w", id, err)
	}
	return r.checkFencedCAS(ctx, res, id, "IncrementAttemptCount")
}

// ── Fenced CAS disambiguation ───────────────────────────────────────────

// checkFencedCAS converts a 0-rowsAffected UPDATE into a typed
// error. The disambiguation probe (SELECT state FROM artifact_stages
// WHERE id = ?) costs one extra round-trip on the failure path;
// the success path stays single-roundtrip (godlike/07: never
// silently accept a fence-mismatch as success).
//
// The ctx is threaded through so a cancelled request aborts the
// disambiguation probe too (otherwise a slow probe would leak past
// the ctx deadline).
func (r *Repository) checkFencedCAS(ctx context.Context, res sql.Result, id, op string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("artifact_stages.%s: rows-affected (id=%s): %w", op, id, err)
	}
	if affected > 0 {
		return nil
	}
	// Disambiguate: row absent vs row already-terminal.
	var state string
	scanErr := r.db.QueryRowContext(ctx, `SELECT state FROM artifact_stages WHERE id = ?`, id).Scan(&state)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return artifact.WrapArtifactStageNotFound(id)
	}
	if scanErr != nil {
		return fmt.Errorf("artifact_stages.%s: disambiguate probe (id=%s): %w", op, id, scanErr)
	}
	return fmt.Errorf("%w: id=%s current_state=%s op=%s", artifact.ErrTerminalStateRejection, id, state, op)
}
