// Package assets — clips_lifecycle_state.go (Blocco 3.1, June 2026)
//
// Companion to clips_index_state.go. SetLifecycleState writes the
// canonical media_assets.lifecycle_state column on the Blocco 3.1
// deletion state machine:
//
//	ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING → DELETED
//
// The Dispatcher (outbox.Dispatcher) does the FIRST-HOP stamp
// (lifecycle_state='DELETE_REQUESTED') inside its own tx because
// that tx also emits the EventAssetDriveDeleteRequested event.
// Subsequent hops (DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING,
// INDEX_DELETE_PENDING → DELETED) flip lifecycle_state either
// standalone (BEFORE-Drive visibility stamp from DriveDeleteHandler)
// or via Dispatcher.AdvanceAndEmit which also emits the next event
// in the same tx.
//
// Like SetIndexState, this method is non-tx-scoped — callers that
// need atomic state-flip + event-emit use Dispatcher.AdvanceAndEmit
// instead. Wire both methods on the same repository so production
// wiring is one &assets.ClipsRepository instance.
//
// Idempotent: writing the same state twice is a no-op write; the
// outbox-pool lease-fence prevents the same worker from racing
// itself.
package assets

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SetLifecycleState writes the canonical media_assets.lifecycle_state
// column on a media_assets row. Blocco 3.1: the column carries the
// 5-explicit-state deletion machine + the 4 legacy states (Staging /
// Processing / Active / DeletePending-legacy / Deleted / Error).
//
// DriveDeleteHandler calls this method for the BEFORE-Drive stamp
// (BEFORE the actual FileLifecycle.Trash/Delete call) so operator
// dashboards see the row in DRIVE_DELETE_PENDING between the
// dispatcher's stamp and the Drive API round-trip. The AFTER-Drive
// stamp (DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING) + next-event
// emit goes through Dispatcher.AdvanceAndEmit, NOT this method, so
// the state-flip and the event-emission commit atomically in one tx.
//
// Caller-side transition validation:
// asset.LifecycleState.IsValidTransition(from, to) is the canonical
// strict-machine table. This repository method does NOT enforce the
// transition table — it accepts any valid LifecycleState value. The
// rationale: the dispatcher and the handler must be able to set the
// same state twice on retry (idempotency) without the repository
// throwing on a no-op self-loop. Strict validation lives at the
// Handler side via the dispatcher's WHERE clause (state = expected
// state pattern), not at the repository.
//
// No lifecycle_state filter — the caller is responsible for picking
// the right state at the right time. CrudClipsRepository callers
// that need to exclude tombstoned rows (live re-index tooling,
// search adapters, etc.) apply a separate filter.
func (r *ClipsRepository) SetLifecycleState(ctx context.Context, id string, state asset.LifecycleState) error {
	if id == "" {
		return fmt.Errorf("clips.SetLifecycleState: id is required")
	}
	if !state.Valid() {
		return fmt.Errorf("clips.SetLifecycleState: state %q is not canonical (use LifecycleState.Valid to check)", state)
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = ?, updated_at = ? WHERE id = ?`,
		string(state), nowStr, id)
	if err != nil {
		return fmt.Errorf("clips.SetLifecycleState(%s, %s): %w", id, state, err)
	}
	return nil
}
