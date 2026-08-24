// Package qdrantprojection — BatchReader regression test (PR 8).
//
// Verifies the keyset-pagination contract documented in batch_reader.go:
//
//   - Next(ctx) returns ([]AssetRow, lastID, err). Caller MUST check err
//     before reading rows (per the source contract).
//   - EOF is returned as (nil, "", io.EOF) — clean iteration end.
//   - (nil, "", ErrBatchReaderClosed) signals that Close() was called
//     early — distinct from io.EOF so callers can distinguish a crashed
//     worker from a finished one.
//   - (rows, lastID, nil) is the happy path. lastID is the LAST id of
//     the returned batch and is the cursor to pass into
//     NewBatchReaderFromCheckpoint for resume.
//
// We deliberately use the production media_assets PRIMARY KEY shape
// (id TEXT NOT NULL, lex ascending) so a regression here is the same
// regression a reindex job would hit in production.
package qdrantprojection

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver registration for :memory:
)

// memDB is a thin test fixture: an in-memory SQLite database pre-loaded
// with the production-replica media_assets schema (id + content_hash +
// source + name) so SELECTs against it succeed. We avoid the canonical
// storage.OpenSQLiteDB (WAL/busy_timeout/foreign-keys) here because
// those don't apply to in-memory testing.
type memDB struct {
	db *sql.DB
}

func newMemDB(t *testing.T) *memDB {
	t.Helper()
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open :memory: db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	// Production-schema mirror: the BatchReader.Next SELECT projects
	// (id, metadata_json, source) and filters by
	// `lifecycle_state IN ('ready', 'active')`. The test schema must
	// include ALL four columns or the production SELECT fails with
	// "no such column: metadata_json" inside the test fixture.
	if _, err := d.Exec(`
CREATE TABLE media_assets (
    id              TEXT PRIMARY KEY,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    source          TEXT NOT NULL DEFAULT '',
    lifecycle_state TEXT NOT NULL DEFAULT 'ready',
    filename TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    index_state TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
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
    origin TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    namespace TEXT NOT NULL DEFAULT '',
    asset_kind TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    semantic_role TEXT NOT NULL DEFAULT '',
    drive_folder_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '')
`); err != nil {
		t.Fatalf("create media_assets schema: %v", err)
	}
	return &memDB{db: d}
}

// padID converts an integer 1..N into a zero-padded 6-digit TEXT id so
// lexicographic order matches numeric order. Deterministic — needed
// for the lex-ascending assertion in the boundary tests.
func padID(n int) string {
	const width = 6
	s := fmt.Sprintf("%d", n)
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}

// hexUUID returns a 32-char hex UUID suitable for use as a media_assets.id.
// We seed with random hex because production asset IDs are random strings
// and the keyset cursor must survive randomly-distributed TEXT keys.
func hexUUID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("crypto/rand: %v", err)
	}
	return hex.EncodeToString(b)
}

func (m *memDB) seedNumeric(t *testing.T, n int) []string {
	t.Helper()
	return m.seed(t, padIDRange(1, n+1))
}

func (m *memDB) seedUUIDs(t *testing.T, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = hexUUID(t)
	}
	return m.seed(t, ids)
}

func (m *memDB) seed(t *testing.T, ids []string) []string {
	t.Helper()
	// Iterate without a transaction; SQLite `:memory:` has no
	// concurrency on a single test, so the tx Begin/Commit pair is
	// pure overhead.
	stmt, err := m.db.Prepare(`INSERT INTO media_assets(id, source) VALUES(?, ?)`)
	if err != nil {
		t.Fatalf("prepare insert: %v", err)
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.Exec(id, "source_"+id); err != nil {
			t.Fatalf("insert id=%s: %v", id, err)
		}
	}
	return ids
}

func padIDRange(lo, hi int) []string {
	out := make([]string, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, padID(i))
	}
	return out
}

// ── Tests ──────────────────────────────────────────────────────────────

// TestBatchReader_FreshIterationNumericIDsContract asserts the primary
// contract: cursor="" → emits rows in deterministic lex order, EOF
// returns io.EOF, the cursor returned is the LAST id of the batch.
func TestBatchReader_FreshIterationNumericIDsContract(t *testing.T) {
	const (
		total    = 26
		batchSz  = 7
		expected = 4 // 26 / 7 = 3 full + 1 partial (5 rows)
	)
	mem := newMemDB(t)
	want := mem.seedNumeric(t, total)
	wantSorted := append([]string{}, want...)
	sort.Strings(wantSorted)

	r := NewBatchReader(mem.db, batchSz)

	batchN := 0
	var got []string
	for {
		rows, lastID, err := r.Next(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				break // clean iteration end
			}
			t.Fatalf("Next iteration %d: %v", batchN, err)
		}
		if len(rows) == 0 {
			t.Fatalf("Next iteration %d returned err=nil but 0 rows", batchN)
		}
		batchN++

		// Cursor invariant: lastID == rows[len-1].ID.
		if lastID != rows[len(rows)-1].ID {
			t.Fatalf("iter %d: lastID=%q != rows[-1].ID=%q",
				batchN, lastID, rows[len(rows)-1].ID)
		}
		for _, row := range rows {
			got = append(got, row.ID)
		}
	}
	if batchN != expected {
		t.Fatalf("expected %d batches, got %d (one-too-many or stuck batch)", expected, batchN)
	}
	if len(got) != total {
		t.Fatalf("iterated %d IDs, want %d", len(got), total)
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("yielded IDs are not lex-ascending")
	}
	for i, id := range got {
		if id != wantSorted[i] {
			t.Fatalf("index %d: got %s want %s", i, id, wantSorted[i])
		}
	}
}

// TestBatchReader_ResumeAfterCursorDoesNotReRead mirrors the production
// checkpoint-resume path: read 2 batches of 10, capture cursor, then
// resume with NewBatchReaderFromCheckpoint and drain. The union must be
// exactly the seeded set, with no duplicates and no gaps.
func TestBatchReader_ResumeAfterCursorDoesNotReRead(t *testing.T) {
	const total = 54
	mem := newMemDB(t)
	mem.seedUUIDs(t, total) // random hex IDs (production shape)

	// First 2 batches with a fresh reader.
	r := NewBatchReader(mem.db, 10)

	var pre []string
	var crashCursor string
	for i := 0; i < 2; i++ {
		rows, lastID, err := r.Next(context.Background())
		if err != nil {
			t.Fatalf("pre-crash batch %d: %v", i, err)
		}
		if len(rows) == 0 {
			t.Fatalf("pre-crash batch %d unexpectedly empty", i)
		}
		for _, row := range rows {
			pre = append(pre, row.ID)
		}
		crashCursor = lastID
		if crashCursor == "" {
			t.Fatalf("pre-crash batch %d returned empty cursor", i)
		}
	}

	// Resume via the production constructor.
	rResumed := NewBatchReaderFromCheckpoint(mem.db, 10, crashCursor)

	var post []string
	for {
		rows, _, err := rResumed.Next(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("post-resume: %v", err)
		}
		for _, row := range rows {
			post = append(post, row.ID)
		}
	}

	seen := make(map[string]int, total)
	for _, id := range pre {
		seen[id]++
	}
	for _, id := range post {
		seen[id]++
	}
	if len(seen) != total {
		t.Fatalf("seen set size %d, want %d (gap or duplicate)", len(seen), total)
	}
	for id, c := range seen {
		if c > 1 {
			t.Fatalf("id %s seen %d times across resume", id, c)
		}
	}
	// Resume must NOT include any pre-crash id (cursor hard cut).
	for _, pid := range pre {
		for _, qid := range post {
			if pid == qid {
				t.Fatalf("id %s appears in both pre and post — resume duplicated", pid)
			}
		}
	}
}

// TestBatchReader_OneRowEOF exercises the off-by-one: the last batch
// has exactly 1 row, Next must emit it AND return io.EOF on the
// subsequent call.
func TestBatchReader_OneRowEOF(t *testing.T) {
	const total = 11
	mem := newMemDB(t)
	mem.seedNumeric(t, total)

	r := NewBatchReader(mem.db, 10)

	rows, lastID, err := r.Next(context.Background())
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("first batch len %d, want 10", len(rows))
	}
	if lastID == "" {
		t.Fatalf("first batch returned empty cursor prematurely")
	}

	rResume := NewBatchReaderFromCheckpoint(mem.db, 10, lastID)
	lastRows, _, err := rResume.Next(context.Background())
	if err != nil {
		t.Fatalf("last batch: %v", err)
	}
	if len(lastRows) != 1 {
		t.Fatalf("last batch len %d, want 1", len(lastRows))
	}

	// Next call must return io.EOF — the iteration contract.
	_, _, err = rResume.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("after last batch, expected io.EOF, got %v", err)
	}
}
