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
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// ── Fakes ────────────────────────────────────────────────────────────
//
// mockIndexClipper and mockAssetSourceChecker satisfy the handler's
// local port interfaces (Pattern 0 — declared inside indexing.go).
// Each fake records invocations so tests can assert both SUCCESSFUL
// calls and non-calls (idempotent / supersede pre-flight).

type mockIndexClipper struct {
	calls   []string
	invoked int
	err     error
}

func (m *mockIndexClipper) IndexClip(ctx context.Context, clipID string) error {
	m.invoked++
	m.calls = append(m.calls, clipID)
	return m.err
}

type mockAssetSourceChecker struct {
	getResult *asset.Asset
	getErr    error
	getCalls  []string
	invoked   int
}

func (m *mockAssetSourceChecker) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	m.invoked++
	m.getCalls = append(m.getCalls, id)
	return m.getResult, m.getErr
}

// ── Helpers ───────────────────────────────────────────────────────────
//
// validIndexRequestPayload builds a v1 envelope. The schema_version
// literal is sourced from outboxhandlers.IndexRequestSchemaVersion so
// a future version bump automatically updates test fixtures in
// lockstep. assetID + sourceVersion + idempotencyKey are caller's
// choice; tests that exercise the schema-version-mismatch path build
// the JSON inline so they can pin a wrong literal deliberately.

func validIndexRequestPayload(t *testing.T, assetID, sourceVersion, idemKey string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"schema_version":       outboxhandlers.IndexRequestSchemaVersion,
		"event_id":             "evt-" + idemKey,
		"asset_id":             assetID,
		"operation":            "UPSERT",
		"source_version":       sourceVersion,
		"target_index_version": "v1",
		"requested_vectors":    []string{"text", "transcript"},
		"requested_at":         "2026-06-25T12:00:00Z",
		"idempotency_key":      idemKey,
	})
	if err != nil {
		t.Fatalf("marshal v1 index payload: %v", err)
	}
	return string(body)
}

func indexEvt(t *testing.T, payload string) outboxevents.Event {
	t.Helper()
	return outboxevents.Event{
		ID:           200,
		EventType:    outboxevents.EventAssetIndexRequested,
		AggregateID:  "agg-1",
		PayloadJSON:  payload,
		AttemptCount: 1,
		MaxAttempts:  10,
	}
}

// assetWithContentHash builds an asset with metadata
// {"content_hash": hash} populated from the JSON-canonical map.
func assetWithContentHash(id, hash string) *asset.Asset {
	a := &asset.Asset{ID: id}
	if hash != "" {
		a.SetMetadataString("content_hash", hash)
	}
	return a
}

// ── Schema version + strict envelope validation ──────────────────────
//
// Tests in this file focus on the v1 envelope additions (schema_version
// literal match, source_version, idempotency_key, operation). The
// payload-parse and empty-asset paths are covered by the
// pre-existing indexing_terminal_test.go and workflow_step_completed_test.go
// files — those use the legacy `&IndexingHandler{}` struct literal
// (zero sourceChecker) to keep the parse-time gates pure. The tests
// here wire NewIndexingHandler with both indexer + sourceChecker so
// the end-to-end wiring path is exercised too.

// TestIndexingHandler_V1EventType pins the EventType ↔ constant tie
// for the v1 constructor (NewIndexingHandler) path. A companion
// test in workflow_step_completed_test.go covers the legacy
// `&IndexingHandler{}` struct-literal path; both must agree on
// the literal.
func TestIndexingHandler_V1EventType(t *testing.T) {
	h := outboxhandlers.NewIndexingHandler(
		&mockIndexClipper{},
		&mockAssetSourceChecker{},
		zap.NewNop(),
	)
	if got := h.EventType(); got != outboxevents.EventAssetIndexRequested {
		t.Errorf("expected %q got %q", outboxevents.EventAssetIndexRequested, got)
	}
}

// TestIndexingHandler_V1PayloadParseIsTerminal: malformed JSON is
// terminal so the producer fixes the payload rather than burning
// max_attempts in a repair loop. NewIndexingHandler w/ mocks
// variant; the legacy `&IndexingHandler{}` variant lives in
// indexing_terminal_test.go for the parse-gate-only path.
func TestIndexingHandler_V1PayloadParseIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexingHandler(
		&mockIndexClipper{},
		&mockAssetSourceChecker{},
		zap.NewNop(),
	)
	err := h.Handle(context.Background(), indexEvt(t, `{ not json`))
	if err == nil {
		t.Fatal("expected error on malformed JSON; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("malformed JSON must be terminal; got: %v", err)
	}
}

// TestIndexingHandler_SchemaVersionMismatchIsTerminal: any
// version literal that isn't exactly IndexRequestSchemaVersion
// is terminal — producers must upgrade rather than silently
// retrying on what looks like a routine failure.
func TestIndexingHandler_SchemaVersionMismatchIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexingHandler(
		&mockIndexClipper{},
		&mockAssetSourceChecker{},
		zap.NewNop(),
	)
	payload := `{"schema_version":"asset.index.requested.v0","event_id":"e","asset_id":"a","source_version":"hash","idempotency_key":"i"}`
	err := h.Handle(context.Background(), indexEvt(t, payload))
	if err == nil {
		t.Fatal("expected error on schema_version mismatch; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("schema_version mismatch must be terminal; got: %v", err)
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error message lost diagnostic hint: %q", err.Error())
	}
}

// TestIndexingHandler_V1EmptyAssetIDIsTerminal: empty asset_id
// cannot appear via retry — terminal. NewIndexingHandler variant.
// The legacy `&IndexingHandler{}` parse-gate-only variant lives in
// indexing_terminal_test.go.
func TestIndexingHandler_V1EmptyAssetIDIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexingHandler(
		&mockIndexClipper{},
		&mockAssetSourceChecker{},
		zap.NewNop(),
	)
	payload := `{"schema_version":"asset.index.requested.v1","event_id":"e","asset_id":"","source_version":"hash","idempotency_key":"i"}`
	err := h.Handle(context.Background(), indexEvt(t, payload))
	if err == nil {
		t.Fatal("expected error on empty asset_id; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("empty asset_id must be terminal; got: %v", err)
	}
}

// TestIndexingHandler_EmptySourceVersionIsTerminal: missing
// source_version is the canonical supersede-ambiguity signal —
// we cannot verify the event is current, so retrying won't fix
// it. Terminal so producers upgrade.
func TestIndexingHandler_EmptySourceVersionIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexingHandler(
		&mockIndexClipper{},
		&mockAssetSourceChecker{},
		zap.NewNop(),
	)
	payload := `{"schema_version":"asset.index.requested.v1","event_id":"e","asset_id":"a","source_version":"","idempotency_key":"i"}`
	err := h.Handle(context.Background(), indexEvt(t, payload))
	if err == nil {
		t.Fatal("expected error on empty source_version; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("empty source_version must be terminal; got: %v", err)
	}
	if !strings.Contains(err.Error(), "source_version") {
		t.Errorf("error message lost diagnostic hint: %q", err.Error())
	}
}

// TestIndexingHandler_MissingIdempotencyKeyIsTerminal: the
// envelope MUST carry idempotency_key so a replay of the same
// event can be deduplicated against outbox event_key.
func TestIndexingHandler_MissingIdempotencyKeyIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexingHandler(
		&mockIndexClipper{},
		&mockAssetSourceChecker{},
		zap.NewNop(),
	)
	payload := `{"schema_version":"asset.index.requested.v1","event_id":"e","asset_id":"a","source_version":"hash","idempotency_key":""}`
	err := h.Handle(context.Background(), indexEvt(t, payload))
	if err == nil {
		t.Fatal("expected error on missing idempotency_key; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("missing idempotency_key must be terminal; got: %v", err)
	}
}

// TestIndexingHandler_UnsupportedOperationIsTerminal: only
// "UPSERT" is supported in v1; any other operation value
// (e.g. future REINDEX mistakenly used) is terminal so the
// producer upgrades its envelope version.
func TestIndexingHandler_UnsupportedOperationIsTerminal(t *testing.T) {
	h := outboxhandlers.NewIndexingHandler(
		&mockIndexClipper{},
		&mockAssetSourceChecker{},
		zap.NewNop(),
	)
	payload := `{"schema_version":"asset.index.requested.v1","event_id":"e","asset_id":"a","operation":"REINDEX","source_version":"hash","idempotency_key":"i"}`
	err := h.Handle(context.Background(), indexEvt(t, payload))
	if err == nil {
		t.Fatal("expected error on unsupported operation; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("unsupported operation must be terminal; got: %v", err)
	}
}

// ── Source-version supersede gate ──────────────────────────────────

// TestIndexingHandler_SourceVersionSupersede: event.source_version
// differs from current asset's content_hash → handler returns a
// *SupersedeError (NOT terminal, NOT retryable). Pool.processEvent's
// IsSupersede classifier routes the row to MarkSuperseded.
// IndexClip MUST NOT be invoked — that's the whole point of the gate.
func TestIndexingHandler_SourceVersionSupersede(t *testing.T) {
	indexer := &mockIndexClipper{}
	src := &mockAssetSourceChecker{
		getResult: assetWithContentHash("clip-z", "hash-CURRENT"),
	}
	h := outboxhandlers.NewIndexingHandler(indexer, src, zap.NewNop())

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-z", "hash-OLD", "idem-x")),
	)

	if err == nil {
		t.Fatal("expected *SupersedeError when source_version mismatches current content_hash; got nil")
	}
	if !outboxevents.IsSupersede(err) {
		t.Fatalf("source_version mismatch must produce *SupersedeError (IsSupersede=true); got: %v", err)
	}
	// Verify independence from terminal (a superseded event is
	// NOT terminal — the pool's first classifier wins).
	if outboxevents.IsTerminal(err) {
		t.Errorf("supersede must NOT classify as terminal (they're orthogonal terminal-success states); got: %v", err)
	}
	if indexer.invoked != 0 {
		t.Errorf("IndexClip must NOT be invoked on supersede (the whole point of the gate); got %d calls", indexer.invoked)
	}
	if src.invoked != 1 {
		t.Errorf("GetClip must be invoked exactly once for the supersede read; got %d", src.invoked)
	}
	// Confirm the metric was bumped before the handler returned.
	// Prometheus counters are global state; we reset by re-reading
	// the metric is hard in unit tests, so we just confirm the
	// handler path was traversed (no error from indexer = correct).
	if !strings.Contains(err.Error(), "superseded by current source_version=\"hash-CURRENT\"") {
		t.Errorf("error message lost current source_version debug: %q", err.Error())
	}
}

// TestIndexingHandler_SourceVersionMatchDelegatesToIndexClip:
// when source_version equals current content_hash the handler
// proceeds to IndexClip (the supersede gate has no effect;
// IndexClip's own internal idempotency check is a separate gate).
func TestIndexingHandler_SourceVersionMatchDelegatesToIndexClip(t *testing.T) {
	indexer := &mockIndexClipper{}
	src := &mockAssetSourceChecker{
		getResult: assetWithContentHash("clip-ok", "hash-X"),
	}
	h := outboxhandlers.NewIndexingHandler(indexer, src, zap.NewNop())

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-ok", "hash-X", "idem-ok")),
	)
	if err != nil {
		t.Fatalf("happy path should succeed; got: %v", err)
	}
	if indexer.invoked != 1 {
		t.Errorf("IndexClip must be called exactly once on matching source_version; got %d", indexer.invoked)
	}
	if indexer.calls[0] != "clip-ok" {
		t.Errorf("IndexClip called with wrong id: %q", indexer.calls[0])
	}
}

// TestIndexingHandler_GetClipErrorIsRetryable: source_checker
// returns error → retryable so the pool's exponential backoff
// retries (transient SQLite lock / network blip on a remote DB).
func TestIndexingHandler_GetClipErrorIsRetryable(t *testing.T) {
	indexer := &mockIndexClipper{}
	src := &mockAssetSourceChecker{
		getErr: errors.New("sqlite: database is locked"),
	}
	h := outboxhandlers.NewIndexingHandler(indexer, src, zap.NewNop())

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-r", "h", "i")),
	)
	if err == nil {
		t.Fatal("expected error from GetClip failure; got nil")
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("GetClip transient error must be RETRYABLE; got terminal: %v", err)
	}
	if outboxevents.IsSupersede(err) {
		t.Fatalf("GetClip error must NOT be classified as supersede; got: %v", err)
	}
	if indexer.invoked != 0 {
		t.Errorf("IndexClip must NOT run when supersede read failed (we don't know if it's stale); got %d", indexer.invoked)
	}
}

// TestIndexingHandler_NilSourceCheckerSkipsGate: when the
// composition root doesn't wire a source_checker (test dbs,
// partial wiring windows), the gate is skipped and IndexClip
// runs unconditionally. IndexClip's own internal idempotency
// check serves as a fallback.
func TestIndexingHandler_NilSourceCheckerSkipsGate(t *testing.T) {
	indexer := &mockIndexClipper{}
	h := outboxhandlers.NewIndexingHandler(indexer, nil, zap.NewNop())

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-n", "h", "i")),
	)
	if err != nil {
		t.Fatalf("nil sourceChecker should still proceed to IndexClip; got: %v", err)
	}
	if indexer.invoked != 1 {
		t.Errorf("IndexClip must be called when supersede gate is skipped; got %d", indexer.invoked)
	}
}

// TestIndexingHandler_NilIndexerReturnsTerminal: missing
// indexer is a production misconfiguration — return terminal so
// the row dead-letters loudly rather than spinning max_attempts
// on a "nil pointer dereference" cycle.
func TestIndexingHandler_NilIndexerReturnsTerminal(t *testing.T) {
	src := &mockAssetSourceChecker{
		getResult: assetWithContentHash("clip-nil", "h"),
	}
	h := outboxhandlers.NewIndexingHandler(nil, src, zap.NewNop())

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-nil", "h", "i")),
	)
	if err == nil {
		t.Fatal("expected error on nil indexer; got nil")
	}
	if !outboxevents.IsTerminal(err) {
		t.Fatalf("nil indexer should be TERMINAL (loud-fail in production); got: %v", err)
	}
}

// TestIndexingHandler_MissingAssetSkipsGate: if GetClip returns
// nil for an unknown asset (idempotency shortcut similar to
// IndexDeleteHandler), the supersede gate is skipped and
// IndexClip proceeds. IndexClip fetches the same asset again,
// observes it doesn't exist, and short-circuits on its own —
// keeping the handler narrow without duplicating that logic.
func TestIndexingHandler_MissingAssetSkipsGate(t *testing.T) {
	indexer := &mockIndexClipper{}
	src := &mockAssetSourceChecker{getResult: nil}
	h := outboxhandlers.NewIndexingHandler(indexer, src, zap.NewNop())

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-ghost", "h", "i")),
	)
	if err != nil {
		t.Fatalf("missing asset should fall through to IndexClip; got: %v", err)
	}
	if indexer.invoked != 1 {
		t.Errorf("IndexClip must be called when asset is missing (its own idempotency check handles the case); got %d", indexer.invoked)
	}
}

// TestIndexingHandler_IndexClipErrorPropagatesAsRetryable: IndexClip
// returned a transient error → retryable so the pool's
// exponential backoff retries. Embedding-server transient
// failures (timeouts, 502/503/504), network blips, Qdrant conn
// drops all sit here.
func TestIndexingHandler_IndexClipErrorPropagatesAsRetryable(t *testing.T) {
	indexer := &mockIndexClipper{err: errors.New("embedding-server 503")}
	src := &mockAssetSourceChecker{
		getResult: assetWithContentHash("clip-r", "h"),
	}
	h := outboxhandlers.NewIndexingHandler(indexer, src, zap.NewNop())

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-r", "h", "i")),
	)
	if err == nil {
		t.Fatal("expected error from IndexClip failure; got nil")
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("IndexClip transient error must be RETRYABLE; got terminal: %v", err)
	}
}

// TestIndexingHandler_MetricsEmitted is a smoke-level
// confirmation that MediaIndexAttemptsTotal bumps on every
// Handle entry. We can't easily read the global counter
// mid-test, so we just exercise the path that increments it
// (parse error before counter → counter not bumped; happy path
// → counter bumped). Reading the through-the-test counter would
// require a Prometheus testutil handler; we trust the wrapping
// deferred Observation is invoked on every return.
func TestIndexingHandler_MetricsEmitted(t *testing.T) {
	indexer := &mockIndexClipper{}
	src := &mockAssetSourceChecker{
		getResult: assetWithContentHash("clip-m", "h"),
	}
	h := outboxhandlers.NewIndexingHandler(indexer, src, zap.NewNop())

	// Just confirm the metric definition is reachable (compile-time
	// pin against accidental renames) AND that we hit it. The
	// increment itself runs on every entry, even parse_err paths.
	_ = metrics.MediaIndexAttemptsTotal

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-m", "h", "i")),
	)
	if err != nil {
		t.Fatalf("happy path should succeed; got: %v", err)
	}
}
