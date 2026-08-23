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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
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
			namespace TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			semantic_role TEXT NOT NULL DEFAULT '',
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

// ── Test E2E-1: metadata analysis failure → NO commit (REAL writer + REAL outbox) ──

// TestMetadataAnalysisFailure_E2E_NoCommit is the E2E companion to the
// unit-level Test 9. Where Test 9 uses stubWriterAssetRecorder (proving
// the writer was NOT called), this test wires the REAL
// ClipAtomicWriterAdapter + outboxevents.Repository against in-memory
// SQLite and asserts on the REAL DB state via SELECT queries. The
// PR-ASSET-COMMITTER-ENRICHMENT contract: when the metadata analyzer
// fails INSIDE step6to9 BEFORE the commit, NO media_assets row and NO
// outbox event may exist — the former "clip committed without metadata"
// partial-state class is eliminated.
//
// Asserted invariants:
//
//	(1) Job status = FAILED with FailureCodeMetadataFailed (the
//	    canonical job outcome — typed *ExtractionError envelope).
//	(2) media_assets row is ABSENT post-Execute (the commit was
//	    never reached).
//	(3) outbox_events row is ABSENT post-Execute.
//	(4) The canonical Warn log "metadata analysis failed BEFORE clip
//	    write — clip not committed" WAS emitted.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion probes a
// falsifiable surface (real DB rows + real log entries). A
// regression in any of the 4 invariants would fail the test
// BEFORE the production deployment reaches an operator dashboard.
func TestMetadataAnalysisFailure_E2E_NoCommit(t *testing.T) {
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

	// ── 5) Wire a real MetadataService that always errors on AnalyzeClip ──
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
	metadata.MetadataService = metaSvc // always-fail metadata analyzer
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

	// ── 7) Execute: metadata analysis fails BEFORE the commit ──
	_, execErr := uc.Execute(context.Background(), cmd)

	// ── Invariant (1): typed job outcome ───────────────────────
	require.Error(t, execErr, "metadata analysis failure MUST surface as typed error")
	ee, ok := execErr.(*ExtractionError)
	require.True(t, ok, "error must be a typed *ExtractionError, got %T %v", execErr, execErr)
	require.Equal(t, FailureCodeMetadataFailed, ee.Code, "FailureCode must be FailureCodeMetadataFailed")
	require.False(t, ee.Retryable, "FailureCodeMetadataFailed is terminal (not retryable)")

	// ── Invariant (2): media_assets row is ABSENT (real DB) ──
	// The analysis failure happens INSIDE step6to9 BEFORE the canonical
	// commit, so the writer must never be reached and no media_assets
	// row may exist (the former partial-state class is eliminated).
	var count int
	err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM media_assets WHERE id = ?`,
		expectedClipID,
	).Scan(&count)
	require.NoError(t, err, "COUNT(media_assets) must succeed")
	require.Equal(t, 0, count,
		"media_assets row MUST NOT exist when metadata analysis fails before commit (no partial state)")

	// ── Invariant (3): outbox_events row is ABSENT (real DB) ──
	err = db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`,
		expectedClipID,
	).Scan(&count)
	require.NoError(t, err, "COUNT(outbox_events) must succeed")
	require.Equal(t, 0, count,
		"outbox_events row MUST NOT exist when metadata analysis fails before commit (no index event emitted)")

	// ── Invariant (4): Warn log "metadata analysis failed BEFORE clip write" ──
	entries := recorded.FilterMessageSnippet("metadata analysis failed BEFORE clip write").All()
	require.NotEmpty(t, entries,
		"Warn log 'metadata analysis failed BEFORE clip write' MUST be emitted before u.fail "+
			"(E2E regression guard against future log removal; got %d total entries)", recorded.Len())

	// Verify the entry has the canonical structured fields so
	// dashboards can correlate the failure with the clip that was
	// NOT committed.
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
	require.Equal(t, string(FailureCodeMetadataFailed), fields["failure_code"],
		"Warn log must carry canonical failure_code for dashboard aggregation")
}
