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

type mockAssetDeleter struct {
	getResult     *asset.Asset
	getErr        error
	softDeleteIDs []string
	softErr       error
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
// (and SoftDelete would no-op the same way).
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
}

// TestIndexDeleteHandler_AlreadyDeletedCanonicalSuccess: lifecycle_state
// is already 'DELETED' (canonical post-PR4). Return idempotent
// success; no deleter calls.
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
}

// TestIndexDeleteHandler_AlreadyDeletedLegacySuccess: lifecycle_state
// is lowercase 'deleted' (legacy SoftDelete output). PR2 also treats
// it as a done state — pins the dual-casing invariant.
func TestIndexDeleteHandler_AlreadyDeletedLegacySuccess(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{
			ID:             "clip-legacy-gone",
			LifecycleState: asset.LifecycleState("deleted"),
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
// call happen in this exact order, with the right asset_id.
func TestIndexDeleteHandler_HappyPathTransitionsToDeleted(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{
			ID:             "clip-to-delete",
			LifecycleState: asset.StateReady,
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
}

// TestIndexDeleteHandler_QdrantErrorPropagatesAsRetryable: Qdrant
// returns a transient error → handler returns plain (non-terminal)
// error so the pool's exponential backoff retries. Critically, no
// SoftDelete happens — Qdrant-side retry is cheap, SQLite-side
// happened-before would be wrong.
func TestIndexDeleteHandler_QdrantErrorPropagatesAsRetryable(t *testing.T) {
	assets := &mockAssetDeleter{
		getResult: &asset.Asset{ID: "clip-x", LifecycleState: asset.StateReady},
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
		getResult: &asset.Asset{ID: "clip-y", LifecycleState: asset.StateReady},
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
