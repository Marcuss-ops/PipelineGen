package rendermetrics

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func mustDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

const schema = `
CREATE TABLE render_attempt_analytics (
    attempt_id      TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    phrase_count    INTEGER NOT NULL DEFAULT 0,
    word_count      INTEGER NOT NULL DEFAULT 0,
    image_count     INTEGER NOT NULL DEFAULT 0,
    leak_count      INTEGER NOT NULL DEFAULT 0,
    render_ms       INTEGER NOT NULL DEFAULT 0,
    encode_ms       INTEGER NOT NULL DEFAULT 0,
    width           INTEGER NOT NULL DEFAULT 0,
    height          INTEGER NOT NULL DEFAULT 0,
    fps_num         INTEGER NOT NULL DEFAULT 0,
    fps_den         INTEGER NOT NULL DEFAULT 0,
    frame_count     INTEGER NOT NULL DEFAULT 0,
    duration_us     INTEGER NOT NULL DEFAULT 0,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    sha256          TEXT NOT NULL DEFAULT '',
    drive_file_id   TEXT NOT NULL DEFAULT '',
    drive_link      TEXT NOT NULL DEFAULT '',
    recorded_at     TEXT NOT NULL
);`

// TestRecordAttemptUpsertsIdempotently pins the idempotency key contract:
// recording the same attempt_id twice converges on one row (updated, not
// duplicated).
func TestRecordAttemptUpsertsIdempotently(t *testing.T) {
	db := mustDB(t)
	reg, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	a := scriptgen.RenderAttemptAnalytics{
		AttemptID: "attempt-1",
		JobID:     "job-1",
		Content:   capoverlay.ContentCounts{Phrases: 1, Words: 2, Images: 3, Leaks: 4},
		RenderMS:  100,
		EncodeMS:  50,
		SHA256:    "sha-1",
	}
	if err := reg.RecordAttempt(ctx, a); err != nil {
		t.Fatal(err)
	}
	// Re-record with updated facts: same attempt_id, different sha256.
	a.SHA256 = "sha-2"
	if err := reg.RecordAttempt(ctx, a); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM render_attempt_analytics`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1 (upsert keyed by attempt_id)", count)
	}
	var gotSHA string
	var phrases, words, images, leaks, renderMS, encodeMS int
	if err := db.QueryRow(`SELECT sha256, phrase_count, word_count, image_count, leak_count, render_ms, encode_ms FROM render_attempt_analytics WHERE attempt_id='attempt-1'`).
		Scan(&gotSHA, &phrases, &words, &images, &leaks, &renderMS, &encodeMS); err != nil {
		t.Fatal(err)
	}
	if gotSHA != "sha-2" || phrases != 1 || words != 2 || images != 3 || leaks != 4 || renderMS != 100 || encodeMS != 50 {
		t.Fatalf("row = sha=%s counts=%d/%d/%d/%d render=%d encode=%d", gotSHA, phrases, words, images, leaks, renderMS, encodeMS)
	}
}

// TestRecordAttemptRequiresAttemptID pins the fail-closed contract: a record
// without an attempt_id is rejected, never silently persisted.
func TestRecordAttemptRequiresAttemptID(t *testing.T) {
	db := mustDB(t)
	reg, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordAttempt(context.Background(), scriptgen.RenderAttemptAnalytics{}); err == nil {
		t.Fatal("empty attempt_id must fail closed")
	}
}

// TestNewRejectsNilDB pins the adapter constructor contract.
func TestNewRejectsNilDB(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil db must be rejected")
	}
}
