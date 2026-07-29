// Package assets — clip_atomic_writer_localized_test.go: pins the
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b (July 2026) atomic
// super-tx surface of ClipAtomicWriterAdapter.commitClipTextAndIndexEvent_.
//
// What this test asserts:
//
//  1. Happy path: CommitClipTextAndIndexEvent on a fresh ledger
//     inserts exactly ONE row each in media_assets,
//     asset_text_tracks, asset_text_track_segments, AND
//     outbox_events — all committed atomically. The asset_text_*
//     tables are governed by migration 144 (inline create-if-not-
//     exists at test setup).
//
//  2. Atomic rollback on bad cue: when a TimedTextTrack cue has
//     EndMs < StartMs the writer surfaces a typed error and the
//     deferred tx.Rollback() fires BEFORE COMMIT. No rows are
//     visible in any of the 4 surfaces.
//
//  3. ErrClipLocaleNotReady pre-tx: when
//     RequireTranscriptReady=true and no transcript-origin track
//     is present in TextTracks, the writer returns the typed
//     error BEFORE opening the tx. No rows visible in any table.
//
//  4. RequireAllLanguagesBeforeVideo: when PreferredLanguages
//     contains languages not present in TextTracks, the writer
//     returns ErrClipLocaleNotReady pre-tx.
//
//  5. Idempotency on replay: a second call with the same inputs
//     collapses via ON CONFLICT(asset_id, language_code, text_kind)
//     DO UPDATE for text tracks, ON CONFLICT(event_key) DO
//     NOTHING for outbox_events, and ON CONFLICT(id) DO UPDATE
//     for media_assets — exactly 1 of each row remains.
//
//  6. Orphan TimedTextTrack rejection: a TimedTextTrack with no
//     matching TextTrack row is rejected with a typed error before
//     any rows are written.
//
//  7. Different FileHash emits second outbox row (supersede gate):
//     a second CommitClipTextAndIndexEvent with a different
//     FileHash on the same ClipAsset.ID produces a NEW
//     outbox_events row (the canonical content-hash supersede
//     pattern) while asset_text_tracks collapses to 1 row via
//     ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE.
//     media_assets.source_version is updated to the LATEST
//     FileHash so the clipindexer CAS fence reads the freshest
//     content.
//
// Schema: minimal in-memory SQLite with the production-faithful
// subset of media_assets + asset_text_tracks +
// asset_text_track_segments + outbox_events. FK constraints
// enabled so the writer's FK contract is exercised.
package assets

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// localizedClipWriterSchema is the in-memory test schema matching
// the production column shapes consumed by commitClipTextAndIndexEvent_.
// FK constraints ON DELETE CASCADE on asset_text_track_segments
// match the produced migration 144 (the writer itself does not
// read segments). The partial unique index
// idx_asset_text_tracks_current (WHERE is_current = 1) matches the
// canonical schema from internal/infrastructure/database/canonical.go.
const localizedClipWriterSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT, name TEXT, filename TEXT, media_type TEXT,
    drive_file_id TEXT, drive_link TEXT, download_link TEXT,
    local_path TEXT, file_hash TEXT,
    folder_id TEXT, folder_path TEXT,
    source_version TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
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
    created_at TEXT, updated_at TEXT
);
CREATE TABLE IF NOT EXISTS asset_text_tracks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    language_code TEXT NOT NULL,
    text_kind TEXT NOT NULL,
    text_content TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original INTEGER NOT NULL DEFAULT 0,
    provider TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    model_version TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    text_hash TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    translation_key TEXT NOT NULL DEFAULT '',
    is_current INTEGER NOT NULL DEFAULT 1,
    source_track_id INTEGER REFERENCES asset_text_tracks(id) ON DELETE SET NULL,
    source_text_hash TEXT NOT NULL DEFAULT '',
    confidence REAL,
    status TEXT NOT NULL DEFAULT 'READY',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_text_tracks_current
    ON asset_text_tracks (asset_id, language_code, text_kind)
    WHERE is_current = 1;
CREATE TABLE IF NOT EXISTS asset_text_track_segments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    track_id INTEGER NOT NULL,
    sequence_no INTEGER NOT NULL,
    start_ms INTEGER NOT NULL,
    end_ms INTEGER NOT NULL,
    text TEXT NOT NULL,
    FOREIGN KEY (track_id) REFERENCES asset_text_tracks(id) ON DELETE CASCADE,
    UNIQUE(track_id, sequence_no)
);
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT,
    worker_id TEXT,
    lease_id TEXT,
    lease_expiry TEXT,
    completed_at TEXT,
    next_attempt_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key ON outbox_events(event_key);
CREATE TABLE IF NOT EXISTS asset_locations (
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
);
`

// newLocalizedWriterDB opens an in-memory SQLite with FK enforcement
// turned on so the writer's FK contract is exercised end-to-end.
// SetMaxOpenConns(1) is required for in-memory sqlite so transactional
// operations scope to the same physical connection (per
// clip_atomic_writer_test.go / txmutation/primitives_test.go precedent).
func newLocalizedWriterDB(t *testing.T) *sql.DB {
	t.Helper()
	db, openErr := sql.Open("sqlite3", ":memory:?_foreign_keys=on&_busy_timeout=5000")
	if openErr != nil {
		t.Fatalf("open :memory: sqlite: %v", openErr)
	}
	db.SetMaxOpenConns(1)
	if _, execErr := db.Exec(localizedClipWriterSchema); execErr != nil {
		t.Fatalf("apply schema: %v", execErr)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// makeClipAssetForTest builds the canonical ClipAsset input shape.
// Lifecycle state mirrors the production "ACTIVE" default.
func makeClipAssetForTest(clipID, videoID, fileHash string) youtubetypes.ClipAsset {
	return youtubetypes.ClipAsset{
		ID:        clipID,
		VideoID:   videoID,
		FileHash:  fileHash,
		LocalPath: "/tmp/clips/" + clipID + ".mp4",
		Drive: youtubetypes.ClipAssetDrive{
			FolderID:    "folder_" + clipID,
			FolderPath:  "youtube/" + videoID,
			FileID:      "drive_" + clipID,
			WebViewLink: "https://drive.google.com/file/d/drive_" + clipID + "/view",
		},
		Coordinates: youtubetypes.ClipAssetCoordinates{
			StartSec: 10,
			EndSec:   60,
			Duration: 50,
		},
		Metadata: youtubetypes.CanonicalClipMetadata{
			Summary:         "Localized Happy Path",
			NormalizedGroup: "general",
		},
		PolicyVersion: "v1",
	}
}

// makeTrackForTest builds a canonical TextTrack row with hashes
// populated via the canonical factory (the writer does NOT
// recompute — it reads TextHash + SourceVersion verbatim).
func makeTrackForTest(clipID, lang, content string, kind asset.TextTrackKind, src asset.TextTrackSource) asset.TextTrack {
	hash := asset.TextHash(content, lang, kind)
	return asset.TextTrack{
		AssetID:            clipID,
		LanguageCode:       lang,
		TextKind:           kind,
		TextContent:        content,
		SourceType:         src,
		SourceLanguageCode: lang,
		IsOriginal:         src == asset.TextSourceProvided || src == asset.TextSourceWhisper || src == asset.TextSourceYouTubeSubtitle,
		Provider:           "test",
		TextHash:           hash,
		SourceVersion:      asset.SourceVersion(hash, lang, lang, "test", "", "", ""),
		Status:             asset.TextTrackReady,
	}
}

// ── Test 1: happy path — all 4 surfaces committed atomically ────────

// TestCommitClipTextAndIndexEvent_HappyPath inserts one row each in
// media_assets + asset_text_tracks + asset_text_track_segments +
// outbox_events in ONE tx. After CommitClipTextAndIndexEvent returns
// nil, every row is queryable. This pins the production super-tx.
func TestCommitClipTextAndIndexEvent_HappyPath(t *testing.T) {
	db := newLocalizedWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_localized_happy_001_10_60_v1"
	fileHash := sha256Hex("localized-happy-path")
	clipAsset := makeClipAssetForTest(clipID, "localized_happy_001", fileHash)

	cmd := localized.CommitLocalizedClipCommand{
		Clip: clipAsset,
		TextTracks: []asset.TextTrack{
			makeTrackForTest(clipID, "en", "Hello everyone", asset.TextTrackTranscript, asset.TextSourceProvided),
			makeTrackForTest(clipID, "it", "Benvenuti a tutti", asset.TextTrackTranscript, asset.TextSourceProvided),
		},
		TimedTracks: []localized.TimedTextTrack{
			{
				LanguageCode: "en",
				TextKind:     asset.TextTrackTranscript,
				SourceType:   asset.TextSourceProvided,
				Cues: []asset.TimedCue{
					{StartMs: 0, EndMs: 2200, Text: "Hello everyone"},
					{StartMs: 2200, EndMs: 5100, Text: "Welcome to the video"},
				},
			},
		},
		IndexEvent: youtubeports.IndexEventPayload{},
	}

	if err := adapter.CommitClipTextAndIndexEvent(context.Background(), cmd); err != nil {
		t.Fatalf("CommitClipTextAndIndexEvent happy path: %v", err)
	}

	// media_assets
	var mediaCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM media_assets WHERE id = ?`, clipID).Scan(&mediaCount); err != nil {
		t.Fatalf("count media_assets: %v", err)
	}
	if mediaCount != 1 {
		t.Errorf("media_assets: want 1 row got %d", mediaCount)
	}

	// asset_text_tracks — one row per (lang, kind=transcript).
	var trackCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_text_tracks WHERE asset_id = ?`, clipID).Scan(&trackCount); err != nil {
		t.Fatalf("count asset_text_tracks: %v", err)
	}
	if trackCount != 2 {
		t.Errorf("asset_text_tracks: want 2 rows (en+it transcript) got %d", trackCount)
	}

	// asset_text_track_segments — 2 cues for the en track (the it
	// track has no cues in this test; that is by design, not a bug).
	var segCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM asset_text_track_segments s JOIN asset_text_tracks t ON s.track_id=t.id WHERE t.asset_id=?`, clipID).Scan(&segCount); err != nil {
		t.Fatalf("count asset_text_track_segments: %v", err)
	}
	if segCount != 2 {
		t.Errorf("asset_text_track_segments: want 2 rows got %d", segCount)
	}

	// outbox_events
	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, clipID).Scan(&outCount); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	if outCount != 1 {
		t.Errorf("outbox_events: want 1 row got %d", outCount)
	}

	// Sanity: source_version present in media_assets (BLOCKER #2).
	var sv string
	if err := db.QueryRow(`SELECT source_version FROM media_assets WHERE id = ?`, clipID).Scan(&sv); err != nil {
		t.Fatalf("read source_version: %v", err)
	}
	if sv != fileHash {
		t.Errorf("source_version: want %q got %q", fileHash, sv)
	}
}

// ── Test 2: atomic rollback on bad cue (writer-level invariant) ────

// TestCommitClipTextAndIndexEvent_RollbackOnBadCue forces a writer-
// level invariant failure: a TimedTextTrack cue with EndMs < StartMs.
// The writer's insertTextTrackSegmentsInTx rejects the cue before
// the row is inserted; the deferred tx.Rollback() fires. NO row
// must be visible in any of the 4 surfaces after the error.
// Pre-empted as the canonical Rule-of-Zero scenario: a mid-tx
// failure MUST NOT leave orphan rows.
func TestCommitClipTextAndIndexEvent_RollbackOnBadCue(t *testing.T) {
	db := newLocalizedWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_localized_rollback_badcue_001_10_60_v1"
	fileHash := sha256Hex("localized-rollback-badcue")
	clipAsset := makeClipAssetForTest(clipID, "localized_rollback_001", fileHash)

	cmd := localized.CommitLocalizedClipCommand{
		Clip: clipAsset,
		TextTracks: []asset.TextTrack{
			makeTrackForTest(clipID, "en", "Hello", asset.TextTrackTranscript, asset.TextSourceProvided),
		},
		TimedTracks: []localized.TimedTextTrack{
			{
				LanguageCode: "en",
				TextKind:     asset.TextTrackTranscript,
				SourceType:   asset.TextSourceProvided,
				Cues: []asset.TimedCue{
					// VALID first cue (seq=1).
					{StartMs: 0, EndMs: 2200, Text: "Hello"},
					// INVALID second cue: end < start. The writer
					// surfaces this as a typed error and the
					// deferred tx.Rollback() cancels the entire tx.
					{StartMs: 5000, EndMs: 4000, Text: "negative duration"},
				},
			},
		},
		IndexEvent: youtubeports.IndexEventPayload{},
	}

	err := adapter.CommitClipTextAndIndexEvent(context.Background(), cmd)
	require.Error(t, err, "Invalid cue MUST surface a typed error")
	require.Contains(t, err.Error(), "invalid cue", "error must identify the cue invariant violation")

	// Assert every surface is empty (rollback complete).
	for tbl, where := range map[string]string{
		"media_assets":              "id = ?",
		"asset_text_tracks":         "asset_id = ?",
		"asset_text_track_segments": "track_id IN (SELECT id FROM asset_text_tracks WHERE asset_id = ?)",
		"outbox_events":             "aggregate_id = ?",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+tbl+` WHERE `+where, clipID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		if count != 0 {
			t.Errorf("atomic rollback FAILED: %s has %d rows for clip=%s (must be 0)", tbl, count, clipID)
		}
	}
}

// ── Test 3: ErrClipLocaleNotReady pre-tx (RequireTranscriptReady) ───

// TestCommitClipTextAndIndexEvent_LocaleNotReady pins the godlike/06
// SSOT for the multilingual policy: when RequireTranscriptReady=true
// and no transcript-origin track is in TextTracks, the writer
// surfaces ErrClipLocaleNotReady BEFORE BeginTx. NO rows in any
// of the 4 surfaces after the error.
//
// Pin target for Fase 5 backfill: errors.As-probe this type.
func TestCommitClipTextAndIndexEvent_LocaleNotReady(t *testing.T) {
	db := newLocalizedWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_localized_notready_001_10_60_v1"
	fileHash := sha256Hex("localized-notready")
	clipAsset := makeClipAssetForTest(clipID, "localized_notready_001", fileHash)

	cmd := localized.CommitLocalizedClipCommand{
		Clip: clipAsset,
		// NO transcript-origin track in TextTracks. The policy
		// validator MUST short-circuit before BeginTx.
		TextTracks: []asset.TextTrack{
			makeTrackForTest(clipID, "en", "Howdy", asset.TextTrackTitle, asset.TextSourceProvided),
		},
		TimedTracks:            []localized.TimedTextTrack{},
		IndexEvent:             youtubeports.IndexEventPayload{},
		RequireTranscriptReady: true,
	}

	err := adapter.CommitClipTextAndIndexEvent(context.Background(), cmd)
	require.Error(t, err, "RequireTranscriptReady=true with no transcript MUST return a typed error")
	var typed *localized.ErrClipLocaleNotReady
	require.True(t, errors.As(err, &typed), "error MUST be errors.As-probeable as *localized.ErrClipLocaleNotReady; got %T %v", err, err)
	require.Equal(t, clipID, typed.AssetID)
	require.Equal(t, asset.TextTrackTranscript, typed.MissingKind)
	require.Contains(t, typed.Error(), "no transcript-origin READY track")

	// Assert every surface is empty (the policy validator runs
	// pre-tx, so this assertion folds to "writer never opened a tx").
	policyPreTxChecks := []struct {
		table string
		query string
	}{
		{"media_assets", `SELECT COUNT(*) FROM media_assets WHERE id = ?`},
		{"asset_text_tracks", `SELECT COUNT(*) FROM asset_text_tracks WHERE asset_id = ?`},
		{"outbox_events", `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`},
	}
	for _, c := range policyPreTxChecks {
		var count int
		if err := db.QueryRow(c.query, clipID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", c.table, err)
		}
		if count != 0 {
			t.Errorf("policy validator pre-tx path FAILED: %s has %d rows for clip=%s", c.table, count, clipID)
		}
	}
}

// ── Test 4: ErrClipLocaleNotReady — PreferredLanguages missing ──────

// TestCommitClipTextAndIndexEvent_LocaleNotReady_MissingLang checks
// the additive language-coverage invariant: when
// RequireAllLanguagesBeforeVideo=true and one of the
// PreferredLanguages has no READY transcript, the writer surfaces
// ErrClipLocaleNotReady with MissingCodes populated.
func TestCommitClipTextAndIndexEvent_LocaleNotReady_MissingLang(t *testing.T) {
	db := newLocalizedWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_localized_missinglang_001_10_60_v1"
	fileHash := sha256Hex("localized-missinglang")
	clipAsset := makeClipAssetForTest(clipID, "localized_missinglang_001", fileHash)

	cmd := localized.CommitLocalizedClipCommand{
		Clip: clipAsset,
		TextTracks: []asset.TextTrack{
			makeTrackForTest(clipID, "en", "Hello", asset.TextTrackTranscript, asset.TextSourceProvided),
		},
		TimedTracks:                    []localized.TimedTextTrack{},
		IndexEvent:                     youtubeports.IndexEventPayload{},
		RequireAllLanguagesBeforeVideo: true,
		PreferredLanguages:             []string{"en", "it", "es"},
	}

	err := adapter.CommitClipTextAndIndexEvent(context.Background(), cmd)
	require.Error(t, err)
	var typed *localized.ErrClipLocaleNotReady
	require.True(t, errors.As(err, &typed))
	require.ElementsMatch(t, []string{"it", "es"}, typed.MissingCodes,
		"MissingCodes must enumerate exactly the languages missing transcripts")
}

// ── Test 5: idempotency on replay ────────────────────────────────────

// TestCommitClipTextAndIndexEvent_IdempotentOnReplay pins the replay
// contract: a second call with the SAME inputs collapses via
// ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE for
// text tracks, ON CONFLICT(track_id, sequence_no) DO UPDATE for
// segments, ON CONFLICT(event_key) DO NOTHING for outbox_events,
// AND ON CONFLICT(id) DO UPDATE for media_assets. Exactly 1 of
// each row remains. The asset_id-keyed counts MUST count 1 per
// table, not 2.
//
// godlike/06 SSOT: the canonical non-fatal replay contract — the
// same clip retry must NOT double-write rows, but the writer MUST
// still return nil (no error) so callers don't trigger a
// retryloop.
func TestCommitClipTextAndIndexEvent_IdempotentOnReplay(t *testing.T) {
	db := newLocalizedWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_localized_idem_001_10_60_v1"
	fileHash := sha256Hex("localized-idem-content")
	clipAsset := makeClipAssetForTest(clipID, "localized_idem_001", fileHash)

	cmd := localized.CommitLocalizedClipCommand{
		Clip: clipAsset,
		TextTracks: []asset.TextTrack{
			makeTrackForTest(clipID, "en", "Hello everyone", asset.TextTrackTranscript, asset.TextSourceProvided),
		},
		TimedTracks: []localized.TimedTextTrack{
			{
				LanguageCode: "en",
				TextKind:     asset.TextTrackTranscript,
				SourceType:   asset.TextSourceProvided,
				Cues: []asset.TimedCue{
					{StartMs: 0, EndMs: 2200, Text: "Hello everyone"},
				},
			},
		},
		IndexEvent: youtubeports.IndexEventPayload{},
	}
	ctx := context.Background()

	if err := adapter.CommitClipTextAndIndexEvent(ctx, cmd); err != nil {
		t.Fatalf("first CommitClipTextAndIndexEvent: %v", err)
	}
	if err := adapter.CommitClipTextAndIndexEvent(ctx, cmd); err != nil {
		t.Fatalf("second CommitClipTextAndIndexEvent (replay): %v", err)
	}

	// Explicit per-table count queries (each names the right column).
	counts := []struct {
		label string
		query string
		args  []interface{}
		want  int
	}{
		{
			label: "media_assets",
			query: `SELECT COUNT(*) FROM media_assets WHERE id = ?`,
			args:  []interface{}{clipID},
			want:  1,
		},
		{
			label: "asset_text_tracks",
			query: `SELECT COUNT(*) FROM asset_text_tracks WHERE asset_id = ?`,
			args:  []interface{}{clipID},
			want:  1,
		},
		{
			label: "asset_text_track_segments",
			query: `SELECT COUNT(*) FROM asset_text_track_segments s JOIN asset_text_tracks t ON s.track_id = t.id WHERE t.asset_id = ?`,
			args:  []interface{}{clipID},
			want:  1,
		},
		{
			label: "outbox_events",
			query: `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`,
			args:  []interface{}{clipID},
			want:  1,
		},
	}
	for _, c := range counts {
		var got int
		if err := db.QueryRow(c.query, c.args...).Scan(&got); err != nil {
			t.Fatalf("count %s after replay: %v", c.label, err)
		}
		if got != c.want {
			t.Errorf("%s after replay: want %d got %d (ON CONFLICT collapse violated)", c.label, c.want, got)
		}
	}
}

// ── Test 6: FK enforcement on TimedTextTrack orphan rejection ──────

// TestCommitClipTextAndIndexEvent_OrphanTimedTrackRejected verifies
// that a TimedTextTrack with no matching TextTrack row is
// rejected by the writer. This guards against a caller passing
// cues for a language/kind/source triplet that doesn't appear in
// TextTracks — the writer's match-by-key invariant surfaces a
// typed error.
func TestCommitClipTextAndIndexEvent_OrphanTimedTrackRejected(t *testing.T) {
	db := newLocalizedWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_localized_orphan_001_10_60_v1"
	fileHash := sha256Hex("localized-orphan")
	clipAsset := makeClipAssetForTest(clipID, "localized_orphan_001", fileHash)

	cmd := localized.CommitLocalizedClipCommand{
		Clip: clipAsset,
		TextTracks: []asset.TextTrack{
			// English transcript only.
			makeTrackForTest(clipID, "en", "Hello", asset.TextTrackTranscript, asset.TextSourceProvided),
		},
		TimedTracks: []localized.TimedTextTrack{
			{
				// ITALIAN cues, but no Italian text track in TextTracks
				// above. The writer's match-by-key must reject this.
				LanguageCode: "it",
				TextKind:     asset.TextTrackTranscript,
				SourceType:   asset.TextSourceProvided,
				Cues: []asset.TimedCue{
					{StartMs: 0, EndMs: 2200, Text: "Ciao"},
				},
			},
		},
		IndexEvent: youtubeports.IndexEventPayload{},
	}

	err := adapter.CommitClipTextAndIndexEvent(context.Background(), cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no matching TextTrack",
		"error must identify the orphan-timed-track rejection: got %v", err)
}

// ── Test 7: different FileHash → 2 outbox rows (super-tx supersede) ─

// TestCommitClipTextAndIndexEvent_DifferentFileHashEmitsSecondRow
// pins the content-hash supersede contract for the localized
// atomic super-tx (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b,
// July 2026). Mirrors TestClipAtomicWriter_DifferentFileHashEmitsSecondRow
// in clip_atomic_writer_test.go (the legacy non-text-tracks
// stripe) at the super-tx stripe: two calls to
// CommitClipTextAndIndexEvent with a different FileHash on the
// same ClipAsset.ID must produce TWO outbox_events rows.
//
// Why two rows (not one) on a different FileHash:
//   - deriveSourceVersion(clipID, fileHash, policyVersion) returns
//     fileHash (when non-empty) → different FileHash → different
//     sourceVersion.
//   - BuildReindexEnvelopeV1 builds eventKey as
//     "reconcile:reindex:<assetID>:<schema>:<sourceVersion>" →
//     different sourceVersion → different eventKey.
//   - The outbox UPSERT uses ON CONFLICT(event_key) DO NOTHING,
//     so a different eventKey INSERTs a fresh row.
//
// The supersede gate downstream (clipindexer.IndexingHandler
// source_version check) fires on the new row, replacing the old
// content. asset_text_tracks collapses via
// ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE so
// still 1 track per (lang, kind) tuple; media_assets.source_version
// is updated to the LATEST FileHash (the source the clipindexer
// CAS fence reads).
func TestCommitClipTextAndIndexEvent_DifferentFileHashEmitsSecondRow(t *testing.T) {
	db := newLocalizedWriterDB(t)
	box := outboxevents.NewRepository(db)
	adapter := NewClipAtomicWriterAdapter(db, box, nil)

	const clipID = "yt_localized_supersede_001_10_60_v1"
	fileHashA := sha256Hex("localized-supersede-content-A")
	fileHashB := sha256Hex("localized-supersede-content-B")
	clipAssetA := makeClipAssetForTest(clipID, "localized_supersede_001", fileHashA)
	clipAssetB := makeClipAssetForTest(clipID, "localized_supersede_001", fileHashB)

	// makeCmd builds a fresh command for each call. The text track
	// content differs (A vs B) so the TextHash + TextTrack.SourceVersion
	// also differ — defensive against future writers that derive
	// sourceVersion from the TextTrack rather than from cmd.Clip.FileHash.
	makeCmd := func(clipAsset youtubetypes.ClipAsset, content string) localized.CommitLocalizedClipCommand {
		return localized.CommitLocalizedClipCommand{
			Clip: clipAsset,
			TextTracks: []asset.TextTrack{
				makeTrackForTest(clipID, "en", content, asset.TextTrackTranscript, asset.TextSourceProvided),
			},
			TimedTracks: []localized.TimedTextTrack{
				{
					LanguageCode: "en",
					TextKind:     asset.TextTrackTranscript,
					SourceType:   asset.TextSourceProvided,
					Cues: []asset.TimedCue{
						{StartMs: 0, EndMs: 2200, Text: content},
					},
				},
			},
			IndexEvent: youtubeports.IndexEventPayload{},
		}
	}
	ctx := context.Background()

	if err := adapter.CommitClipTextAndIndexEvent(ctx, makeCmd(clipAssetA, "Content A")); err != nil {
		t.Fatalf("first call (content-A): %v", err)
	}
	if err := adapter.CommitClipTextAndIndexEvent(ctx, makeCmd(clipAssetB, "Content B")); err != nil {
		t.Fatalf("second call (content-B): %v", err)
	}

	// Supersede gate: 2 outbox rows (one per sourceVersion).
	var outCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = ?`, clipID).Scan(&outCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outCount != 2 {
		t.Errorf("outbox_events count after different content: want 2 got %d (supersede gate collapsed — FileHash-derived sourceVersion MUST differ)", outCount)
	}

	// Sanity: two distinct event_keys (the canonical differentiator).
	rows, err := db.Query(`SELECT event_key FROM outbox_events WHERE aggregate_id = ? ORDER BY id ASC`, clipID)
	if err != nil {
		t.Fatalf("query event_keys: %v", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan event_key: %v", err)
		}
		keys = append(keys, k)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 distinct keys, got %v", keys)
	}
	if keys[0] == keys[1] {
		t.Errorf("different FileHash MUST produce different event_keys; both rows had %q", keys[0])
	}

	// media_assets.source_version reflects the LATEST content
	// (ON CONFLICT(id) DO UPDATE). The clipindexer CAS fence
	// reads this column.
	var sv string
	if err := db.QueryRow(`SELECT source_version FROM media_assets WHERE id = ?`, clipID).Scan(&sv); err != nil {
		t.Fatalf("read source_version: %v", err)
	}
	if sv != fileHashB {
		t.Errorf("media_assets.source_version: want %q (latest) got %q", fileHashB, sv)
	}

	// asset_text_tracks collapses to 1 row
	// (ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE).
	var trackCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_text_tracks WHERE asset_id = ?`, clipID).Scan(&trackCount); err != nil {
		t.Fatalf("count asset_text_tracks: %v", err)
	}
	if trackCount != 1 {
		t.Errorf("asset_text_tracks: want 1 (ON CONFLICT collapse) got %d", trackCount)
	}
}

// ── End of Fase 2.b atomic-super-tx tests ───────────────────────────
// All helpers (makeClipAssetForTest, makeTrackForTest,
// newLocalizedWriterDB) live at the top of the file. The 7 tests above
// cover the canonical super-tx surface: happy path, atomic rollback
// on bad cue (deferred tx.Rollback), ErrClipLocaleNotReady before
// BeginTx, missing-language coverage, idempotent replay overlap,
// orphan-timed-track rejection, and the content-hash supersede gate
// (different FileHash → 2 outbox rows).
