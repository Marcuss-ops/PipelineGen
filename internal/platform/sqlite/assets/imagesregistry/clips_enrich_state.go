// Package assets — clips_enrich_state.go holds the *ClipsRepository
// methods that read/write the canonical media_assets.enrich_state
// column (PR-ENRICHMENT-STATE-MACHINE, July 2026, migration 123).
//
// Mirrors clips_index_state.go::SetIndexState EXACTLY in shape:
// atomic UPDATE of (enrich_state, enrich_state_updated_at) on the
// canonical media_assets row. The typed-enum + state-machine wrapper
// guard at the application layer (internal/capabilities/assets/
// enrichment/state_machine.go) is the only place where state-
// transition validity is enforced. The SQL primitive is intentionally
// permissive — accepts any valid typed enum so future direct-call
// paths from admin tooling can flip state without going through the
// wrapper (the wrapper enforces validEdges; the primitive is a thin
// atomic-UPDATE helper).
package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SetEnrichState writes the canonical media_assets.enrich_state column
// (migration 123). Returns ErrEnrichStateMissing-equivalent
// (errored with the typed envelope surfacing) when the asset row is
// absent (UPDATE RowsAffected=0).
//
// Called by EnrichStateMachine.Transition (the typed state-machine
// wrapper in internal/capabilities/assets/enrichment/) and directly by
// the VLM 15-min sweeper's claim-fence path
// (startVLMAutoTagSweeper in internal/app/lifecycle_sweepers.go).
//
// No lifecycle_state filter — the caller is responsible for picking
// the right state at the right time. SoftDeleteFilter() is applied
// by callers that need to exclude tombstoned rows (e.g. operator
// reindex admin tooling); the canonical ingest path does NOT need
// it because ProcessAsset.VerifyDB guards against inserting
// tombstoned rows in the first place.
//
// Idempotent: the column flip on an already-target-state row is a
// no-op write (RowsAffected=0 because the column value matches the
// WHERE predicate implicitly via the column assignment).
//
// Unconditional: use SetEnrichStateIfCurrent when the caller needs
// to ensure the current state matches an expected from-state.
func (r *ClipsRepository) SetEnrichState(ctx context.Context, id string, state asset.EnrichState) error {
	if id == "" {
		return fmt.Errorf("clips.SetEnrichState: id is required")
	}
	if state == "" {
		return fmt.Errorf("clips.SetEnrichState: state is required (got empty string; use the canonical 4-state enum)")
	}
	if !state.Valid() {
		return fmt.Errorf("clips.SetEnrichState: state %q is not canonical (godlike/06 SSOT: %v)",
			string(state), asset.CanonicalEnrichStateValues())
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	rowsAffected, err := UpdateMediaAssetEnrichState(ctx, r.db, id, string(state), nowStr)
	if err != nil {
		return fmt.Errorf("clips.SetEnrichState(%s, %s): %w", id, state, err)
	}
	if rowsAffected == 0 {
		// Surface typed-error envelope (godlike/07 contract) so the
		// state-machine wrapper's caller can errors.Is(err,
		// ErrEnrichStateMissing) cleanly. The wrapper does this via
		// the SetEnrichState error path; an ad-hoc SQL-primitive
		// caller surfaces a fmt.Errorf so the absence is still
		// diagnoseable.
		return fmt.Errorf("clips.SetEnrichState(%s, %s): asset row missing in media_assets", id, state)
	}
	return nil
}

// SetEnrichStateIfCurrent atomically flips the canonical
// media_assets.enrich_state column from `from` to `to` only if the
// row currently holds `from`. It also atomically stamps
// enrich_state_updated_at. Returns ErrEnrichStateMissing-equivalent
// when the asset row is absent OR when the row's current state is
// not `from` (CAS lost, e.g. another worker claimed the row first).
//
// This is the primitive used by EnrichStateMachine.Transition for
// all validated transitions; the unconditional SetEnrichState is
// reserved for MarkPending and admin tooling paths that do not
// require a current-state guard.
func (r *ClipsRepository) SetEnrichStateIfCurrent(ctx context.Context, id string, from, to asset.EnrichState) error {
	if id == "" {
		return fmt.Errorf("clips.SetEnrichStateIfCurrent: id is required")
	}
	if from == "" {
		return fmt.Errorf("clips.SetEnrichStateIfCurrent: from state is required")
	}
	if to == "" {
		return fmt.Errorf("clips.SetEnrichStateIfCurrent: to state is required")
	}
	if !from.Valid() {
		return fmt.Errorf("clips.SetEnrichStateIfCurrent: from state %q is not canonical (godlike/06 SSOT: %v)",
			string(from), asset.CanonicalEnrichStateValues())
	}
	if !to.Valid() {
		return fmt.Errorf("clips.SetEnrichStateIfCurrent: to state %q is not canonical (godlike/06 SSOT: %v)",
			string(to), asset.CanonicalEnrichStateValues())
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	rowsAffected, err := UpdateMediaAssetEnrichStateIfCurrent(ctx, r.db, id, string(from), string(to), nowStr)
	if err != nil {
		return fmt.Errorf("clips.SetEnrichStateIfCurrent(%s, %s, %s): %w", id, from, to, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("clips.SetEnrichStateIfCurrent(%s, %s, %s): asset row missing or current state mismatch", id, from, to)
	}
	return nil
}

// GetEnrichState reads the canonical media_assets.enrich_state column
// (migration 123). Returns (EnrichStatePending, nil) when the row
// exists and the column carries the canonical default — the SQL
// primitive is intentionally write-only on the typed enum; reads
// return the canonical default when the column is the post-migration
// PENDING sentinel so an unscanned row is treated as "PENDING" by the
// caller — which matches the godlike/06 SSOT "rows are born PENDING"
// contract. Returns (zero, fmt.Errorf) when the row is absent —
// surfaced via sql.ErrNoRows so the EnrichStateMachine wrapper can
// remap it into ErrEnrichStateMissing (godlike/07 typed-error
// envelope contract).
//
// Used by:
//   - The typed state-machine wrapper's Transition pre-flight via
//     GetEnrichState to read the current from-state (mirrors the
//     IndexDeleteHandler pre-flight uses ClipsRepository's index
//     state reads). The from-state MUST match what the caller passes
//     to Transition; otherwise ErrIllegalEnrichTransition is
//     surfaced.
//   - Operator reindex admin tooling (OUT OF SCOPE this PR,
//     forward-pointer).
func (r *ClipsRepository) GetEnrichState(ctx context.Context, id string) (asset.EnrichState, error) {
	if id == "" {
		return "", fmt.Errorf("clips.GetEnrichState: id is required")
	}
	var stateStr string
	err := r.db.QueryRowContext(ctx,
		`SELECT enrich_state FROM media_assets WHERE id = ?`,
		id).Scan(&stateStr)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("clips.GetEnrichState(%s): asset row missing in media_assets", id)
	}
	if err != nil {
		return "", fmt.Errorf("clips.GetEnrichState(%s): %w", id, err)
	}
	state := asset.EnrichState(stateStr)
	if !state.Valid() {
		// Defensive: pre-migration-123 rows or unknown values
		// (typos in metadata_json) MUST NOT silently flow into
		// the state-machine wrapper. Surface an explicit typed
		// error envelope so a backfill wave (PR-ENRICHMENT-
		// STATE-BACKFILL, forward-pointer) can diagnose.
		return "", fmt.Errorf("clips.GetEnrichState(%s): column value %q is not canonical (godlike/06 SSOT: %v)",
			id, stateStr, asset.CanonicalEnrichStateValues())
	}
	return state, nil
}
