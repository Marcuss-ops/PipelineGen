// Package texttracks — text_track_repository_lookup_test.go
//
// Regression test for PR-ARGOS-TRANSLATION (Aug 2026): FindReady MUST
// return the is_current=1 (authoritative) row when a stale
// is_current=0 predecessor is still present with status=READY. Before
// the fix, FindReady's WHERE clause omitted `is_current = 1`, so a
// query that matched two READY rows (an old gemma/ollama track and a
// new argos track) could return the stale row — and, worse, the stale
// track's own per-segment cues — which made the multilingual renderer
// burn the OLD subtitles into the final video.
package texttracks

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"go.uber.org/zap"
)

// lookupTestSchema is a production-faithful subset of the two tables
// FindReady touches. Columns match the SELECT projection in
// text_track_repository_lookup.go::FindReady and the segment shape in
// findCuesForTrackID. FKs are intentionally not enforced here: the
// repository's read path never depends on them.
const lookupTestSchema = `
CREATE TABLE IF NOT EXISTS asset_text_tracks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    language_code TEXT NOT NULL,
    text_kind TEXT NOT NULL,
    text_content TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'provided',
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
    confidence REAL,
    status TEXT NOT NULL DEFAULT 'READY',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    source_track_id INTEGER,
    source_text_hash TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS asset_text_track_segments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    track_id INTEGER NOT NULL,
    sequence_no INTEGER NOT NULL,
    start_ms INTEGER NOT NULL,
    end_ms INTEGER NOT NULL,
    text TEXT NOT NULL,
    text_hash TEXT NOT NULL DEFAULT '',
    UNIQUE(track_id, sequence_no)
);
`

func newLookupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err, "open in-memory sqlite")
	db.SetMaxOpenConns(1) // keep the single in-memory store pinned to one conn
	_, err = db.Exec(lookupTestSchema)
	require.NoError(t, err, "apply schema")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertTrack(t *testing.T, db *sql.DB, lang, modelVersion string, isCurrent int, cueText string) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO asset_text_tracks
			(asset_id, language_code, text_kind, text_content, source_type,
			 source_language_code, is_original, provider, model_name,
			 model_version, prompt_version, text_hash, source_version,
			 translation_key, is_current, confidence, status,
			 created_at, updated_at, source_track_id, source_text_hash)
		VALUES (?, ?, 'transcript', ?, 'translation', 'en', 0, ?, ?, ?, '',
		        ?, '', '', ?, NULL, 'READY', '', '', NULL, '')`,
		"asset-1", lang, "full text", "ollama", "ollama", modelVersion, "hash", isCurrent)
	require.NoError(t, err, "insert track %s", lang)
	id, err := res.LastInsertId()
	require.NoError(t, err, "last insert id %s", lang)
	_, err = db.Exec(`
		INSERT INTO asset_text_track_segments (track_id, sequence_no, start_ms, end_ms, text)
		VALUES (?, 1, 0, 2400, ?)`,
		id, cueText)
	require.NoError(t, err, "insert segment for %s", lang)
	return id
}

func TestFindReady_PrefersCurrentRowOverStalePredecessor(t *testing.T) {
	db := newLookupDB(t)
	repo, err := NewTextTrackRepository(db, zap.NewNop())
	require.NoError(t, err)

	// Stale predecessor: is_current=0, still status=READY, carries the
	// OLD subtitle text. This is exactly the state the flip-and-insert
	// audit path leaves behind.
	insertTrack(t, db, "it", "gemma4:e4b", 0, "In realtà, ho messo il mio,")
	// Current authoritative row: is_current=1, fresh Argos cue text.
	insertTrack(t, db, "it", "argos-translate/v2", 1, "Veramente, ho messo i miei,")

	track, cues, err := repo.FindReady(context.Background(), "asset-1", "it", detail.TextTrackTranscript)
	require.NoError(t, err)
	require.NotNil(t, track, "FindReady must return the authoritative track")
	if track.IsCurrent != true {
		t.Fatalf("FindReady returned a non-current track (is_current=%v, model_version=%q)", track.IsCurrent, track.ModelVersion)
	}
	if track.ModelVersion != "argos-translate/v2" {
		t.Fatalf("FindReady returned the stale track: model_version=%q, want %q", track.ModelVersion, "argos-translate/v2")
	}
	require.Len(t, cues, 1, "FindReady must return the current track's cues")
	if cues[0].Text != "Veramente, ho messo i miei," {
		t.Fatalf("FindReady returned stale cues: %q", cues[0].Text)
	}
}
