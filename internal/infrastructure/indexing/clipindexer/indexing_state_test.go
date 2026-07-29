package clipindexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

// mediaAssetsStateMachineSchema is intentionally MINIMAL — only the
// columns touched by setIndexState / setIndexedAt per PR6. Future
// drift that adds a sidecar column the writers don't read (yet
// could be read by a future code path) is impossible to mask; the
// schema mirrors precisely the production writers' SQL projection.
const mediaAssetsStateMachineSchema = `
	CREATE TABLE IF NOT EXISTS media_assets (
		id TEXT PRIMARY KEY,
		metadata_json TEXT NOT NULL DEFAULT '{}',
		index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
		index_state_updated_at TEXT NOT NULL DEFAULT '',
		source_version TEXT NOT NULL DEFAULT '',
		file_hash TEXT NOT NULL DEFAULT ''
	);
`

// newTestServiceForStateMachine constructs a fully-wired Service
// the same way production callers do (NewService with a
// *storage.SQLiteDB typed handle — see service.go PG-016 comment).
//
// Why we mirror production NewService even though the tests only
// call setIndexState / setIndexedAt (which only touch s.db + s.log):
//
//	Service.db is *storage.SQLiteDB (PG-016 typed-handle migration);
//	the compiler rejects raw *sql.DB literals in struct context.
//	Bypassing this with a private wrapper that wraps a test *sql.DB
//	re-introduces the PG-016 surface area the typed handle was
//	introduced to remove. Future Service field additions (e.g., a
//	config validator that panics on missing values) would crash
//	test setup for irrelevant reasons, so we accept the extra
//	setup cost and document the coupling here.
func newTestServiceForStateMachine(t *testing.T, log *zap.Logger) *Service {
	t.Helper()
	if log == nil {
		log = zap.NewNop()
	}
	cfg := DefaultConfig()
	cfg.ScriptPath = "" // never invoke; belt-and-suspenders against a stray call

	tmpDir := t.TempDir()
	db, err := storage.NewSQLiteDB(tmpDir, "test.db", log)
	if err != nil {
		t.Fatalf("storage.NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Apply the test schema. storage.NewSQLiteDB opens an empty
	// SQLite — no migrations run automatically, so the caller
	// (this helper) owns schema setup.
	if _, err := db.Exec(mediaAssetsStateMachineSchema); err != nil {
		t.Fatalf("apply test schema: %v", err)
	}

	return NewService(cfg, db, db.Path(), log)
}

// TestSetIndexState_WritesColumn pins that setIndexState writes the
// media_assets.index_state COLUMN on the success path, not a
// metadata_json.$.index_state JSON key. The legacy implementation
// used json_set on metadata_json — PR6 promotes the state to a real
// column where an operator / dashboard query can read it without
// paying json_extract cost.
func TestSetIndexState_WritesColumn(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	mustInsertAsset(t, ctx, svc.db, "clip-1", `{}`)

	if err := svc.setIndexState(ctx, "clip-1", asset.StateIndexing, ""); err != nil {
		t.Fatalf("setIndexState: %v", err)
	}

	got := readStateAndMeta(t, ctx, svc.db, "clip-1")
	if got.indexState != string(asset.StateIndexing) {
		t.Errorf("index_state column: want %q got %q",
			string(asset.StateIndexing), got.indexState)
	}
	if got.indexStateUpdatedAt == "" {
		t.Errorf("index_state_updated_at column: want non-empty; got %q",
			got.indexStateUpdatedAt)
	}
	// PR6 invariant — verify via structured json.Unmarshal parse
	// rather than substring match (a substring check would false-
	// positive on compound keys like `subfolder_index_states`).
	meta := parseJSONMeta(got.metadataJSON)
	if hasMetaKey(meta, "index_state") {
		t.Errorf("metadata_json.$.index_state must NOT be set by PR6 writers; got %v", meta)
	}
}

// TestSetIndexState_WritesLastErrorSidecar pins that the
// last_index_error sidecar (in metadata_json) survives the
// column-promotion — operators grep on
// `metadata_json LIKE '%last_index_error%'` today and changing the
// audit-trail sidecar lives outside PR6's scope.
func TestSetIndexState_WritesLastErrorSidecar(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	mustInsertAsset(t, ctx, svc.db, "clip-2", `{}`)

	if err := svc.setIndexState(ctx, "clip-2", asset.StateIndexFailed, "boom"); err != nil {
		t.Fatalf("setIndexState: %v", err)
	}

	got := readStateAndMeta(t, ctx, svc.db, "clip-2")
	if got.indexState != string(asset.StateIndexFailed) {
		t.Errorf("index_state column: want %q got %q",
			string(asset.StateIndexFailed), got.indexState)
	}
	meta := parseJSONMeta(got.metadataJSON)
	if !hasMetaKey(meta, "last_index_error") {
		t.Errorf("metadata_json must carry $.last_index_error sidecar (operator audit); got %v", meta)
	}
	if metaString(meta, "last_index_error") != "boom" {
		t.Errorf("$.last_index_error must equal %q; got %q",
			"boom", metaString(meta, "last_index_error"))
	}
}

// TestSetIndexState_RefusesStateIndexedPanics pins the runtime guard
// preventing accidental INDEXED writes via setIndexState. The
// success path goes through setIndexedAt so the column flip +
// $.indexed_at + $.indexed_content_hash + $.embedding_model +
// $.embedding_model_version sit in ONE atomic UPDATE (the thinker's
// question-G answer). A panic here surfaces the mistake loudly
// instead of double-counting MediaIndexSuccessTotal.
func TestSetIndexState_RefusesStateIndexedPanics(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	mustInsertAsset(t, ctx, svc.db, "clip-3", `{}`)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("setIndexState(STATE_INDEXED) must panic — use setIndexedAt for the success path")
		}
		msg, _ := r.(string)
		if !strContains(msg, "setIndexedAt") {
			t.Errorf("panic message must reference setIndexedAt; got %q", msg)
		}
	}()

	svc.setIndexState(ctx, "clip-3", asset.StateIndexed, "") // panic expected, no error return
}

// TestSetIndexedAt_WritesColumnPlusSidecarsAtomically pins that
// setIndexedAt's UPDATE flushes the column flip and the four JSON
// sidecars in ONE statement. The pre-PR6 implementation had two
// separate UPDATEs (setIndexState then setIndexedAt) which left a
// "Settled column, empty sidecars" window the fast-path could read.
// After PR6 the column flip and the sidecars are inseparable.
func TestSetIndexedAt_WritesColumnPlusSidecarsAtomically(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	// Direct INSERT with source_version AND index_state='INDEXING'
	// so the CAS fence matches: source_version=? AND index_state='INDEXING'.
	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO media_assets (id, metadata_json, index_state, source_version, file_hash) VALUES (?, '{}', 'INDEXING', ?, ?)`,
		"clip-4", "sv-v1", "hash-CURRENT",
	)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	if err := svc.setIndexedAt(ctx, "clip-4", "hash-CURRENT", "sv-v1"); err != nil {
		t.Fatalf("setIndexedAt: %v", err)
	}

	got := readStateAndMeta(t, ctx, svc.db, "clip-4")
	if got.indexState != string(asset.StateIndexed) {
		t.Errorf("index_state column: want %q got %q",
			string(asset.StateIndexed), got.indexState)
	}
	if got.indexStateUpdatedAt == "" {
		t.Errorf("index_state_updated_at column: want non-empty; got %q",
			got.indexStateUpdatedAt)
	}
	// Sidecar writes are all-or-nothing because they sit in the
	// same UPDATE statement. If any is missing, the writer is BROKEN
	// (an intermediate state was observable mid-write).
	meta := parseJSONMeta(got.metadataJSON)
	required := []string{"indexed_at", "indexed_content_hash", "embedding_model", "embedding_model_version"}
	for _, k := range required {
		if !hasMetaKey(meta, k) {
			t.Errorf("metadata_json must carry $.%s sidecar; got %v", k, meta)
		}
	}
	if metaString(meta, "indexed_content_hash") != "hash-CURRENT" {
		t.Errorf("$.indexed_content_hash must equal %q; got %q",
			"hash-CURRENT", metaString(meta, "indexed_content_hash"))
	}
}

// TestSetIndexState_RefusesUnknownStateLogsWarning pins the guard
// against enum drift. If a future agent introduces an IndexState
// variant without registering it in the Valid() switch, setIndexState
// must not silently write a garbage value — log a warning and refuse.
func TestSetIndexState_RefusesUnknownStateLogsWarning(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	mustInsertAsset(t, ctx, svc.db, "clip-5", `{}`)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("setIndexState(unknown) must NOT panic; got %v", r)
		}
	}()

	err := svc.setIndexState(ctx, "clip-5", asset.IndexState("BANANA_STATE"), "")
	if err == nil {
		t.Error("setIndexState with unknown state should return an error")
	}

	got := readStateAndMeta(t, ctx, svc.db, "clip-5")
	if got.indexState != string(asset.StateDiscovered) {
		t.Errorf("index_state column must remain at DEFAULT after rejected write; want %q got %q",
			string(asset.StateDiscovered), got.indexState)
	}
}

// TestSetIndexState_RefusesEmptyState pins the PR6 invariant that
// empty IndexState is rejected explicitly rather than silently
// written. A worker that misconfigures the state enum and
// accidentally passes `IndexState("")` would otherwise flip the
// column to empty and the row would re-read as the column DEFAULT
// (DISCOVERED) — losing any INDEXED / INDEX_FAILED / DELETED pre-
// state. The guard turns the silent garbage write into a loud
// operator-log warning.
func TestSetIndexState_RefusesEmptyState(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	mustInsertAsset(t, ctx, svc.db, "clip-empty", `{}`)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("setIndexState(empty) must NOT panic; got %v", r)
		}
	}()

	err := svc.setIndexState(ctx, "clip-empty", asset.IndexState(""), "")
	if err == nil {
		t.Error("setIndexState with empty state should return an error")
	}

	got := readStateAndMeta(t, ctx, svc.db, "clip-empty")
	if got.indexState != string(asset.StateDiscovered) {
		t.Errorf("index_state column must remain at DEFAULT after rejected empty-state write; want %q got %q",
			string(asset.StateDiscovered), got.indexState)
	}
	if got.indexStateUpdatedAt != "" {
		t.Errorf("index_state_updated_at column must remain empty after rejected write; got %q",
			got.indexStateUpdatedAt)
	}
}

// TestSetIndexState_LastErrorSidecarClearsOnEmpty pins the PR6
// invariant that the last_index_error sidecar is idempotent across
// state transitions. A non-empty error writes $.last_index_error;
// the NEXT state change with empty error must clear it via
// json_remove — otherwise operators grep on
// `metadata_json LIKE '%last_index_error%'` would see STALE errors
// that don't match the current column state. The fix: both
// branches in setIndexState touch metadata_json (json_set on
// non-empty, json_remove on empty) so the sidecar is always in
// lockstep with the column flip.
func TestSetIndexState_LastErrorSidecarClearsOnEmpty(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	mustInsertAsset(t, ctx, svc.db, "clip-clear", `{}`)

	// First: transition to INDEX_FAILED with non-empty lastError.
	if err := svc.setIndexState(ctx, "clip-clear", asset.StateIndexFailed, "boom"); err != nil {
		t.Fatalf("setIndexState INDEX_FAILED: %v", err)
	}
	got := readStateAndMeta(t, ctx, svc.db, "clip-clear")
	meta := parseJSONMeta(got.metadataJSON)
	if !hasMetaKey(meta, "last_index_error") {
		t.Fatalf("setup: must have last_index_error sidecar; got %v", meta)
	}
	if metaString(meta, "last_index_error") != "boom" {
		t.Fatalf("setup: last_index_error must equal %q; got %q",
			"boom", metaString(meta, "last_index_error"))
	}

	// Then: transition to INDEXING with empty lastError — the
	// sidecar MUST be cleared.
	if err := svc.setIndexState(ctx, "clip-clear", asset.StateIndexing, ""); err != nil {
		t.Fatalf("setIndexState INDEXING: %v", err)
	}
	got = readStateAndMeta(t, ctx, svc.db, "clip-clear")
	if got.indexState != string(asset.StateIndexing) {
		t.Errorf("index_state column: want %q got %q",
			string(asset.StateIndexing), got.indexState)
	}
	meta = parseJSONMeta(got.metadataJSON)
	if hasMetaKey(meta, "last_index_error") {
		t.Errorf("$.last_index_error must be cleared after empty-error transition; got %v", meta)
	}
}

// TestSetIndexState_MultiStateTransitions pins that the
// (transient → failure → pending → transient → terminal) sequence
// writes happen in order with no missed states.
//
// Task 2 (July 2026): updated to exercise the new granular states:
// DISCOVERED → EMBEDDING → EMBEDDING_FAILED → INDEX_PENDING
// → EMBEDDING → EMBEDDED → INDEXING → INDEXED.
func TestSetIndexState_MultiStateTransitions(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	// Direct INSERT with source_version to satisfy the CAS fence.
	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO media_assets (id, metadata_json, index_state, source_version, file_hash) VALUES (?, '{}', 'DISCOVERED', ?, ?)`,
		"clip-6", "sv-MULTI", "hash-FINAL",
	)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	// Step 1: DISCOVERED → EMBEDDING (embedding generation starts)
	if err := svc.setIndexState(ctx, "clip-6", asset.StateEmbedding, ""); err != nil {
		t.Fatalf("setIndexState EMBEDDING: %v", err)
	}
	// Step 2: EMBEDDING → EMBEDDING_FAILED (embedding generation failed)
	if err := svc.setIndexState(ctx, "clip-6", asset.StateEmbeddingFailed, "transient-err"); err != nil {
		t.Fatalf("setIndexState EMBEDDING_FAILED: %v", err)
	}
	// Step 3: EMBEDDING_FAILED → INDEX_PENDING (waiting for retry)
	if err := svc.setIndexState(ctx, "clip-6", asset.StateIndexPending, ""); err != nil {
		t.Fatalf("setIndexState INDEX_PENDING: %v", err)
	}
	// Step 4: INDEX_PENDING → EMBEDDING (retry: embedding generation restarts)
	if err := svc.setIndexState(ctx, "clip-6", asset.StateEmbedding, ""); err != nil {
		t.Fatalf("setIndexState EMBEDDING (retry): %v", err)
	}
	// Step 5: EMBEDDING → EMBEDDED (embeddings saved to SQLite)
	if err := svc.setIndexState(ctx, "clip-6", asset.StateEmbedded, ""); err != nil {
		t.Fatalf("setIndexState EMBEDDED: %v", err)
	}
	// Step 6: EMBEDDED → INDEXING (Qdrant upsert begins)
	if err := svc.setIndexState(ctx, "clip-6", asset.StateIndexing, ""); err != nil {
		t.Fatalf("setIndexState INDEXING (pre-setIndexedAt): %v", err)
	}
	// Step 7: INDEXING → INDEXED (CAS fence: source_version matches, index_state='INDEXING' matches)
	if err := svc.setIndexedAt(ctx, "clip-6", "hash-FINAL", "sv-MULTI"); err != nil {
		t.Fatalf("setIndexedAt: %v", err)
	}

	got := readStateAndMeta(t, ctx, svc.db, "clip-6")
	if got.indexState != string(asset.StateIndexed) {
		t.Errorf("final index_state: want %q got %q",
			string(asset.StateIndexed), got.indexState)
	}
	if got.indexStateUpdatedAt == "" {
		t.Errorf("final index_state_updated_at: want non-empty")
	}
}

// TestSetIndexedAt_StaleCASFence_ReturnsSupersededWhenVersionMismatch verifies
// that when the source_version in the CAS fence doesn't match the
// row (e.g. an obsolete indexing goroutine tries to write INDEXED
// for a version that has already been superseded), setIndexedAt
// returns *ErrIndexSuperseded so callers can route the event to
// SUPERSEDED instead of SUCCESS.
// The row's index_state must remain unchanged.
func TestSetIndexedAt_StaleCASFence_ReturnsSupersededWhenVersionMismatch(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	// Insert with index_state = 'INDEXING' (the state setIndexedAt expects)
	// and a known source_version.
	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO media_assets (id, metadata_json, index_state, source_version, file_hash) VALUES (?, '{}', 'INDEXING', ?, ?)`,
		"clip-cas-1", "sv-v99", "hash-CURRENT",
	)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	// Pass a DIFFERENT source_version — should be a stale CAS match.
	err = svc.setIndexedAt(ctx, "clip-cas-1", "hash-CURRENT", "sv-v1")
	if err == nil {
		t.Fatalf("setIndexedAt with stale version should return ErrIndexSuperseded")
	}
	var superseded *ErrIndexSuperseded
	if !errors.As(err, &superseded) {
		t.Fatalf("setIndexedAt with stale version should return *ErrIndexSuperseded; got %T: %v", err, err)
	}
	if superseded.ClipID != "clip-cas-1" {
		t.Errorf("ErrIndexSuperseded.ClipID = %q; want %q", superseded.ClipID, "clip-cas-1")
	}

	// Verify the row was NOT updated (stale event skipped).
	got := readStateAndMeta(t, ctx, svc.db, "clip-cas-1")
	if got.indexState != "INDEXING" {
		t.Errorf("index_state must remain INDEXING after stale CAS event; got %q", got.indexState)
	}
}

// TestSetIndexedAt_StaleCASFence_ReturnsSupersededWhenNotIndexing verifies that
// setIndexedAt returns *ErrIndexSuperseded for rows that are not in INDEXING
// state (e.g. already INDEXED by a faster goroutine). The CAS fence's
// index_state = 'INDEXING' clause prevents overwriting a terminal state.
func TestSetIndexedAt_StaleCASFence_ReturnsSupersededWhenNotIndexing(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	// Insert with index_state = 'INDEXED' (already terminal).
	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO media_assets (id, metadata_json, index_state, source_version, file_hash) VALUES (?, '{}', 'INDEXED', ?, ?)`,
		"clip-cas-2", "sv-v1", "hash-CURRENT",
	)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	// Pass matching source_version but the row is already INDEXED.
	err = svc.setIndexedAt(ctx, "clip-cas-2", "hash-CURRENT", "sv-v1")
	if err == nil {
		t.Fatalf("setIndexedAt on already INDEXED row should return ErrIndexSuperseded")
	}
	var superseded *ErrIndexSuperseded
	if !errors.As(err, &superseded) {
		t.Fatalf("setIndexedAt on already INDEXED row should return *ErrIndexSuperseded; got %T: %v", err, err)
	}

	// Verify the row was NOT overwritten.
	got := readStateAndMeta(t, ctx, svc.db, "clip-cas-2")
	if got.indexState != "INDEXED" {
		t.Errorf("index_state must remain INDEXED; got %q", got.indexState)
	}
}

// TestSetIndexedAt_SucceedsWhenContentHashDiffersButSourceVersionMatches verifies
// the audit 2026-07-03 BLOCKER #1 closure: the CAS fence guards on
// (id, source_version, index_state='INDEXING') ONLY — file_hash is no
// longer compared. When the content hash differs, the new hash is still
// written as $.indexed_content_hash (the metadata sidecar records what
// was indexed), but the CAS succeeds because source_version matches.
func TestSetIndexedAt_SucceedsWhenContentHashDiffersButSourceVersionMatches(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO media_assets (id, metadata_json, index_state, source_version, file_hash) VALUES (?, '{}', 'INDEXING', ?, ?)`,
		"clip-cas-3", "sv-v1", "hash-OLD",
	)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	// Pass matching source_version but DIFFERENT content hash.
	// BLOCKER #1 closure: file_hash is no longer in the CAS fence,
	// so this now SUCCEEDS. The new hash is written to $.indexed_content_hash.
	err = svc.setIndexedAt(ctx, "clip-cas-3", "hash-NEW", "sv-v1")
	if err != nil {
		t.Fatalf("BLOCKER #1 closure: setIndexedAt must succeed when source_version matches (file_hash no longer compared): %v", err)
	}

	got := readStateAndMeta(t, ctx, svc.db, "clip-cas-3")
	if got.indexState != string(asset.StateIndexed) {
		t.Errorf("index_state must be INDEXED after successful CAS; got %q", got.indexState)
	}
	meta := parseJSONMeta(got.metadataJSON)
	if metaString(meta, "indexed_content_hash") != "hash-NEW" {
		t.Errorf("$.indexed_content_hash must be the NEW hash (%q); got %q",
			"hash-NEW", metaString(meta, "indexed_content_hash"))
	}
}

// TestSetIndexedAt_CASFence_SucceedsWhenAllMatch verifies that
// when source_version, file_hash, and index_state all match,
// the row is correctly updated to INDEXED.
func TestSetIndexedAt_CASFence_SucceedsWhenAllMatch(t *testing.T) {
	ctx := context.Background()
	svc := newTestServiceForStateMachine(t, nil)

	_, err := svc.db.ExecContext(ctx,
		`INSERT INTO media_assets (id, metadata_json, index_state, source_version, file_hash) VALUES (?, '{}', 'INDEXING', ?, ?)`,
		"clip-cas-4", "sv-v1", "hash-MATCH",
	)
	if err != nil {
		t.Fatalf("insert fixture: %v", err)
	}

	// Both match: source_version, index_state='INDEXING'.
	err = svc.setIndexedAt(ctx, "clip-cas-4", "hash-MATCH", "sv-v1")
	if err != nil {
		t.Fatalf("setIndexedAt with matching CAS: %v", err)
	}

	got := readStateAndMeta(t, ctx, svc.db, "clip-cas-4")
	if got.indexState != string(asset.StateIndexed) {
		t.Errorf("index_state must be INDEXED; got %q", got.indexState)
	}
}

// ── Test helpers ────────────────────────────────────────────────────

type stateAndMeta struct {
	indexState          string
	indexStateUpdatedAt string
	metadataJSON        string
}

func mustInsertAsset(t *testing.T, ctx context.Context, db *storage.SQLiteDB, id, metaJSON string) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO media_assets (id, metadata_json, index_state) VALUES (?, ?, 'DISCOVERED')`,
		id, metaJSON)
	if err != nil {
		t.Fatalf("insert fixture asset %s: %v", id, err)
	}
}

func readStateAndMeta(t *testing.T, ctx context.Context, db *storage.SQLiteDB, id string) stateAndMeta {
	t.Helper()
	var s stateAndMeta
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(index_state, ''), COALESCE(index_state_updated_at, ''), COALESCE(metadata_json, '{}')
		 FROM media_assets WHERE id = ?`, id,
	).Scan(&s.indexState, &s.indexStateUpdatedAt, &s.metadataJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("asset %s missing after writer call", id)
		}
		t.Fatalf("read state for %s: %v", id, err)
	}
	return s
}

// parseJSONMeta unmarshals metadata_json into a map for structured
// assertion. Returns nil if the JSON is empty or unparseable (the
// hasMetaKey helper treats nil-map as "no keys" so this is a
// safe representation rather than a programming error).
func parseJSONMeta(jsonStr string) map[string]any {
	if jsonStr == "" || jsonStr == "{}" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return nil
	}
	return m
}

// hasMetaKey asserts the metadata JSON has the named key (top-level).
// Operates on parsed map[string]any for fixture-route semantic
// matching — substring checks would false-positive on compound
// keys like `subfolder_index_states` or `idx_state_other`.
func hasMetaKey(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	_, ok := m[k]
	return ok
}

// metaString returns the string value of a metadata key, or "" if
// the key is absent or the value is not a string. Single-switch
// type assertion — does not coerce non-string types (the operator
// audit sidecar contract is strictly string).
func metaString(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v, ok := m[k]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// strContains is a tiny local alias so the panic-message assertion
// in TestSetIndexState_RefusesStateIndexedPanics reads cleaner than
// `strings.Contains(msg, ...)` from a single-call site.
func strContains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
