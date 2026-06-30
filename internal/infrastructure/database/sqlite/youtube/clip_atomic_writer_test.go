// Package ytsqlite — regression tests for the Commit F ClipAtomicWriter.
//
// Test plan (3 scenarios — pinned contracts):
//  1. HappyPath — single CommitClipAndIndexEvent call writes 1
//     media_assets row + 1 outbox_events row in one atomic tx.
//  2. Idempotency — calling CommitClipAndIndexEvent twice with the
//     same clipID must NOT duplicate the outbox row (event_key
//     UNIQUE → ON CONFLICT DO NOTHING collapses the duplicate).
//  3. Validation — clipID="" / event.AggregateID != clipID surface
//     typed errors verbatim so the parent's retry logic can branch
//     via errors.Is(err, ErrClipWriterNil) without substring parsing.
//
// Test fixtures use the canonical CanonicalMediaAssetsSchema
// (storage/canonical.go) for media_assets and an inline-minified
// copy of the outbox_events migration schema (092) to keep the
// sqlite subsystem independent of the migrations/sqlite/ directory
// (tests in this subsystem historically inline their own schemas —
// see internal/infrastructure/database/sqlite/outbox for the
// pattern).
package ytsqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqoutbox "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"errors"
)

// ── Inlined outbox_events schema (mirrors migrations/sqlite/092) ────────
//
// We inline the schema because tests in the yt/sqlite subsystem
// historically don't reach into migrations/sqlite/*.sql. The columns
// declared here match the columns the production insertion statement
// expects exactly (event_type, aggregate_id, aggregate_type,
// payload_json, event_key, status, attempt_count, max_attempts,
// created_at, updated_at).

const outboxEventsTestSchema = `
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key);
`

// openTestDB constructs a fresh in-memory SQLite schema with
// media_assets (canonical schema) + outbox_events (inlined) tables
// pre-applied. A temp-dir backed file is opened because Go's
// database/sql guards against multiple in-memory connections being
// the same database (each ":memory:" handle is independent — a
// multi-table regression test needs a SINGLE shared backing).
func openTestDB(t *testing.T) (*sql.DB, *sqassets.ClipsRepository, sqoutbox.TxManager) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on")
	require.NoError(t, err)
	_, err = db.Exec(storage.CanonicalMediaAssetsSchema)
	require.NoError(t, err)
	_, err = db.Exec(outboxEventsTestSchema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	clips := sqassets.NewClipsRepository(db, zap.NewNop())
	txMgr := sqoutbox.NewManager(db, zap.NewNop())
	return db, clips, txMgr
}

func makeItem(clipID string) youtubetypes.ExtractItem {
	return youtubetypes.ExtractItem{
		ID:           clipID,
		Name:         "intro",
		Start:        "0",
		End:          "10",
		StartSeconds: 0,
		EndSeconds:   10,
		Duration:     10,
		Filename:     "intro.mp4",
		Status:       "processed",
	}
}

func makeEvent(clipID string) youtubeports.IndexEventPayload {
	return youtubeports.IndexEventPayload{
		Type:        "asset.index.requested",
		AggregateID: clipID,
		Payload:     []byte(`{"clip_id":"` + clipID + `"}`),
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
	}
}

// TestCommitClipAndIndexEvent_HappyPath pins the canonical Commit F
// contract: a single call writes BOTH a media_assets row AND an
// outbox_events row in one atomic tx. Row count ==1 in each table;
// the asset row's id matches the clipID; the event row's event_key
// matches the clipID; the event row's payload_json roundtrips the
// payload bytes verbatim.
func TestCommitClipAndIndexEvent_HappyPath(t *testing.T) {
	db, clips, txMgr := openTestDB(t)
	w := NewClipAtomicWriter(clips, txMgr, zap.NewNop())

	const clipID = "yt_dQw4w9WgXcQ_0_10_v1"
	item := makeItem(clipID)
	event := makeEvent(clipID)
	ctx := context.Background()

	require.NoError(t, w.CommitClipAndIndexEvent(ctx, clipID, item, event),
		"happy-path CommitClipAndIndexEvent must succeed")

	var mediaCount int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_assets WHERE id = ?", clipID).Scan(&mediaCount))
	assert.Equal(t, 1, mediaCount, "media_assets row must exist exactly once for clipID")

	var eventCount int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM outbox_events WHERE event_key = ?", clipID).Scan(&eventCount))
	assert.Equal(t, 1, eventCount, "outbox_events row must exist exactly once for event_key=clipID")

	// Spot-check the round-trip fields.
	var (
		gotDBName   string
		gotFilename string
	)
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT name, filename FROM media_assets WHERE id = ?", clipID).Scan(&gotDBName, &gotFilename))
	assert.Equal(t, item.Name, gotDBName, "media_assets.name must equal item.Name")
	assert.Equal(t, item.Filename, gotFilename, "media_assets.filename must equal item.Filename")

	var (
		gotEventType   string
		gotPayload     string
		gotAggregateID string
	)
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT event_type, payload_json, aggregate_id FROM outbox_events WHERE event_key = ?",
		clipID).Scan(&gotEventType, &gotPayload, &gotAggregateID))
	assert.Equal(t, event.Type, gotEventType)
	assert.Equal(t, string(event.Payload), gotPayload)
	assert.Equal(t, clipID, gotAggregateID)
}

// TestCommitClipAndIndexEvent_Idempotent_OutboxEvent pins the
// re-enqueue contract: two CommitClipAndIndexEvent calls with the
// same clipID must produce EXACTLY ONE outbox_events row (event_key
// UNIQUE → ON CONFLICT DO NOTHING collapses the duplicate). The
// media_assets row gets updated (UpsertClipTx is INSERT…ON CONFLICT
// DO UPDATE) but the outbox row count stays 1 — the contract is the
// idempotent-enqueue guarantee production callers rely on for
// safe retry.
func TestCommitClipAndIndexEvent_Idempotent_OutboxEvent(t *testing.T) {
	db, clips, txMgr := openTestDB(t)
	w := NewClipAtomicWriter(clips, txMgr, zap.NewNop())

	const clipID = "yt_xxxxxxxxxxx_5_15_v1"
	item := makeItem(clipID)
	event := makeEvent(clipID)
	ctx := context.Background()

	require.NoError(t, w.CommitClipAndIndexEvent(ctx, clipID, item, event))
	require.NoError(t, w.CommitClipAndIndexEvent(ctx, clipID, item, event),
		"second call must succeed via ON CONFLICT DO NOTHING path")

	var eventCount int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM outbox_events WHERE event_key = ?", clipID).Scan(&eventCount))
	assert.Equal(t, 1, eventCount,
		"second call must NOT duplicate outbox_events row (event_key UNIQUE + ON CONFLICT DO NOTHING)")

	// media_assets gets updated but the row count stays 1 (no duplicate).
	var mediaCount int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_assets WHERE id = ?", clipID).Scan(&mediaCount))
	assert.Equal(t, 1, mediaCount, "UpsertClipTx ON CONFLICT keeps media_assets row count = 1")
}

// TestCommitClipAndIndexEvent_ValidationErrors pins the input-
// validation contract: clipID="" and event.AggregateID != clipID
// both surface typed errors verbatim. The parent's retry logic
// branches on errors.Is(err, ErrClipWriterNil) etc. without
// substring parsing — the test confirms each branch matches.
func TestCommitClipAndIndexEvent_ValidationErrors(t *testing.T) {
	db, clips, txMgr := openTestDB(t)
	w := NewClipAtomicWriter(clips, txMgr, zap.NewNop())
	ctx := context.Background()

	t.Run("empty clipID returns ErrClipWriterNil", func(t *testing.T) {
		err := w.CommitClipAndIndexEvent(ctx, "", makeItem(""), makeEvent(""))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrClipWriterNil),
			"empty clipID must surface ErrClipWriterNil verbatim, got: %v", err)
	})

	t.Run("mismatched AggregateID returns ErrClipWriterEventInvalid", func(t *testing.T) {
		err := w.CommitClipAndIndexEvent(ctx, "yt_clip_1", makeItem("yt_clip_1"),
			(youtubeports.IndexEventPayload{
				Type:        "asset.index.requested",
				AggregateID: "yt_some_other_clip", // deliberately mismatched
				Payload:     []byte(`{}`),
				CreatedAt:   time.Now().UTC(),
			}))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrClipWriterEventInvalid),
			"mismatched AggregateID must surface ErrClipWriterEventInvalid, got: %v", err)
	})

	t.Run("empty event.Type returns ErrClipWriterEventInvalid", func(t *testing.T) {
		err := w.CommitClipAndIndexEvent(ctx, "yt_clip_2", makeItem("yt_clip_2"),
			(youtubeports.IndexEventPayload{
				Type:        "", // deliberately empty
				AggregateID: "yt_clip_2",
				Payload:     []byte(`{}`),
				CreatedAt:   time.Now().UTC(),
			}))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrClipWriterEventInvalid),
			"empty event.Type must surface ErrClipWriterEventInvalid, got: %v", err)
	})

	// Sanity: after all the failed-validations, no rows leaked into
	// either table — the writer never writes without passing input checks.
	var mediaCount, eventCount int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets").Scan(&mediaCount))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox_events").Scan(&eventCount))
	assert.Equal(t, 0, mediaCount, "validation failures must NOT leak media_assets rows")
	assert.Equal(t, 0, eventCount, "validation failures must NOT leak outbox_events rows")
}
