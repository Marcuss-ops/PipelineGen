// Package outbox — drive_delete_test.go (Blocco 3.1 commit 2/3)
//
// Regression tests pinning the DriveDeleteHandler's contract:
//
//   - Idempotency: a row already past DRIVE_DELETE_PENDING
//     (state ∈ {INDEX_DELETE_PENDING, DELETED, "deleted"}) is
//     treated as success with no side effects; the asset row missing
//     entirely is also success (next-hop already finished).
//
//   - Happy path (6-step sequence): pre-flight → stamp DRIVE_DELETE_PENDING
//     → Drive.Trash → AdvanceAndEmit (DRIVE_DELETE_PENDING → DRIVE_DELETED
//
//   - emit EventAssetIndexDeleteRequested). The advancer target is
//     StateDriveDeleted (the post-Drive confirmation hop) per the
//     Blocco 3.1 commit 2/3 state machine expansion: the canonical
//     6-state deletion chain is ACTIVE → DELETE_REQUESTED →
//     DRIVE_DELETE_PENDING → DRIVE_DELETED → INDEX_DELETE_PENDING
//     → INDEX_DELETED → DELETED. The legacy direct-to-
//     INDEX_DELETE_PENDING transition is FORBIDDEN by
//     lifecycle_state.go::IsValidTransition; IndexDeleteHandler
//     accepts both DRIVE_DELETED (new) and INDEX_DELETE_PENDING
//     (legacy forward-compat) as entry points.
//
//   - Drive 404 tolerance: Drive.Delete on an already-deleted
//     fileID is folded to idempotent success.
//
//   - Transient Drive failure: a non-404 Drive error returns a
//     non-terminal error so the outbox pool retries; the row stays
//     in DRIVE_DELETE_PENDING.
//
//   - Empty fileID metadata: handler skips the Drive side-effect
//     and advances directly to INDEX_DELETE_PENDING.
//
//   - Schema mismatch / empty asset_id / missing idempotency_key
//     classified as TERMINAL via the driveLifecycleTerminalErr
//     sentinel.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/api/googleapi"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Mock surfaces ─────────────────────────────────────────────────────

// mockLifecycleStateReader records GetClip calls and returns a
// pre-programmed lifecycle_state for the given asset id.
type mockLifecycleStateReader struct {
	state asset.LifecycleState
	err   error
	nil   bool // when true, returns (nil, nil) regardless of state

	gotIDs []string
}

func (m *mockLifecycleStateReader) GetClip(_ context.Context, id string) (*asset.Asset, error) {
	m.gotIDs = append(m.gotIDs, id)
	if m.err != nil {
		return nil, m.err
	}
	if m.nil {
		return nil, nil
	}
	return &asset.Asset{
		ID:             id,
		Source:         "test",
		Name:           "test-asset",
		Filename:       "test.mp4",
		LifecycleState: m.state,
	}, nil
}

// mockLifecycleStateWriter records SetLifecycleState calls.
type mockLifecycleStateWriter struct {
	calls []setLifecycleCall
	err   error
}

type setLifecycleCall struct {
	id    string
	state asset.LifecycleState
}

func (m *mockLifecycleStateWriter) SetLifecycleState(_ context.Context, id string, state asset.LifecycleState) error {
	m.calls = append(m.calls, setLifecycleCall{id: id, state: state})
	return m.err
}

// mockDriveDeleter records Trash/Delete calls and returns the
// pre-programmed error verbatim. The mock does NOT pre-fold
// 404 → success; the handler's own isDriveNotFoundError gate is
// the unit under test, and a stub that folds before the
// production code sees the error would silently mask a regression
// in the production fold-path. Mocks should faithfully return
// the configured error so the test exercises the handler's
// classification logic, not the stub's classification logic.
type mockDriveDeleter struct {
	trashCalls  []string
	deleteCalls []string
	trashErr    error
	deleteErr   error
}

func (m *mockDriveDeleter) Trash(_ context.Context, fileID string) error {
	m.trashCalls = append(m.trashCalls, fileID)
	return m.trashErr
}

func (m *mockDriveDeleter) Delete(_ context.Context, fileID string) error {
	m.deleteCalls = append(m.deleteCalls, fileID)
	return m.deleteErr
}

// mockStateAdvancer records AdvanceAndEmit calls. Errors as
// programmed.
type mockStateAdvancer struct {
	calls []advanceCall
	err   error
}

type advanceCall struct {
	assetID     string
	fromState   asset.LifecycleState
	newState    asset.LifecycleState
	eventType   string
	payloadJSON []byte
	eventKey    string
}

func (m *mockStateAdvancer) AdvanceAndEmit(
	_ context.Context,
	assetID string,
	fromState, newState asset.LifecycleState,
	eventType string,
	payloadJSON []byte,
	eventKey string,
) error {
	m.calls = append(m.calls, advanceCall{
		assetID:     assetID,
		fromState:   fromState,
		newState:    newState,
		eventType:   eventType,
		payloadJSON: append([]byte(nil), payloadJSON...),
		eventKey:    eventKey,
	})
	return m.err
}

// ── Helpers ──────────────────────────────────────────────────────────

func buildDriveDeleteEvent(t *testing.T, assetID string, permanently bool) outboxevents.Event {
	t.Helper()
	payload, err := json.Marshal(driveDeleteRequestV1{
		SchemaVersion:  DriveDeleteRequestSchemaVersion,
		EventID:        "evt-test",
		AssetID:        assetID,
		Permanently:    permanently,
		IdempotencyKey: "drive_delete:false:" + assetID,
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return outboxevents.Event{
		ID:            1,
		PayloadJSON:   string(payload),
		AggregateID:   assetID,
		AggregateType: "media_asset",
		AttemptCount:  0,
		EventKey:      "drive_delete:false:" + assetID,
	}
}

// ── Tests ────────────────────────────────────────────────────────────

// Case 1 — happy path: full chain completes with the expected order
// of side-effects. STAMP-before-DRIVE → DRIVE → ADVANCE+EMIT.
func TestDriveDeleteHandler_HappyPath_PermanentlyFalse(t *testing.T) {
	reader := &mockLifecycleStateReader{state: asset.StateDeleteRequested}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, reader, writer, adv)

	evt := buildDriveDeleteEvent(t, "asset-happy", false)
	// Inject a Drive fileID via the Asset metadata.
	reader.state = asset.StateDeleteRequested
	// Override the reader to return an asset with a Drive fileID.
	reader.nil = false

	// Override the reader/state — we need an asset WITH a Drive fileID.
	// Go around the mock via a fresh inline reader:
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			a := &asset.Asset{
				ID:             id,
				LifecycleState: asset.StateDeleteRequested,
			}
			a.SetDriveFileID("drive-file-happy")
			return a, nil
		},
	}
	h.stateReader = fr

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("happy-path: Handle returned %v", err)
	}

	// 1. SetLifecycleState stamped DRIVE_DELETE_PENDING.
	if len(writer.calls) != 1 {
		t.Fatalf("expected 1 SetLifecycleState call, got %d", len(writer.calls))
	}
	if writer.calls[0].state != asset.StateDriveDeletePending {
		t.Fatalf("expected DRIVE_DELETE_PENDING stamp, got %s", writer.calls[0].state)
	}

	// 2. Drive.Trash called with the resolved fileID (Permanently=false).
	if len(drive.trashCalls) != 1 || drive.trashCalls[0] != "drive-file-happy" {
		t.Fatalf("Drive.Trash not called once with right id: %+v", drive.trashCalls)
	}
	if len(drive.deleteCalls) != 0 {
		t.Fatalf("Drive.Delete should NOT be called when Permanently=false, got %+v", drive.deleteCalls)
	}

	// 3. AdvanceAndEmit once: from DRIVE_DELETE_PENDING to
	//    DRIVE_DELETED + emit EventAssetIndexDeleteRequested.
	//    Blocco 3.1 commit 2/3 (July 2026) expanded the deletion
	//    state machine to 6 explicit states; the post-Drive
	//    confirmation hop is now StateDriveDeleted. The
	//    IndexDeleteHandler accepts this as its entry point
	//    (alongside legacy INDEX_DELETE_PENDING for pre-commit 2/3
	//    forward-compat), and lifecycle_state.go::IsValidTransition
	//    FORBIDS the direct DRIVE_DELETE_PENDING → INDEX_DELETE_PENDING
	//    transition the previous test asserted.
	if len(adv.calls) != 1 {
		t.Fatalf("expected 1 AdvanceAndEmit call, got %d", len(adv.calls))
	}
	got := adv.calls[0]
	if got.fromState != asset.StateDriveDeletePending || got.newState != asset.StateDriveDeleted {
		t.Fatalf("AdvanceAndEmit state-machine transition wrong: %+v", got)
	}
	if got.eventType != outboxevents.EventAssetIndexDeleteRequested {
		t.Fatalf("AdvanceAndEmit emitted wrong event-type: got %s want %s",
			got.eventType, outboxevents.EventAssetIndexDeleteRequested)
	}
}

// Case 2 — Permanently=true → Drive.Delete.
func TestDriveDeleteHandler_HappyPath_PermanentlyTrue(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			a := &asset.Asset{ID: id, LifecycleState: asset.StateDeleteRequested}
			a.SetDriveFileID("drive-file-perm")
			return a, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	evt := buildDriveDeleteEvent(t, "asset-perm", true)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("happy-path-permanently: Handle returned %v", err)
	}
	if len(drive.deleteCalls) != 1 || drive.deleteCalls[0] != "drive-file-perm" {
		t.Fatalf("Drive.Delete not called once with right id: %+v", drive.deleteCalls)
	}
	if len(adv.calls) != 1 {
		t.Fatalf("expected 1 AdvanceAndEmit, got %d", len(adv.calls))
	}
}

// Case 3 — Drive.Delete returns 404 → folded to idempotent success.
//
// Exercises the new errors.As(*googleapi.Error) + ge.Code ==
// http.StatusNotFound path. The mock constructs a real
// *googleapi.Error (not a plain errors.New with a 404 string)
// because isDriveNotFoundError's errors.As only matches the
// canonical googleapi error type — a plain string error would
// fail to classify and the test would lose its assertion
// fidelity.
func TestDriveDeleteHandler_DeleteNotFoundFoldedSuccess(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			a := &asset.Asset{ID: id, LifecycleState: asset.StateDriveDeletePending}
			a.SetDriveFileID("drive-file-gone")
			return a, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{
		deleteErr: &googleapi.Error{
			Code:    http.StatusNotFound,
			Message: "File not found: drive-file-gone",
		},
	}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	evt := buildDriveDeleteEvent(t, "asset-404", true)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("404 tolerance (Delete): Handle returned %v", err)
	}
	if len(adv.calls) != 1 {
		t.Fatalf("AdvanceAndEmit should fire once on 404 tolerance, got %d", len(adv.calls))
	}
	if len(drive.deleteCalls) != 1 {
		t.Fatalf("Drive.Delete call count: want 1, got %d", len(drive.deleteCalls))
	}
}

// Case 3b — Drive.Trash returns 404 → folded to idempotent success.
//
// Trash is normally idempotent at the Drive API level, but the
// rare Trash→404 (file permanently deleted between Trash
// attempts) ALSO folds to success — the Blocco 3.1 semantic is
// "the file is already gone" = terminal regardless of intent.
// This test pins that the production-code Trash branch also calls
// the same isDriveNotFoundError gate; without it, a future refactor
// could silently regress the Trash→404 fold without Build breaking.
func TestDriveDeleteHandler_TrashNotFoundFoldedSuccess(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			a := &asset.Asset{ID: id, LifecycleState: asset.StateDriveDeletePending}
			a.SetDriveFileID("drive-file-trash-gone")
			return a, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{
		trashErr: &googleapi.Error{
			Code:    http.StatusNotFound,
			Message: "File not found: drive-file-trash-gone",
		},
	}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	evt := buildDriveDeleteEvent(t, "asset-trash-404", false)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("404 tolerance (Trash): Handle returned %v", err)
	}
	if len(adv.calls) != 1 {
		t.Fatalf("AdvanceAndEmit should fire once on Trash-404 tolerance, got %d", len(adv.calls))
	}
	if len(drive.trashCalls) != 1 {
		t.Fatalf("Drive.Trash call count: want 1, got %d", len(drive.trashCalls))
	}
}

// Case 4 — Drive.Trash returns transient error → retryable, row stays
// in DRIVE_DELETE_PENDING.
func TestDriveDeleteHandler_DriveTransientFailure(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			a := &asset.Asset{ID: id, LifecycleState: asset.StateDeleteRequested}
			a.SetDriveFileID("drive-file-503")
			return a, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{
		trashErr: &googleapi.Error{
			Code:    http.StatusServiceUnavailable,
			Message: "Service Unavailable",
		},
	}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	evt := buildDriveDeleteEvent(t, "asset-503", false)

	err := h.Handle(context.Background(), evt)
	if err == nil {
		t.Fatalf("transient Drive failure must return a non-nil error")
	}
	if errors.Is(err, driveLifecycleTerminalErr) {
		t.Fatalf("transient Drive error must NOT be classified as terminal: %v", err)
	}
	if !strings.Contains(err.Error(), "Drive API") {
		t.Fatalf("expected non-terminal error to wrap 'Drive API' marker, got %v", err)
	}
	if len(adv.calls) != 0 {
		t.Fatalf("AdvanceAndEmit should NOT fire on transient Drive failure, got %d", len(adv.calls))
	}
}

// Case 5 — Idempotent skip: asset row missing.
func TestDriveDeleteHandler_AssetRowMissing_IsIdempotentSkip(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			return nil, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	evt := buildDriveDeleteEvent(t, "asset-missing", false)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("missing-asset idempotent skip: Handle returned %v", err)
	}
	if len(writer.calls) != 0 || len(drive.trashCalls) != 0 || len(adv.calls) != 0 {
		t.Fatalf("missing-asset must skip ALL side effects; writer=%d drive=%d adv=%d",
			len(writer.calls), len(drive.trashCalls), len(adv.calls))
	}
}

// Case 6 — Idempotent skip: asset already in INDEX_DELETE_PENDING.
func TestDriveDeleteHandler_AlreadyPastDriveHop_IsIdempotentSkip(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			a := &asset.Asset{ID: id, LifecycleState: asset.StateLifecycleIndexDeletePending}
			a.SetDriveFileID("drive-file-already-gone")
			return a, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	evt := buildDriveDeleteEvent(t, "asset-already", false)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("already-past idempotent skip: Handle returned %v", err)
	}
	if len(writer.calls) != 0 || len(drive.trashCalls) != 0 || len(adv.calls) != 0 {
		t.Fatalf("already-past must skip ALL side effects; writer=%d drive=%d adv=%d",
			len(writer.calls), len(drive.trashCalls), len(adv.calls))
	}
}

// Case 7 — Empty fileID metadata → Drive side-effect skipped, advance
// still fires (the chain still needs the INDEX_DELETE_PENDING hop).
func TestDriveDeleteHandler_EmptyFileID_SkipsDriveAdvancesAnyway(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			// No DriveFileID, no DriveLink, no DownloadLink.
			return &asset.Asset{
				ID:             id,
				LifecycleState: asset.StateDeleteRequested,
			}, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	evt := buildDriveDeleteEvent(t, "asset-no-drive", false)

	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("empty-fileID: Handle returned %v", err)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("expected SetLifecycleState DRIVE_DELETE_PENDING stamp, got %d", len(writer.calls))
	}
	if len(drive.trashCalls) != 0 {
		t.Fatalf("Drive side-effect must be SKIPPED for empty fileID, got %d calls", len(drive.trashCalls))
	}
	if len(adv.calls) != 1 {
		t.Fatalf("AdvanceAndEmit MUST fire even when no Drive side-effect: got %d", len(adv.calls))
	}
}

// Case 8 — Schema mismatch → terminal.
func TestDriveDeleteHandler_SchemaMismatch_Terminal(t *testing.T) {
	fr := &flexLifecycleReader{
		getClip: func(ctx context.Context, id string) (*asset.Asset, error) {
			return &asset.Asset{ID: id, LifecycleState: asset.StateDeleteRequested}, nil
		},
	}
	writer := &mockLifecycleStateWriter{}
	drive := &mockDriveDeleter{}
	adv := &mockStateAdvancer{}

	h := NewDriveDeleteHandler(zap.NewNop(), drive, fr, writer, adv)
	bad, err := json.Marshal(driveDeleteRequestV1{
		SchemaVersion:  "asset.drive.delete_requested.BOGUS",
		EventID:        "evt-bad",
		AssetID:        "asset-bad",
		IdempotencyKey: "drive_delete:false:asset-bad",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	evt := outboxevents.Event{ID: 1, PayloadJSON: string(bad)}

	err = h.Handle(context.Background(), evt)
	if !errors.Is(err, driveLifecycleTerminalErr) {
		t.Fatalf("schema mismatch must be terminal: %v", err)
	}
	if len(adv.calls) != 0 {
		t.Fatalf("schema mismatch must not advance state: got %d AdvanceAndEmit calls", len(adv.calls))
	}
}

// Case 9 — Re-declaration parity check: DriveDeleteEventType in this
// package matches the production outboxevents registry constant.
// Drift = production wiring never routes the event.
func TestDriveDeleteEventType_ParityWithRegistry(t *testing.T) {
	if DriveDeleteEventType != outboxevents.EventAssetDriveDeleteRequested {
		t.Fatalf("DriveDeleteEventType=%s != outboxevents.EventAssetDriveDeleteRequested=%s",
			DriveDeleteEventType, outboxevents.EventAssetDriveDeleteRequested)
	}
}

// ── Inline reader helper ─────────────────────────────────────────────

// flexLifecycleReader lets a test inject a state + Drive metadata
// without the GetClip/SetLifecycleState state-machines coupling
// (the default mockLifecycleStateReader cannot set Drive metadata
// from outside the GetClip test wrapper).
type flexLifecycleReader struct {
	getClip func(context.Context, string) (*asset.Asset, error)
}

func (r *flexLifecycleReader) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return r.getClip(ctx, id)
}
