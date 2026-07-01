package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	outboxhandlers "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ── Fakes ────────────────────────────────────────────────────────────
//
// mockQdrantDeleter and mockAssetDeleter satisfy the handler's local
// port interfaces (Pattern 0 — declared inside index_delete.go, not in
// domain). Each fake records invocations so tests can assert both
// SUCCESSFUL calls AND non-calls (idempotent pre-flight).

type mockQdrantDeleter struct {
	deleteCalls [][]string
	err         error
}

func (m *mockQdrantDeleter) DeletePoints(ctx context.Context, ids []string) error {
	if m.err != nil {
		return m.err
	}
	copied := append([]string(nil), ids...)
	m.deleteCalls = append(m.deleteCalls, copied)
	return nil
}

func (m *mockQdrantDeleter) callCount() int { return len(m.deleteCalls) }

// indexStateCall records a SetIndexState call (IndexState column).
type indexStateCall struct {
	ID    string
	State asset.IndexState
}

// lifecycleStateCall records a SetLifecycleState call (LilycleState
// column). Kept structurally separate from indexStateCall so a future
// regression that mixes the two is a build failure (different concrete
// types in State field).
type lifecycleStateCall struct {
	ID    string
	State asset.LifecycleState
}

type mockAssetDeleter struct {
	getResult            *asset.Asset
	getErr               error
	softDeleteIDs        []string
	softErr              error
	indexStateCalls      []indexStateCall
	setStateErr          error
	lifecycleStateCalls  []lifecycleStateCall
	setLifecycleStateErr error
}

func (m *mockAssetDeleter) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return m.getResult, m.getErr
}

func (m *mockAssetDeleter) SoftDelete(ctx context.Context, id string) error {
	// Always record the call regardless of error so softDeleteCount()
	// answers "how many times was SoftDelete invoked?" — not "how many
	// succeeded?" Tests that assert a successful write then check the
	// return value separately; tests that assert a non-call check
	// length-zero, which is unaffected by this change.
	m.softDeleteIDs = append(m.softDeleteIDs, id)
	return m.softErr
}

func (m *mockAssetDeleter) softDeleteCount() int { return len(m.softDeleteIDs) }

// SetIndexState records the (id, state) pair so tests can assert the
// canonical DELETE_PENDING → DELETED transitions. Mirrors the same
// "always record, optionally error" pattern as SoftDelete so a call
// is observable even when setStateErr is non-nil.
func (m *mockAssetDeleter) SetIndexState(ctx context.Context, id string, state asset.IndexState) error {
	m.indexStateCalls = append(m.indexStateCalls, indexStateCall{ID: id, State: state})
	if m.setStateErr != nil {
		return m.setStateErr
	}
	return nil
}

func (m *mockAssetDeleter) indexStateTransitions() []indexStateCall {
	return m.indexStateCalls
}

// SetLifecycleState records the (id, state) pair so tests can assert
// the canonical terminal hop lifecycle_state=INDEX_DELETE_PENDING →
// DELETED that closes the Blocco 3.1 deletion state machine. Mirrors
// the "always record, optionally error" pattern used by SoftDelete
// and SetIndexState so a call is observable even when
// setLifecycleStateErr is non-nil.
func (m *mockAssetDeleter) SetLifecycleState(ctx context.Context, id string, state asset.LifecycleState) error {
	m.lifecycleStateCalls = append(m.lifecycleStateCalls, lifecycleStateCall{ID: id, State: state})
	if m.setLifecycleStateErr != nil {
		return m.setLifecycleStateErr
	}
	return nil
}

func (m *mockAssetDeleter) lifecycleStateTransitions() []lifecycleStateCall {
	return m.lifecycleStateCalls
}

// ── Helpers ───────────────────────────────────────────────────────────

// validIndexDeletePayload builds a v1 envelope. The schema_version
// literal is sourced from outboxhandlers.DeleteRequestSchemaVersion so
// a future version bump automatically updates test fixtures in lockstep.
// assetID and idemKey are the caller's choice; tests that exercise the
// schema-version-mismatch path build the JSON inline so they can pin a
// wrong literal deliberately.
func validIndexDeletePayload(t *testing.T, assetID, idemKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"schema_version":  outboxhandlers.DeleteRequestSchemaVersion,
		"event_id":        "evt-" + idemKey,
		"asset_id":        assetID,
		"requested_at":    "2026-06-25T12:00:00Z",
		"idempotency_key": idemKey,
	})
	if err != nil {
		t.Fatalf("marshal valid payload: %v", err)
	}
	return string(body)
}

func deleteEvt(t *testing.T, payload string) outboxevents.Event {
	t.Helper()
	return outboxevents.Event{
		ID:           100,
		EventType:    outboxevents.EventAssetIndexDeleteRequested,
		AggregateID:  "agg-1",
		PayloadJSON:  payload,
		AttemptCount: 1,
		MaxAttempts:  10,
	}
}

// ── Tests ─────────────────────────────────────────────────────────────

// TestIndexDeleteHandler_EventType pins the EventType ↔ constant
// tie. A drop-in refactor that flips the literal would fail this
// test loudly — same pattern as IndexingHandler / MetadataExportHandler.
func TestIndexDeleteHandler_EventType(t *testing.T) {
	h := outboxhandlers.NewIndexDeleteHandler(
		zap.NewNop(),
		&mockQdrantDeleter{},
		&mockAssetDeleter{},
	)
	if got := h.EventType(); got != outboxevents.EventAssetIndexDeleteRequested {
		t.Errorf("expected %q got %q", outboxevents.EventAssetIndexDeleteRequested, got)
	}
}

// TestIndexDeleteHandler_PayloadParseIsTerminal: malformed JSON →
// PR1's NewTerminalError so the pool dead-letters immediately.
func TestIndexDeleteHandler_PayloadParseIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexDeleteHandler(
		zap.NewNop(),
		&mockQdrantDeleter{},
		&mockAssetDeleter{},
	)
	err := h.Handle(context.Background(), deleteEvt(t, `{ not json`))
	if err == nil {
		t.Fatal("expected error on malformed JSON; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("malformed JSON must be terminal (prevents max_attempts repair loop); got: %v", err)
	}
}

// TestIndexDeleteHandler_SchemaVersionMismatchIsTerminal: any
// version literal that isn't exactly DeleteRequestSchemaVersion is
// terminal — producers must upgrade instead of retrying.
func TestIndexDeleteHandler_SchemaVersionMismatchIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexDeleteHandler(
		zap.NewNop(),
		&mockQdrantDeleter{},
		&mockAssetDeleter{},
	)
	payload := `{"schema_version":"asset.index.delete_requested.v0","event_id":"e","asset_id":"a","idempotency_key":"i"}`
	err := h.Handle(context.Background(), deleteEvt(t, payload))
	if err == nil {
		t.Fatal("expected error on schema_version mismatch; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("schema_version mismatch must be terminal; got: %v", err)
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error message lost the diagnostic hint: %q", err.Error())
	}
}

// TestIndexDeleteHandler_EmptyAssetIDIsTerminal: empty asset_id
// cannot appear via retry — terminal so the producer fixes it.
func TestIndexDeleteHandler_EmptyAssetIDIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexDeleteHandler(
		zap.NewNop(),
		&mockQdrantDeleter{},
		&mockAssetDeleter{},
	)
	payload := `{"schema_version":"asset.index.delete_requested.v1","event_id":"e","asset_id":"","idempotency_key":"i"}`
	err := h.Handle(context.Background(), deleteEvt(t, payload))
	if err == nil {
		t.Fatal("expected error on empty asset_id; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("empty asset_id must be terminal; got: %v", err)
	}
}

// TestIndexDeleteHandler_MissingAssetIdempotentSuccess: the asset
// row is already gone (deleted elsewhere; or never existed). The
// handler MUST return nil so the pool MarksCompleted, and MUST NOT
// call either deleter — Qdrant call would be cheap but pointless
// (and SoftDelete would no-op the same way). The terminal
// lifecycle_state=DELETED hop must also NOT fire (no asset row to
// write to).
func TestIndexDeleteHandler_MissingAssetIdempotentSuccess(t *testing.T) {
	assets := &mockAssetDeleter{getResult: nil}
	qdrant := &mockQdrantDeleter{}
	h := outboxhandlers.NewIndexDeleteHandler(zap.NewNop(), qdrant, assets)

	err := h.Handle(context.Background(), deleteEvt(t, validIndexDeletePayload(t, "clip-ghost", "idem-ghost")))
	if err != nil {
		t.Fatalf("missing asset should be idempotent success; got: %v", err)
	}
	if qdrant.callCount() != 0 {
		t.Errorf("Qdrant.DeletePoints must NOT be called when asset row is absent (got %d)", qdrant.callCount())
	}
	if assets.softDeleteCount() != 0 {
		t.Errorf("SoftDelete must NOT be called when asset row is absent (got %d)", assets.softDeleteCount())
	}
	if len(assets.lifecycleStateCalls) != 0 {
		t.Errorf("SetLifecycleState must NOT be called when asset row is absent (got %d calls)", len(assets.lifecycleStateCalls))
	}
}

// TestIndexDeleteHandler_AlreadyDeletedCanonicalSuccess: lifecycle_state
// is already 'DELETED' (canonical post-PR4). Return idempotent
// success; no deleter calls. The terminal lifecycle_state=DELETED
// hop must also NOT fire (the row is already in DELETED, writing
// again would be observed as a redundant state flip).
func TestIndexDeleteHandler_AlreadyDeletedCanonicalSuccess(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{
			ID:             "clip-already-gone",
			LifecycleState: asset.StateDeleted, // "DELETED"
		},
	}
	qdrant := &mockQdrantDeleter{}
	h := outboxhandlers.NewIndexDeleteHandler(zap.NewNop(), qdrant, assets)

	err := h.Handle(context.Background(), deleteEvt(t, validIndexDeletePayload(t, "clip-already-gone", "idem-x")))
	if err != nil {
		t.Fatalf("canonical 'DELETED' asset should be idempotent success; got: %v", err)
	}
	if qdrant.callCount() != 0 {
		t.Errorf("already-deleted asset must NOT trigger Qdrant call (got %d)", qdrant.callCount())
	}
	if assets.softDeleteCount() != 0 {
		t.Errorf("already-deleted asset must NOT trigger SoftDelete (got %d)", assets.softDeleteCount())
	}
	if len(assets.lifecycleStateCalls) != 0 {
		t.Errorf("SetLifecycleState must NOT fire on already-DELETED pre-flight (got %d calls)", len(assets.lifecycleStateCalls))
	}
}

// TestIndexDeleteHandler_AlreadyDeletedLegacySuccess: lifecycle_state
// is lowercase 'deleted' (legacy SoftDelete output). PR2 also treats
// it as a done state — pins the dual-casing invariant.
func TestIndexDeleteHandler_AlreadyDeletedLegacySuccess(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{
			ID:             "clip-legacy-gone",
			LifecycleState: asset.LifecycleState("DELETED"),
		},
	}
	qdrant := &mockQdrantDeleter{}
	h := outboxhandlers.NewIndexDeleteHandler(zap.NewNop(), qdrant, assets)

	err := h.Handle(context.Background(), deleteEvt(t, validIndexDeletePayload(t, "clip-legacy-gone", "idem-y")))
	if err != nil {
		t.Fatalf("lowercase 'deleted' asset should also be idempotent success; got: %v", err)
	}
	if qdrant.callCount() != 0 {
		t.Errorf("lowercase deleted asset must NOT trigger Qdrant call (got %d)", qdrant.callCount())
	}
}

// TestIndexDeleteHandler_HappyPathTransitionsToDeleted: live asset
// (state = 'ready'). Verify BOTH the Qdrant call AND the SoftDelete
// call happen in this exact order, with the right asset_id. The
// terminal lifecycle_state=DELETED hop must fire AFTER Qdrant
// delete + SoftDelete + index_state=DELETED — pinning the full
// state-machine close for the Blocco 3.1 audit.
func TestIndexDeleteHandler_HappyPathTransitionsToDeleted(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{
			ID:             "clip-to-delete",
			LifecycleState: asset.StateActive,
		},
	}
	qdrant := &mockQdrantDeleter{}
	h := outboxhandlers.NewIndexDeleteHandler(zap.NewNop(), qdrant, assets)

	err := h.Handle(context.Background(), deleteEvt(t, validIndexDeletePayload(t, "clip-to-delete", "idem-z")))
	if err != nil {
		t.Fatalf("happy path should succeed; got: %v", err)
	}
	if qdrant.callCount() != 1 {
		t.Fatalf("Qdrant.DeletePoints must be called exactly once on success; got %d", qdrant.callCount())
	}
	if n := assets.softDeleteCount(); n != 1 {
		t.Fatalf("SoftDelete must be called once on success; got %d", n)
	}
	if qdrant.deleteCalls[0][0] != "clip-to-delete" {
		t.Errorf("Qdrant called with wrong id: %q", qdrant.deleteCalls[0][0])
	}
	if assets.softDeleteIDs[0] != "clip-to-delete" {
		t.Errorf("SoftDelete called with wrong id: %q", assets.softDeleteIDs[0])
	}
	// index_state must reach DELETED exactly once.
	if len(assets.indexStateCalls) != 2 {
		t.Fatalf("expected 2 index_state flips on happy path (DELETE_PENDING + DELETED); got %d", len(assets.indexStateCalls))
	}
	if assets.indexStateCalls[0].State != asset.StateIndexDeletePending {
		t.Errorf("first index_state flip must be DELETE_PENDING; got %q", assets.indexStateCalls[0].State)
	}
	if assets.indexStateCalls[1].State != asset.StateDELETED {
		t.Errorf("second index_state flip must be DELETED; got %q", assets.indexStateCalls[1].State)
	}
	// Lifecycle_state must reach DELETED exactly once (terminal hop).
	if len(assets.lifecycleStateCalls) != 1 {
		t.Fatalf("lifecycle_state must flip to DELETED exactly once on success (terminal hop); got %d calls", len(assets.lifecycleStateCalls))
	}
	if assets.lifecycleStateCalls[0].State != asset.StateDeleted {
		t.Errorf("lifecycle_state terminal hop must be DELETED; got %q", assets.lifecycleStateCalls[0].State)
	}
	if assets.lifecycleStateCalls[0].ID != "clip-to-delete" {
		t.Errorf("lifecycle_state terminal hop called with wrong id: %q", assets.lifecycleStateCalls[0].ID)
	}
}

// TestIndexDeleteHandler_LifecycleStateAdvancesToDeleted: a
// narrower-form mirror of the happy-path test that focuses the
// assertion on the lifecycle_state hop. Future regressions that
// drop the terminal flip will fail this test loudly even if they
// preserve all other invariants.
func TestIndexDeleteHandler_LifecycleStateAdvancesToDeleted(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{
			ID:             "clip-terminal-hop",
			LifecycleState: asset.StateLifecycleIndexDeletePending, // mid-flight — handler must close
		},
	}
	qdrant := &mockQdrantDeleter{}
	h := outboxhandlers.NewIndexDeleteHandler(zap.NewNop(), qdrant, assets)

	if err := h.Handle(context.Background(), deleteEvt(t, validIndexDeletePayload(t, "clip-terminal-hop", "idem-terminal-hop"))); err != nil {
		t.Fatalf("terminal-hop happy path should succeed; got: %v", err)
	}
	if got := len(assets.lifecycleStateCalls); got != 1 {
		t.Fatalf("expected exactly 1 lifecycle_state call (terminal hop); got %d", got)
	}
	if got := assets.lifecycleStateCalls[0].State; got != asset.StateDeleted {
		t.Errorf("terminal hop must write DELETED; got %q", got)
	}
}

// TestIndexDeleteHandler_LifecycleStateSetErrorIsRetryable: when
// SetLifecycleState returns a transient SQLite error (e.g. database
// is locked), the handler MUST classify the error as retryable
// (NOT terminal) so the outbox pool retries per its backoff. The
// retry is safe because the Qdrant delete + SoftDelete + index_state
// flips are all idempotent at the API/repo layer; the
// lifecycle_state write will re-attempt and succeed on the next
// pool attempt.
func TestIndexDeleteHandler_LifecycleStateSetErrorIsRetryable(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult:            &asset.Asset{ID: "clip-setlsc-fail", LifecycleState: asset.StateActive},
		setLifecycleStateErr: errors.New("sqlite: database is locked"),
	}
	qdrant := &mockQdrantDeleter{}
	h := outboxhandlers.NewIndexDeleteHandler(zap.NewNop(), qdrant, assets)

	err := h.Handle(context.Background(), deleteEvt(t, validIndexDeletePayload(t, "clip-setlsc-fail", "idem-setlsc-fail")))
	if err == nil {
		t.Fatal("expected error from SetLifecycleState failure; got nil")
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("SetLifecycleState transient error must be RETRYABLE; got terminal: %v", err)
	}
	if !strings.Contains(err.Error(), "SetLifecycleState") {
		t.Errorf("retryable error lost the diagnostic marker: %q", err.Error())
	}
	// Qdrant + SoftDelete + index_state=DELETED must have completed
	// (these are the prerequisites for the failing SetLifecycleState
	// call). A future regression that reorders and skips the
	// earlier writes when SetLifecycleState fails must break here.
	if qdrant.callCount() != 1 {
		t.Errorf("Qdrant.DeletePoints should have completed before SetLifecycleState(DELETED); got %d calls", qdrant.callCount())
	}
	if assets.softDeleteCount() != 1 {
		t.Errorf("SoftDelete should have completed before SetLifecycleState(DELETED); got %d calls", assets.softDeleteCount())
	}
}

// TestIndexDeleteHandler_QdrantErrorPropagatesAsRetryable: Qdrant
// returns a transient error → handler returns plain (non-terminal)
// error so the pool's exponential backoff retries. Critically, no
// SoftDelete happens — Qdrant-side retry is cheap, SQLite-side
// happened-before would be wrong.
func TestIndexDeleteHandler_QdrantErrorPropagatesAsRetryable(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{ID: "clip-x", LifecycleState: asset.StateActive},
	}
	qdrant := &mockQdrantDeleter{err: errors.New("qdrant 503")}
	h := outboxhandlers.NewIndexDeleteHandler(zap.NewNop(), qdrant, assets)

	err := h.Handle(context.Background(), deleteEvt(t, validIndexDeletePayload(t, "clip-x", "idem-x")))
	if err == nil {
		t.Fatal("expected error from Qdrant failure; got nil")
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("Qdrant transient error must be RETRYABLE (so the pool can backoff); got terminal: %v", err)
	}
	if assets.softDeleteCount() != 0 {
		t.Errorf("SoftDelete must NOT run when Qdrant delete fails (got %d)", assets.softDeleteCount())
	}
}

// TestIndexDeleteHandler_SoftDeleteErrorPropagatesAsRetryable: covers
// the post-Qdrant-success path where the local SQLite write fails.
// The retry is safe because Qdrant delete is idempotent and the
// SoftDelete is also idempotent (already-deleted short-circuits next
// time).
func TestIndexDeleteHandler_SoftDeleteErrorPropagatesAsRetryable(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{ID: "clip-y", LifecycleState: asset.StateActive},
		softErr:   errors.New("sqlite: database is locked"),
	}
	qdrant := &mockQdrantDeleter{}
	h := outboxhandlers.NewIndexDeleteHandler(zap.NewNop(), qdrant, assets)

	err := h.Handle(context.Background(), deleteEvt(t, validIndexDeletePayload(t, "clip-y", "idem-y")))
	if err == nil {
		t.Fatal("expected error from SoftDelete failure; got nil")
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("SoftDelete transient error must be RETRYABLE; got terminal: %v", err)
	}
	if qdrant.callCount() != 1 {
		t.Errorf("Qdrant.DeletePoints should have completed before SoftDelete; got %d calls", qdrant.callCount())
	}
}

// TestIndexDeleteHandler_PreservesUnwrapChain: identical pattern to
// IndexingHandler's PreservesWrappedCause. errors.As must reach the
// underlying *json.SyntaxError so operator log greps stay viable
// after the NewTerminalError wrap. Using the actual underlying type
// (json.SyntaxError) is required — a custom sentinel would NOT match
// errors.As's type-assignability check since the chain's concrete
// type is *json.SyntaxError, not any local probe type.
func TestIndexDeleteHandler_PreservesUnwrapChain(t *testing.T) {
	h := outboxhandlers.NewIndexDeleteHandler(
		zap.NewNop(),
		&mockQdrantDeleter{},
		&mockAssetDeleter{},
	)
	err := h.Handle(context.Background(), deleteEvt(t, `{ absolutely not valid json`))
	if err == nil {
		t.Fatal("expected error")
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("errors.As must reach underlying json.SyntaxError; Unwrap chain is broken")
	}
}
