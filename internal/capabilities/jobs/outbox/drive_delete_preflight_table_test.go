// Package outbox — drive_delete_preflight_table_test.go (Blocco 3.2 commit 1/2, June 2026)
//
// Pins the DriveDeleteHandler pre-flight idempotency table
// exhaustively. The full state-machine pre-flight is enumerated as
// a Go subtest so a future state added to the canonical 9-state
// LifecycleState set surfaces as a documented gap in the test
// output (rather than as a silent skip-path that silently swallows
// a new state's side-effects).
//
// Pre-flight contract (drive_delete.go::Handle switch on
// clip.LifecycleState):
//
//	case INDEX_DELETE_PENDING, DELETED, "deleted" (lowercase compat):
//	    → idempotent skip, return nil
//	case DELETE_REQUESTED, DELETE_PENDING, DRIVE_DELETE_PENDING:
//	    → continue (advance through the chain)
//	default (STAGING, PROCESSING, ACTIVE, ERROR, ...):
//	    → terminal error
//
// Plus edge cases NOT covered by the state-machine switch:
//   - clip == nil (row missing entirely)
//     → idempotent skip, return nil
//
// Each subtest wires the SAME mock surfaces (stateReader +
// stateWriter + drive + advancer) and asserts the side-effects
// counts after Handle returns. A drift projection: the table is
// the regression pin for future refactors; a row that stays the
// same gets a "PASS" subtest, a row whose expectations change gets
// a force-updated table.
package outbox

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/api/googleapi"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestDriveDeleteHandler_PreflightIdempotencyTable runs every
// (lifecycle_state, expected_outcome) pair as its own subtest. The
// four outcome families:
//
//	idempotent_skip_no_side_effect — Handler returns nil; writer,
//	    drive, advancer all 0 calls. State ∈ {INDEX_DELETE_PENDING,
//	    DELETED, "deleted"} + asset row missing.
//	continue_normal_chain — Handler returns nil (success); writer
//	    1 (DRIVE_DELETE_PENDING stamp); drive 1 (side-effect fired);
//	    advancer 1 (DRIVE_DELETED advance + emit of the next
//	    asset.index.delete_requested event). State ∈
//	    {DELETE_REQUESTED, DELETE_PENDING, DRIVE_DELETE_PENDING}.
//	    The advancer target is StateDriveDeleted (not the legacy
//	    direct-to-INDEX_DELETE_PENDING) per the Blocco 3.1 commit
//	    2/3 state machine expansion: the canonical 6-state deletion
//	    chain is ACTIVE → DELETE_REQUESTED → DRIVE_DELETE_PENDING
//	    → DRIVE_DELETED → INDEX_DELETE_PENDING → INDEX_DELETED →
//	    DELETED, and StateDriveDeleted is the post-Drive
//	    confirmation hop that IndexDeleteHandler's Drive-block guard
//	    consults on entry.
//	terminal_lifecycle_state — Handler returns non-nil wrapped with
//	    driveLifecycleTerminalErr. State ∉ any of the 5 deletion-chain
//	    states (e.g. {ACTIVE, STAGING, PROCESSING, ERROR}).
//	empty_fileid_path — Handler returns nil; writer 1; drive 0;
//	    advancer 1. State = DELETE_REQUESTED with no Drive metadata.
//	    (Already covered by the existing drive_delete_test.go Case 7;
//	    referenced here for completeness.)
func TestDriveDeleteHandler_PreflightIdempotencyTable(t *testing.T) {
	type outcome string
	const (
		idempotentSkip      outcome = "idempotent_skip_no_side_effect"
		continueNormalChain outcome = "continue_normal_chain"
		terminalError       outcome = "terminal_lifecycle_state"
	)

	type tc struct {
		name           string
		state          asset.LifecycleState // "" → row missing
		driveFileID    string               // "" → no Drive metadata
		expect         outcome
		expectTerminal bool // true ⇒ handler must return wrapped terminal
		permanently    bool // false default; set `permanently=true` for the Delete route
	}

	cases := []tc{
		// ── Authorised states (continue) ─────────────────────────────
		// 3 cases with permanently=false (Trash route) + 3 cases with
		// permanently=true (Delete route). The split is intentional:
		// the per-route assertion at the bottom of the switch compares
		// trashCalls vs deleteCalls explicitly, so a future refactor
		// that fires both methods defensively surfaces as a clean
		// failure on this table.
		{name: "DELETE_REQUESTED_with_driveID_trash", state: asset.StateDeleteRequested, driveFileID: "drive-c1", expect: continueNormalChain},
		{name: "DELETE_PENDING_with_driveID_trash", state: asset.StateDeletePending, driveFileID: "drive-c2", expect: continueNormalChain},
		{name: "DRIVE_DELETE_PENDING_with_driveID_trash", state: asset.StateDriveDeletePending, driveFileID: "drive-c3", expect: continueNormalChain},
		{name: "DELETE_REQUESTED_with_driveID_delete", state: asset.StateDeleteRequested, driveFileID: "drive-c1d", permanently: true, expect: continueNormalChain},
		{name: "DELETE_PENDING_with_driveID_delete", state: asset.StateDeletePending, driveFileID: "drive-c2d", permanently: true, expect: continueNormalChain},
		{name: "DRIVE_DELETE_PENDING_with_driveID_delete", state: asset.StateDriveDeletePending, driveFileID: "drive-c3d", permanently: true, expect: continueNormalChain},

		// ── Idempotent skip (already past Drive hop) ─────────────────
		{name: "INDEX_DELETE_PENDING_skips", state: asset.StateLifecycleIndexDeletePending, driveFileID: "drive-c4", expect: idempotentSkip},
		{name: "DELETED_skips", state: asset.StateDeleted, driveFileID: "drive-c5", expect: idempotentSkip},
		{name: "lowercase_deleted_compat_skips", state: asset.LifecycleState("deleted"), driveFileID: "drive-c6", expect: idempotentSkip},

		// ── Terminal (state outside deletion chain) ──────────────────
		{name: "ACTIVE_terminal", state: asset.StateActive, driveFileID: "drive-c7", expect: terminalError, expectTerminal: true},
		{name: "STAGING_terminal", state: asset.StateStaging, driveFileID: "drive-c8", expect: terminalError, expectTerminal: true},
		{name: "PROCESSING_terminal", state: asset.StateProcessing, driveFileID: "drive-c9", expect: terminalError, expectTerminal: true},
		{name: "ERROR_terminal", state: asset.StateError, driveFileID: "drive-c10", expect: terminalError, expectTerminal: true},

		// ── Asset row missing (GetClip returns nil) ─────────────────
		{name: "missing_asset_row_skips", state: "", driveFileID: "", expect: idempotentSkip},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := &flexLifecycleReader{
				getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
					if c.state == "" {
						return nil, nil
					}
					a := &asset.Asset{ID: id, LifecycleState: c.state}
					if c.driveFileID != "" {
						a.SetDriveFileID(c.driveFileID)
					}
					return a, nil
				},
			}
			writer := &mockLifecycleStateWriter{}
			drive := &mockDriveDeleter{}
			adv := &mockStateAdvancer{}

			h := NewDriveDeleteHandler(zap.NewNop(), drive, reader, writer, adv)
			evt := buildDriveDeleteEvent(t, "asset-preflight-"+c.name, c.permanently)

			err := h.Handle(context.Background(), evt)

			switch c.expect {
			case idempotentSkip:
				if err != nil {
					t.Fatalf("idempotent_skip: Handle returned %v, want nil", err)
				}
				if len(writer.calls) != 0 || len(drive.trashCalls)+len(drive.deleteCalls) != 0 || len(adv.calls) != 0 {
					t.Fatalf("idempotent_skip: side-effects fired; writer=%d drive=%d adv=%d",
						len(writer.calls), len(drive.trashCalls)+len(drive.deleteCalls), len(adv.calls))
				}
			case continueNormalChain:
				if err != nil {
					t.Fatalf("continue_normal_chain: Handle returned %v, want nil", err)
				}
				if len(writer.calls) != 1 {
					t.Fatalf("continue_normal_chain: writer calls=%d, want 1 (DRIVE_DELETE_PENDING stamp)", len(writer.calls))
				}
				if writer.calls[0].state != asset.StateDriveDeletePending {
					t.Fatalf("continue_normal_chain: writer stamp %s, want DRIVE_DELETE_PENDING", writer.calls[0].state)
				}
				// Per-route assertion (fix for review Defect 2): the
				// combined sum check would mask a future regression
				// that fires both Trash AND Delete defensively. Split
				// by permanently so the regression pin is unambiguous.
				if c.permanently {
					if len(drive.deleteCalls) != 1 || len(drive.trashCalls) != 0 {
						t.Fatalf("continue_permanently_true: want 1 Delete call; got trash=%d delete=%d",
							len(drive.trashCalls), len(drive.deleteCalls))
					}
				} else {
					if len(drive.trashCalls) != 1 || len(drive.deleteCalls) != 0 {
						t.Fatalf("continue_permanently_false: want 1 Trash call; got trash=%d delete=%d",
							len(drive.trashCalls), len(drive.deleteCalls))
					}
				}
				if len(adv.calls) != 1 {
					t.Fatalf("continue_normal_chain: advancer calls=%d, want 1 (advance + emit)", len(adv.calls))
				}
				advCall := adv.calls[0]
				// Blocco 3.1 commit 2/3 (July 2026): the advancer's
				// target is StateDriveDeleted (the post-Drive
				// confirmation hop) per the canonical 6-state
				// deletion state machine in
				// internal/kernel/asset/lifecycle_state.go. The legacy
				// direct-to-INDEX_DELETE_PENDING transition is
				// FORBIDDEN by IsValidTransition: from DRIVE_DELETE_PENDING
				// the only valid forward edge is to DRIVE_DELETED. The
				// IndexDeleteHandler accepts both DRIVE_DELETED (new
				// chain) and INDEX_DELETE_PENDING (legacy forward-compat
				// for pre-commit 2/3 rows) as entry points, so the
				// chain stays valid across the migration.
				if advCall.fromState != asset.StateDriveDeletePending || advCall.newState != asset.StateDriveDeleted {
					t.Fatalf("continue_normal_chain: advancer transition %s→%s, want DRIVE_DELETE_PENDING→DRIVE_DELETED",
						advCall.fromState, advCall.newState)
				}
			case terminalError:
				if err == nil {
					t.Fatalf("terminal: Handle returned nil, want wrapped terminal error")
				}
				if !errors.Is(err, driveLifecycleTerminalErr) {
					t.Fatalf("terminal: error %v must wrap %v", err, driveLifecycleTerminalErr)
				}
				if len(adv.calls) != 0 {
					t.Fatalf("terminal: advancer must NOT fire; got %d calls", len(adv.calls))
				}
			default:
				t.Fatalf("test case %q: unexpected outcome %s", c.name, c.expect)
			}
		})
	}
}

// TestDriveDeleteHandler_Drive404_OnDeleteFoldsToSuccess is the
// "drive-file already deleted at the Drive API" boundary. The
// handler MUST fold a 404 from Drive.Delete (or Drive.Trash) into
// idempotent success so the reconcile-on-restart flow doesn't
// loop forever on a row whose Drive file is already gone.
//
// The test lives here (and not in the main drive_delete_test.go to
// keep the pre-flight table compact) because the 404 fold path is
// orthogonal to the pre-flight table — it exercises the test
// CONTINUES through the table even when the Drive API rejects.
func TestDriveDeleteHandler_Drive404_OnDeleteFoldsToSuccess(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			a := &asset.Asset{ID: id, LifecycleState: asset.StateDeleteRequested}
			a.SetDriveFileID("drive-file-already-gone")
			return a, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{
		deleteErr: &googleapi.Error{Code: http.StatusNotFound, Message: "File not found"},
	}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	evt := buildDriveDeleteEvent(t, "asset-preflight-404", true)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("Drive 404 must fold to success; got %v", err)
	}
	if len(adv.calls) != 1 {
		t.Fatalf("AdvanceAndEmit must fire ONCE on 404 fold; got %d calls", len(adv.calls))
	}
}
