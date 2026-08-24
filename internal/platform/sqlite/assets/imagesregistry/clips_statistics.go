package imagesregistry

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"go.uber.org/zap"
)

// ── PR1 (June 2026) — file role ───────────────────────────────────────────
//
// clips_statistics.go is the canonical home for unconditional metric /
// aggregation methods on *ClipsRepository: cross-state counters,
// distribution summaries, and operationally-oriented gauges that
// callers (e.g. IndexHealth, dashboards, readiness barriers) poll
// without per-call filter scoping.
//
// CURRENT STATE:
//
//   - PR1 (June 2026): file reserved; existing aggregation (CountAll,
//     CountIndexed, CountIndexable, CountPendingOutbox,
//     CountDeadLetter) intentionally stays in clips_repository_queries.go.
//   - PR-P2-DIAGNOSTICS-REALE (July 2026): added CountBySource(ctx,
//     source) (P2-DIAG-08 surfacing). The /api/artlist/diagnostics
//     endpoint now reports how many clips are indexed per source
//     (especially "artlist" as a sanity check); the helper is generic
//     so future callers (dashboards, /api/media/index-health) can
//     reuse it under one SSOT location (godlike/06).
//
// OWNERSHIP RULE FOR FUTURE CONTRIBUTORS:
//
//   * stats-shaped (un-filtered, metric-packaging) → clips_statistics.go (here)
//   * query-shaped (caller-filtered)              → clips_queries.go
//   * counts of outbox events                     → clips_repository_queries.go
//                                                (Wave 15 SSOT, not migrated
//                                                by PR1)
//   * tx-scoped metrics                           → clips_transactions.go
//
// Do NOT add metric-shaped methods into clips_queries.go — that
// folder is caller-filtered query territory, by deliberate
// separation.

// ErrEmptySource is the typed sentinel CountBySource returns when
// the caller passes an empty source string. Per godlike/07
// no-fake-availability the diagnostic endpoint MUST NOT silently
// over-report a "count of everything" — the empty-source case is
// surfaced as a probe failure instead. Mirrors the
// ErrEmpty / ErrEmptyResult discipline on artlist ports.
var ErrEmptySource = errors.New("assets.CountBySource: source is required (godlike/07 — never silently over-report a count of everything)")

// CountBySource returns the number of rows in media_assets whose
// `source` column matches the supplied value. PR-P2-DIAGNOSTICS-REALE
// (July 2026) — surfaced via /api/artlist/diagnostics probe #8.
//
// godlike/06 SSOT (godlike/07 no-fake-availability): this is the
// SINGLE canonical owner of the COUNT(*) WHERE source=? query. The
// diagnostic endpoint aggregates via this helper rather than running
// its own SQL. Adding parallel queries scattered across handlers is a
// godlike/06 violation — fan in here.
//
// Filters:
//   - source (required, non-empty)   — exact-match on media_assets.source
//   - no soft-delete discount        — soft-deleted rows are still
//     counted (true "indexed" metric, not
//     "online" metric; callers wanting
//     the latter compose with
//     asset.SoftDeleteFilter())
//
// Returns ErrEmptySource when source=="" so the caller surfaces a
// probe FAILURE rather than over-reporting "everything".
func (r *ClipsRepository) CountBySource(ctx context.Context, source string) (int, error) {
	if r == nil {
		return 0, errors.New("assets.CountBySource: nil repository")
	}
	if r.db == nil {
		return 0, errors.New("assets.CountBySource: nil db handle (composition root forgot to wire DB?)")
	}
	if source == "" {
		return 0, ErrEmptySource
	}
	const query = `SELECT COUNT(*) FROM media_assets WHERE source = ?`
	var count int
	if err := r.db.QueryRowContext(ctx, query, source).Scan(&count); err != nil {
		if r.log != nil {
			r.log.Warn("CountBySource: query failed",
				zap.String("source", source),
				zap.Error(err),
			)
		}
		return 0, err
	}
	return count, nil
}

// CountPersistedSince returns the number of media_assets rows with
// the given source and created_at >= ts. Used by the Fase 3
// run-status state machine as the "real-DB" cross-check (godlike/07
// fail-closed: a run claiming N processed must have at least N
// media_assets rows; if it doesn't, the state machine forces
// PARTIAL_SUCCESS with diagnostic "real_db_mismatch").
//
// godlike/06 SSOT: this is the SINGLE canonical owner of the
// source + `created_at >= ts` cross-check used by the Fase 3
// /api/artlist/runs/:id state machine. Adding parallel queries
// scattered across handlers is a godlike/06 violation — fan in here.
//
// Filters:
//   - source (required, non-empty)  — exact-match on media_assets.source
//   - ts (required, non-zero)        — time-window lower bound (RFC3339 UTC)
//
// Returns ErrEmptySource when source=="" (reuses the Fase 0 sentinel
// — single canonical typed error for the empty-source condition).
//
// Returns a typed error when ts is the zero time: passing a zero
// `ts` would over-count every row, which is the same "fake-
// availability" anti-pattern as ErrEmptySource. The Fase 3 handler
// constructs the time window from the run's artlist_runs.created_at
// column; a missing or invalid run row fails-closed before reaching
// this call site (the handler returns RunStatusUnknown + HTTP 404).
//
// ── FORMAT-COMPATIBILITY FORWARD-POINTER (July 2026) ────────────────
//
// The media_assets.created_at column has `DEFAULT CURRENT_TIMESTAMP`
// (per migrations/sqlite/033_media_assets_youtube_video_id_index.sql
// line 36 + canonical schema in
// internal/platform/sqlite/canonical.go). The Mattn
// driver + canonical Go writers (time.Now().UTC().Format(RFC3339))
// produce TWO DIFFERENT FORMATS in the same column:
//
//   - `CURRENT_TIMESTAMP` → "2026-07-12 10:00:00" (space, no Z)
//   - `time.Now().UTC().Format(time.RFC3339)` → "2026-07-12T10:00:00Z" (T, Z)
//
// A naive string comparison `created_at >= '2026-07-12T10:00:00Z'`
// against the space-formatted CURRENT_TIMESTAMP row evaluates to
// FALSE (the space character 0x20 < 'T' 0x54 in ASCII), so the
// helper would return 0 for production rows even when real assets
// exist — and Rule 2 of the state machine would falsely fire
// PARTIAL_SUCCESS, breaking the "zero-asset-impossible-to-succeed"
// guarantee.
//
// The fix below coerces both sides through SQLite's `datetime()`
// function (which parses BOTH space and T-formatted ISO-8601
// strings correctly) before comparing. Format-agnostic; works
// regardless of which insertion path produced the row.
//
// DO NOT "simplify" this back to raw string comparison — a future
// refactor that drops the `datetime()` coercion re-introduces the
// production-silent false-PARTIAL_SUCCESS bug. The test
// `TestCountPersistedSince_CURRENT_TIMESTAMP_Default` pins the
// format-agnostic guarantee verbatim.
func (r *ClipsRepository) CountPersistedSince(ctx context.Context, source string, ts time.Time) (int, error) {
	if r == nil {
		return 0, errors.New("assets.CountPersistedSince: nil repository")
	}
	if r.db == nil {
		return 0, errors.New("assets.CountPersistedSince: nil db handle (composition root forgot to wire DB?)")
	}
	if source == "" {
		return 0, ErrEmptySource
	}
	if ts.IsZero() {
		return 0, errors.New("assets.CountPersistedSince: ts is zero (would over-count every row, godlike/07 — refuse silently-double-count-all-rows semantics)")
	}
	// Format-agnostic comparison: datetime(created_at) parses both
	// "2026-07-12 10:00:00" (CURRENT_TIMESTAMP) and
	// "2026-07-12T10:00:00Z" (time.RFC3339) correctly. datetime(?)
	// parses the RFC3339 input the same way.
	const query = `SELECT COUNT(*) FROM media_assets
		WHERE source = ?
		  AND datetime(created_at) >= datetime(?)`
	tsStr := ts.UTC().Format(time.RFC3339)
	var count int
	if err := r.db.QueryRowContext(ctx, query, source, tsStr).Scan(&count); err != nil {
		if r.log != nil {
			r.log.Warn("CountPersistedSince: query failed",
				zap.String("source", source),
				zap.String("since", tsStr),
				zap.Error(err),
			)
		}
		return 0, err
	}
	return count, nil
}

// _ interface pin — ClipsRepository embeds *AssetStoreSQLite which
// already satisfies asset.SourceVersionQuerier (PR 11 followup)
// via clips_repository.go. No additional pin needed here; this
// note guards against the regression where a future refactor splits
// *ClipsRepository away from the embedded struct.
var _ *sql.DB = (*sql.DB)(nil) // silences unused-import pruning on `database/sql`
