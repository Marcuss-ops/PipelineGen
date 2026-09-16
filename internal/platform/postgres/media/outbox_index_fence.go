// Package media — outbox_index_fence.go: the FENCED terminal INDEXED
// transition for the canonical index worker.
//
// Why this file exists (September 2026 audit, POSTGRES-MEDIA-CUTOVER
// follow-up): the worker used to flip the row with an UNCONDITIONAL
//
//	UPDATE media_assets SET index_state='INDEXED' WHERE id = $1
//
// That write has no fence, so it raced the deletion saga:
//
//	asset.index.requested still pending
//	  → operator deletes the asset (index_delete → index_state='DELETE_PENDING')
//	  → index_delete completes (index_state='DELETED' + media_embeddings retired)
//	  → the stale index event is claimed LAST
//	  → the unconditional flip resurrects the row as INDEXED and re-inserts
//	    a vector for an asset that is gone.
//
// The terminal INDEXED state is documented as a CAS
// (mutations.go::SetMediaAssetIndexed fences on `source_version` +
// `index_state='INDEXING'`), but nothing on the PostgreSQL media plane ever
// writes 'INDEXING' — that hop belonged to the retired Qdrant clipindexer.
// The worker therefore owns its own fence, and the fence it needs is the
// RETIREMENT fence, not the supersede fence:
//
//   - the supersede (source_version) gate is unnecessary on this plane: the
//     worker resolves the vector from the LIVE row (EmbedAssetText reads
//     search_text at embed time), not from an event snapshot, so an
//     out-of-order duplicate re-embeds current content and converges.
//   - retirement is NOT self-healing: re-writing 'INDEXED' after 'DELETED'
//     silently re-publishes a deleted asset in semantic search.
//
// The retired set is closed and derived from the canonical
// kernel/asset.IndexState enum so it cannot drift from the owner of the fact.
//
// Transaction contract: the fence is the FIRST statement of the caller's
// transaction, so a retirement that wins the race rolls the whole tx back —
// the vector is never written for an asset the fence rejected.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// indexFenceKind is the closed outcome of applyIndexedFenceTx.
type indexFenceKind int

const (
	// indexFenceApplied — the row was flipped to INDEXED; the caller may
	// write the vector and commit.
	indexFenceApplied indexFenceKind = iota
	// indexFenceAssetMissing — no media_assets row owns the id. Fail
	// closed (never write an orphan vector); the caller owns the retry
	// decision, exactly as before this fence existed.
	indexFenceAssetMissing
	// indexFenceAssetRetired — the row exists but its index_state forbids
	// indexing forever. Terminal: retrying can never make it applicable.
	indexFenceAssetRetired
)

// indexFenceOutcome couples the decision with its operator-facing reason.
type indexFenceOutcome struct {
	Kind   indexFenceKind
	Reason string
}

// retiredIndexStates returns the canonical closed set of index_state values
// that FORBID the terminal INDEXED transition: a row being retired
// (DELETE_PENDING) and a retired row (DELETED).
//
// Restore is deliberately NOT blocked by this set: the restore producer
// (delete_saga.go::EnqueueAndRestore) stamps index_state=DISCOVERED in its own
// transaction BEFORE emitting the canonical index request, so a restored asset
// reaches the worker already outside the retired set.
func retiredIndexStates() []string {
	return []string{string(asset.StateIndexDeletePending), string(asset.StateDELETED)}
}

// retiredIndexStatePredicate renders `COALESCE(<column>, ”) NOT IN ($n, …)`
// and appends the bound values to args starting at ordinal `first`.
//
// The predicate is built from retiredIndexStates so a future state added to the
// canonical enum list cannot be silently ignored by the SQL.
func retiredIndexStatePredicate(column string, args *[]any, first int) string {
	states := retiredIndexStates()
	placeholders := make([]string, 0, len(states))
	for i, state := range states {
		*args = append(*args, state)
		placeholders = append(placeholders, fmt.Sprintf("$%d", first+i))
	}
	return fmt.Sprintf("COALESCE(%s, '') NOT IN (%s)", column, strings.Join(placeholders, ", "))
}

// applyIndexedFenceTx performs the FENCED terminal INDEXED transition inside
// the caller-owned transaction and reports what happened.
//
// Exactly one row is expected: media_assets.id is the primary key, so a
// rows-affected of 1 means the fence passed and anything else means the fence
// rejected the row (absent or retired). A 0-row result is NOT an error — it is
// the fence doing its job — so the outcome is classified, not thrown.
func applyIndexedFenceTx(ctx context.Context, tx *sql.Tx, assetID, updatedAt string) (indexFenceOutcome, error) {
	if tx == nil {
		return indexFenceOutcome{}, errors.New("media index fence: transaction is required")
	}
	if strings.TrimSpace(assetID) == "" {
		return indexFenceOutcome{}, errors.New("media index fence: asset id is required")
	}
	if strings.TrimSpace(updatedAt) == "" {
		return indexFenceOutcome{}, errors.New("media index fence: updated-at is required")
	}

	args := []any{string(asset.StateIndexed), updatedAt, assetID}
	predicate := retiredIndexStatePredicate("index_state", &args, len(args)+1)
	// Dual-write expand (BASELINE_PLAN.md §5.2, parity with the canonical
	// UpdateMediaAssetIndexState): the same RFC3339 string is written to the
	// legacy TEXT column AND mirrored into the TIMESTAMPTZ column. Writing
	// only the TEXT side left the two representations of one fact diverging.
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET index_state = $1,
		    index_state_updated_at = $2,
		    index_state_updated_at_ts = NULLIF($2, '')::timestamptz
		WHERE id = $3 AND `+predicate,
		args...)
	if err != nil {
		return indexFenceOutcome{}, fmt.Errorf("media index fence: flip asset %q: %w", assetID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return indexFenceOutcome{}, fmt.Errorf("media index fence: rows affected asset %q: %w", assetID, err)
	}
	if affected == 1 {
		return indexFenceOutcome{Kind: indexFenceApplied}, nil
	}

	// The fence rejected the row. Classify it on the SAME transaction so the
	// observed state is the one the fence compared against.
	var state string
	readErr := tx.QueryRowContext(ctx,
		`SELECT COALESCE(index_state, '') FROM media_assets WHERE id = $1`, assetID,
	).Scan(&state)
	if errors.Is(readErr, sql.ErrNoRows) {
		return indexFenceOutcome{Kind: indexFenceAssetMissing, Reason: "asset_not_found"}, nil
	}
	if readErr != nil {
		return indexFenceOutcome{}, fmt.Errorf("media index fence: read index_state asset %q: %w", assetID, readErr)
	}
	state = strings.TrimSpace(state)
	return indexFenceOutcome{
		Kind:   indexFenceAssetRetired,
		Reason: "asset_retired:" + state,
	}, nil
}
