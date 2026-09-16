// Package media — enrichment_candidate_reader.go: the narrow PostgreSQL read
// behind the VLM auto-tag sweep selector.
//
// WHY THIS EXISTS. internal/capabilities/ai/autotag selected its sweep
// candidates with a SQLite-only statement against the operational handle:
//
//	SELECT id, enrich_state FROM media_assets
//	WHERE enrich_state = 'PENDING'
//	  AND enrich_state_updated_at < datetime('now', ?)
//	  AND media_type != 'folder'
//	  AND local_path != ''
//	ORDER BY enrich_state_updated_at ASC LIMIT ?
//
// media_assets is owned by PostgreSQL, so the selector graded a mirror. The
// selector could not be migrated on its own, though: the sweep then CLAIMS each
// candidate through the enrichment state machine, whose repository port was also
// wired to SQLite. Moving only the read would have left the sweep reading rows it
// could never claim. Both halves now resolve from the same media handle (see
// wiring.enrichStateStoreFromCommitter and pgmedia.MediaEnrichStateStore), so the
// scan and the claim share one engine by construction.
//
// PARITY NOTES — the two places where a literal translation would have CHANGED
// the result set:
//
//  1. THE FENCE COMPARISON. SQLite compared TEXT against datetime('now', ?) with
//     a relative modifier string ('-30 seconds'), so a row whose
//     enrich_state_updated_at was the empty string COMPARED AS OLDER and was
//     therefore SELECTED. Under PostgreSQL the TIMESTAMPTZ mirror of an empty
//     stamp is NULL, and `NULL < x` is NULL — so a literal translation would
//     silently STOP selecting those rows. This reader preserves the SQLite
//     behaviour with `COALESCE(<mirror>, '-infinity')`: a missing stamp means
//     "never stamped", which is the oldest possible candidate, which is exactly
//     what the empty string meant. The same COALESCE is used for ORDER BY so the
//     oldest-first contract is preserved too.
//
//  2. THE MIRROR, NOT THE TEXT COLUMN. The fence reads
//     enrich_state_updated_at_ts because comparing the TEXT sibling would be a
//     lexicographic string comparison in PostgreSQL. The mirror is maintained by
//     the migration-004 dual-write, which MediaEnrichStateStore now honours for
//     this column (the gate percheck_pg_timestamp_dual_write_contract declared
//     the pair but no writer satisfied it before).
//
// The remaining predicates are carried over verbatim. `enrich_state = 'PENDING'`
// and `media_type <> 'folder'` and `local_path <> ”` all exclude NULL under both
// engines for the same reason — a NULL comparison is never true — so no extra
// guard is warranted. Measured: enrich_state, media_type and local_path are all
// NOT NULL on the media SSOT, so those three cases cannot arise at all. The
// `enrich_state = 'PENDING'` predicate is what lets this reader return bare ids:
// it is the contract, not an optimisation.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MediaEnrichmentCandidateReader selects VLM sweep candidates from the
// PostgreSQL media SSOT.
//
// It returns asset IDS ONLY, deliberately. The selector's contract already
// fixes the state (the predicate is `enrich_state = 'PENDING'`), so returning
// the state would mean either a struct shared with the consumer — which this
// package cannot declare, since platform must not import the autotag capability
// — or a mapping adapter at the wiring site for a value no caller acts on. The
// consumer-owned port therefore names ids and its godoc carries the "PENDING
// only" clause as part of the contract.
type MediaEnrichmentCandidateReader struct {
	db *sql.DB
}

// NewMediaEnrichmentCandidateReader returns a reader bound to the PostgreSQL
// media database. The handle is required: a nil database is a programming
// error, not a degrade path.
func NewMediaEnrichmentCandidateReader(db *sql.DB) *MediaEnrichmentCandidateReader {
	if db == nil {
		panic("media.NewMediaEnrichmentCandidateReader: db is required")
	}
	return &MediaEnrichmentCandidateReader{db: db}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *MediaEnrichmentCandidateReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// PendingEnrichCandidates returns the PENDING assets whose enrich_state stamp is
// older than claimFence, oldest first, capped at limit. A non-positive limit is
// treated as the sweep default of 10, mirroring the retired selector.
//
// enrichStateUpdatedAtExpr is the effective ordering/fence timestamp: the typed
// mirror when present, and '-infinity' when absent so an unstamped row is the
// oldest possible candidate (SQLite empty-string parity, see the package note).
const enrichStateUpdatedAtExpr = `COALESCE(enrich_state_updated_at_ts, '-infinity'::timestamptz)`

func (r *MediaEnrichmentCandidateReader) PendingEnrichCandidates(
	ctx context.Context,
	claimFence time.Duration,
	limit int,
) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("media enrichment candidate reader: media SSOT handle is not configured")
	}
	if limit <= 0 {
		limit = 10
	}

	// make_interval(secs => $1) replaces SQLite's relative-modifier string. The
	// fence is passed as seconds, so the two engines express the same instant.
	query := `
		SELECT id FROM media_assets
		WHERE enrich_state = 'PENDING'
		  AND ` + enrichStateUpdatedAtExpr + ` < now() - make_interval(secs => $1)
		  AND media_type <> 'folder'
		  AND local_path <> ''
		ORDER BY ` + enrichStateUpdatedAtExpr + ` ASC
		LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, query, claimFence.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("media enrichment candidate reader: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("media enrichment candidate reader: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media enrichment candidate reader: iterate: %w", err)
	}
	return out, nil
}
