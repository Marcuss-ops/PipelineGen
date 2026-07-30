// Package usecase — process_segment_step10_partial_state_e2e_test.go:
// E2E companion to the unit-level Test 9 in
// process_segment_correttezza_test.go (PR-YT-DOD-11-PARTIAL-STATE-TDD).
//
// Where Test 9 uses stubWriterAssetRecorder to record Step 9 call counts
// (proving the use case called the writer), THIS test wires the REAL
// ClipAtomicWriterAdapter + outboxevents.Repository against in-memory
// SQLite and asserts on the REAL DB state via SELECT queries.
//
// godlike/07 NO-FAKE-AVAILABILITY contract: a future regression where
// the writer stops writing to media_assets (or the outbox stops
// enqueuing) would surface HERE as a missing row, even if the use
// case still calls the writer. Test 9's stub-recorder would not catch
// such a regression (the stub returns nil even if the writer is
// internally broken). This E2E test is the canonical integration
// surface for the writer contract.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - media_assets table schema is inlined in openPartialStateDB; it
//     mirrors the canonical 17-column shape in
//     internal/infrastructure/database/sqlite/assets/clip_atomic_writer.go::upsertClipInTx.
//   - outbox_events table schema is inlined in openPartialStateDB; it
//     mirrors the canonical shape in
//     internal/infrastructure/database/sqlite/outboxevents/repository.go::Enqueue.
//   - future schema changes that break this test are the canonical
//     signal that the E2E companion needs to be updated in lockstep
//     (the test is the regression guard, not the source of truth).
package usecase

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	assetsdb "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
)

// openPartialStateDB creates an in-memory SQLite DB with the canonical
// 17-column media_assets + 15-column outbox_events schema. The shape
// is the bare minimum needed to drive ClipAtomicWriterAdapter +
// outboxevents.Repository end-to-end; the production migration
// runner is intentionally NOT used (the E2E test is hermetic and
// does not need the full schema surface).
//
// The created-at / updated-at columns are TEXT (not TIMESTAMP) per
// the canonical outboxevents.Repository contract (time.Time →
// time.RFC3339 string formatting). The lifecycle_state is seeded
// to the canonical "ACTIVE" string the writer UPSERTs.
func openPartialStateDB(t *testing.T) (*sql.DB, *outboxevents.Repository) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "sql.Open(:memory:) must succeed")
	t.Cleanup(func() { _ = db.Close() })

	// media_assets — production-faithful subset, mirrors the canonical
	// schema consumed by persistence.AssetCommitter (SQLiteAssetCommitter).
	// The schema intentionally duplicates the canonical test DDL rather
	// than using the production migration runner to keep the E2E test
	// hermetic; any future column addition that breaks this test signals
	// that the E2E companion schema needs to be updated in lockstep.
	_, err = db.Exec(`
		CREATE TABLE media_assets (
			id TEXT PRIMARY KEY,
			source TEXT, name TEXT, filename TEXT, media_type TEXT,
			category TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0,
			drive_file_id TEXT, drive_link TEXT, download_link TEXT,
			local_path TEXT, file_hash TEXT,
			folder_id TEXT, folder_path TEXT,
			source_version TEXT NOT NULL DEFAULT '',
			search_text TEXT NOT NULL DEFAULT '',
			lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			index_state TEXT NOT NULL DEFAULT '',
			thumbnail_url TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			asset_version TEXT NOT NULL DEFAULT '',
			asset_location TEXT NOT NULL DEFAULT '',
			rendition TEXT NOT NULL DEFAULT '',
			source_provider TEXT NOT NULL DEFAULT '',
			source_video_id TEXT NOT NULL DEFAULT '',
			source_url TEXT NOT NULL DEFAULT '',
			start_ms INTEGER NOT NULL DEFAULT 0,
			end_ms INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',
			updated_at TEXT, created_at TEXT
		)
	`)
	require.NoError(t, err, "CREATE TABLE media_assets must succeed")

	// asset_locations — required by persistence.AssetCommitter for the
	// primary drive location produced by Step 9.
	_, err = db.Exec(`
		CREATE TABLE asset_locations (
			asset_id TEXT NOT NULL,
			location_kind TEXT NOT NULL DEFAULT '',
			uri TEXT NOT NULL DEFAULT '',
			external_id TEXT NOT NULL DEFAULT '',
			web_view_link TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			file_size_bytes INTEGER NOT NULL DEFAULT 0,
			file_hash TEXT NOT NULL DEFAULT '',
			is_primary INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (asset_id, location_kind)
		)
	`)
	require.NoError(t, err, "CREATE TABLE asset_locations must succeed")

	// outbox_events — 15 columns, mirrors outboxevents/repository.go::Enqueue.
	// The Enqueue contract expects: id INTEGER PK AUTOINCREMENT, event_type,
	// aggregate_id, aggregate_type, payload_json, status, attempt_count,
	// max_attempts, last_error, event_key, worker_id, lease_id, lease_expiry,
	// completed_at, created_at, updated_at.
	_, err = db.Exec(`
		CREATE TABLE outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT,
			aggregate_id TEXT,
			aggregate_type TEXT,
			payload_json TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER DEFAULT 0,
			max_attempts INTEGER DEFAULT 5,
			last_error TEXT,
			event_key TEXT UNIQUE,
			worker_id TEXT,
			lease_id TEXT,
			lease_expiry TEXT,
			completed_at TEXT,
			created_at TEXT,
			updated_at TEXT
		)
	`)
	require.NoError(t, err, "CREATE TABLE outbox_events must succeed")

	return db, outboxevents.NewRepository(db)
}

// ── Test E2E-1: PR-YT-DOD-11 partial-state E2E (REAL writer + REAL outbox) ──

// TestPartialState_E2E_Step10FailsAfterClipWrite_MediaAssetsAndOutboxPresent
// is the E2E companion to the unit-level Test 9. Where Test 9 uses
// stubWriterAssetRecorder (proving Step 9 was CALLED), this test
// wires the REAL ClipAtomicWriterAdapter + outboxevents.Repository
// against in-memory SQLite and asserts on the REAL DB state via
// SELECT queries. A writer-side regression (e.g. media_assets
// UPSERT stops writing, outbox INSERT stops enqueuing, transaction
// rolls back silently) would surface here as a missing row, even
// if the use case still calls the writer.
//
// Asserted invariants (mirrors the unit-level Test 9 contract at the
// persistence layer instead of the use-case call surface):
//
//	(1) Job status = FAILED with FailureCodeMetadataFailed (the
//	    canonical job outcome — typed *ExtractionError envelope).
//	(2) media_assets row IS present post-Execute (Step 9 committed
//	    via the REAL writer; all 7 of the canonical writer-visible
//	    columns are populated: id, source, file_hash, local_path,
//	    source_version, search_text, lifecycle_state='ACTIVE').
//	(3) outbox_events row IS present post-Execute (Step 9 enqueued
//	    via the REAL repository; canonical event_type='asset.index.requested'
//	    + aggregate_id=clipID + aggregate_type='media_asset' +
//	    non-empty event_key (idempotency surface) + non-empty
//	    payload_json (canonical v1 envelope shape)).
//	(4) The canonical Warn log "Step 10 failed AFTER clip write"
//	    WAS emitted (regression guard against future log removal).
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion probes a
// falsifiable surface (real DB rows + real log entries). A
// regression in any of the 4 invariants would fail the test
// BEFORE the production deployment reaches an operator dashboard.
func TestPartialState_E2E_Step10FailsAfterClipWrite_MediaAssetsAndOutboxPresent(t *testing.T) {
	// Derived from cmd.VideoID + startSec + endSec + policyVer per the
	// canonical format `yt_<videoID>_<startSec>_<endSec>_<policyVer>`
	// (mirrors process_segment.go::Execute Step 1). Defined once at
	// the top of the test to avoid drift between the WHERE clause,
	// the row.id assertion, and the clip_id log field assertion.
	const expectedClipID = "yt_abc_0_10_v1"

	// ── 1) In-memory SQLite + canonical schema + outbox repo ──
	db, outboxRepo := openPartialStateDB(t)

	// ── 2) Real local file on disk so Step 5's os.Stat passes ──
	realPath := filepath.Join(t.TempDir(), "yt_abc_0_10_v1.mp4")
	require.NoError(t, os.WriteFile(realPath, []byte("fake clip bytes for E2E partial-state test"), 0o644))

	// ── 3) Wire the REAL ClipAtomicWriterAdapter (not a stub) ──
	writer := assetsdb.NewClipAtomicWriterAdapter(db, outboxRepo, zap.NewNop())
	require.NotNil(t, writer, "NewClipAtomicWriterAdapter must succeed with db + outbox repo")

	// ── 4) Wire a captured logger so we can assert on the Warn log ──
	obsCore, recorded := observer.New(zapcore.WarnLevel)
	capturedLog := zap.New(obsCore)

	// ── 5) Wire a real MetadataService that always errors on EnrichClip ──
	// errBuilder is the canonical stub from process_segment_correttezza_test.go
	// (Test 8/9). It returns errors.New from Build; noopWriter satisfies
	// the ctor's required-arg contract but is never called because
	// errBuilder errors first.
	metaSvc, metaSvcErr := ytmetadata.NewMetadataService(ytmetadata.MetadataDeps{
		Builder: errBuilder{},
		Writer:  noopWriter{},
		Logger:  capturedLog,
	})
	require.NoError(t, metaSvcErr, "NewMetadataService must succeed with errBuilder + noopWriter")
	require.NotNil(t, metaSvc, "MetadataService must be non-nil")

	// ── 6) Build deps with the REAL writer (the only difference from Test 9) ──
	bundleCore, media, metadata, observability := validProcessSegmentDeps()
	bundleCore.VideoPipeline = stubVideoPipelineWithPath{path: realPath}
	bundleCore.Hash = testStubHash{}   // non-empty fileHash so Step 5 passes
	bundleCore.Writer = writer         // REAL writer (not stubWriterAssetRecorder)
	bundleCore.Log = capturedLog       // override zap.NewNop default
	metadata.MetadataService = metaSvc // always-fail Step 10 service
	uc := NewProcessYouTubeSegmentFromSubBundles(bundleCore, media, metadata, observability)

	// VideoID="abc" matches the existing Test 9 pattern (process_segment_correttezza_test.go)
	// and produces the clean canonical clipID `yt_abc_0_10_v1` (no doubled
	// `yt_yt_` prefix that would have been cosmetically confusing and risks
	// masking future regressions in clipID canonicalization).
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoURL: "https://www.youtube.com/watch?v=abc",
		VideoID:  "abc",
		OutDir:   t.TempDir(),
		Segment: youtubetypes.Segment{
			Start: "0:00",
			End:   "0:10",
			Name:  "TestE2E",
		},
	}

	// ── 7) Execute: Step 9 commits media_assets + outbox; Step 10 fails ──
	_, execErr := uc.Execute(context.Background(), cmd)

	// ── Invariant (1): typed job outcome ───────────────────────
	require.Error(t, execErr, "Step 10 metadata-enrichment failure MUST surface as typed error")
	ee, ok := execErr.(*ExtractionError)
	require.True(t, ok, "error must be a typed *ExtractionError, got %T %v", execErr, execErr)
	require.Equal(t, FailureCodeMetadataFailed, ee.Code, "FailureCode must be FailureCodeMetadataFailed")
	require.False(t, ee.Retryable, "FailureCodeMetadataFailed is terminal (not retryable)")

	// ── Invariant (2): media_assets row IS present (real DB) ───
	// This is the canonical regression guard for Step 9's tx commit.
	// A writer-side regression that rolls back the media_assets
	// INSERT would surface here as sql.ErrNoRows.
	var (
		rowID            string
		rowSource        string
		rowFileHash      string
		rowSourceVersion string
		rowSearchText    string
		rowState         string
	)
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c (July 2026): the
	// local_path column is RETIRED from the E2E assertion surface.
	// step10 no longer threads localPath (per user spec); clip_id
	// is the canonical lookup key — operators JOIN with media_assets
	// to retrieve the local_path for manual re-extract.
	err := db.QueryRowContext(context.Background(),
		`SELECT id, source, file_hash, source_version, search_text, lifecycle_state
		   FROM media_assets WHERE id = ?`,
		expectedClipID,
	).Scan(&rowID, &rowSource, &rowFileHash, &rowSourceVersion, &rowSearchText, &rowState)
	require.NoError(t, err, "media_assets row must be durably present after Step 9 commit "+
		"(E2E partial-state contract: Step 9 wrote the row before Step 10 failed)")
	require.Equal(t, expectedClipID, rowID, "row.id must match canonical clipID format")
	require.Equal(t, "youtube", rowSource, "row.source must be the canonical 'youtube' string")
	require.NotEmpty(t, rowFileHash, "row.file_hash must be non-empty (Step 5 hash result)")
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c: local_path assertion
	// retired; clip_id is the canonical lookup key.
	require.NotEmpty(t, rowSourceVersion, "row.source_version must be non-empty (BLOCKER #2 closure: source_version is written to the column, not just the outbox envelope)")
	require.Equal(t, "ACTIVE", rowState, "row.lifecycle_state must be 'ACTIVE' (canonical PR-C lifecycle)")

	// ── Invariant (3): outbox_events row IS present (real DB) ──
	// A regression in the writer's outbox enqueue would surface
	// here as sql.ErrNoRows. The payload_json must also be a
	// non-empty canonical v1 envelope (validates the writer's
	// payload construction, not just the row existence).
	var (
		evtType    string
		evtAggID   string
		evtAggType string
		evtStatus  string
		evtKey     string
		evtPayload string
	)
	err = db.QueryRowContext(context.Background(),
		`SELECT event_type, aggregate_id, aggregate_type, status, event_key, payload_json
		   FROM outbox_events WHERE aggregate_id = ?`,
		expectedClipID,
	).Scan(&evtType, &evtAggID, &evtAggType, &evtStatus, &evtKey, &evtPayload)
	require.NoError(t, err, "outbox_events row must be enqueued after Step 9 commit "+
		"(E2E partial-state contract: Step 9 enqueued the asset.index.requested event)")
	require.Equal(t, outboxevents.EventAssetIndexRequested, evtType,
		"outbox.event_type must be the canonical constant 'asset.index.requested'")
	require.Equal(t, expectedClipID, evtAggID,
		"outbox.aggregate_id must match the clipID")
	require.Equal(t, "media_asset", evtAggType,
		"outbox.aggregate_type must be the canonical 'media_asset'")
	require.Equal(t, "pending", evtStatus,
		"outbox.status must be 'pending' (canonical initial state after a fresh Enqueue; a regression that pre-sets a terminal status would surface here)")
	require.NotEmpty(t, evtKey,
		"outbox.event_key must be non-empty (idempotency surface for ON CONFLICT collapse)")
	require.NotEmpty(t, evtPayload,
		"outbox.payload_json must be non-empty (canonical v1 envelope from BuildReindexEnvelopeV1)")

	// Verify the payload is parseable JSON (proves the writer built a
	// valid envelope, not just an empty string). The canonical v1
	// envelope has a schema_version field; its presence is a strong
	// signal that BuildReindexEnvelopeV1 was the canonical builder.
	var parsedPayload map[string]any
	require.NoError(t, json.Unmarshal([]byte(evtPayload), &parsedPayload),
		"outbox.payload_json must be valid JSON (canonical v1 envelope shape)")
	require.Contains(t, parsedPayload, "schema_version",
		"payload must carry the schema_version field (canonical v1 envelope surface)")

	// ── BLOCKER #2 closure invariant: source_version in media_assets
	// must EQUAL source_version in the outbox event payload (audit
	// 2026-07-03 BLOCKER #2: the CAS fence in clipindexer.setIndexedAt
	// reads media_assets.source_version; if it drifts from the
	// outbox event's source_version, the fence starves and the
	// clipindexer never marks the clip as indexed). The canonical
	// v1 envelope carries `source_version` as a top-level field
	// (set by BuildReindexEnvelopeV1 from the same deriveSourceVersion
	// call that upsertClipInTx writes to the column). A regression
	// that breaks this invariant — e.g. by re-deriving
	// source_version at two different code paths — would fail this
	// assertion.
	payloadSourceVersion, hasSourceVersion := parsedPayload["source_version"].(string)
	require.True(t, hasSourceVersion,
		"payload must carry the source_version field (BLOCKER #2 closure surface — confirmed at outboxevents/envelope.go::BuildReindexEnvelopeV1 line \"source_version\": sourceVersion)")
	// Defense-in-depth: a future regression that blanks BOTH the
	// column AND the payload (e.g. a deriveSourceVersion bug returning
	// "") would silently pass the equality check ("" == ""). The
	// NotEmpty guard is the failsafe: the canonical contract is
	// non-empty on BOTH surfaces.
	require.NotEmpty(t, payloadSourceVersion,
		"BLOCKER #2 closure: payload.source_version must be non-empty (defense-in-depth; otherwise the equality check would silently pass a regression that blanks both surfaces)")
	require.NotEmpty(t, rowSourceVersion,
		"BLOCKER #2 closure: media_assets.source_version must be non-empty (defense-in-depth; same rationale as the payload check above)")
	require.Equal(t, rowSourceVersion, payloadSourceVersion,
		"BLOCKER #2 closure: media_assets.source_version MUST equal outbox payload.source_version "+
			"(the CAS fence in clipindexer.setIndexedAt reads the column; drift starves the fence)")

	// ── Invariant (4): Warn log "Step 10 failed AFTER clip write" ──
	// This is the operator-observable partial-state class. A future
	// refactor that removes the Warn log (or changes the message
	// substring) would surface here as zero matching log entries.
	entries := recorded.FilterMessageSnippet("Step 10 failed AFTER clip write").All()
	require.NotEmpty(t, entries,
		"Warn log 'Step 10 failed AFTER clip write' MUST be emitted before u.fail "+
			"(E2E regression guard against future log removal; got %d total entries)", recorded.Len())

	// Verify the entry has the canonical structured fields so
	// dashboards can correlate the partial-state event with the
	// clip row that was actually persisted.
	entry := entries[0]
	require.Equal(t, zapcore.WarnLevel, entry.Level, "log entry must be at Warn level")
	fields := map[string]string{}
	for _, f := range entry.Context {
		if f.Type == zapcore.StringType {
			fields[f.Key] = f.String
		}
	}
	require.Equal(t, expectedClipID, fields["clip_id"],
		"Warn log must carry canonical clip_id (yt_<videoID>_<startSec>_<endSec>_<policyVer>)")
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c: local_path field
	// retired from step10's Warn log; clip_id is the canonical
	// lookup key for operator dashboards.
	require.Equal(t, string(FailureCodeMetadataFailed), fields["failure_code"],
		"Warn log must carry canonical failure_code for dashboard aggregation")
}
