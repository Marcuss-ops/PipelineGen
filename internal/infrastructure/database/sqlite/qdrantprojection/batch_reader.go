// Package qdrantprojection — SQLite-resident projection read/write
// helpers for the v2 reindex pipeline.
//
// PR 8 (June 2026, feat/qdrant-reindex-v2) — verdict section #15.
//
// Purpose of this package:
//   - Memory-bounded iteration over media_assets (keyset pagination).
//   - Per-job checkpoint persistence for resume-after-crash safety.
//   - Per-doc DLQ persistence for validation failure triage.
//
// Invariants enforced here:
//   - ZERO inferred global state. Every helper takes an explicit
//     *sql.DB / context pair so test fixtures and the production
//     wiring share the same code path.
//   - Cursor semantics: after a successful Next(), cursor advances
//     to the LAST id in the returned batch. EOF is signalled by
//     Next() returning (nil, io.EOF) so callers can `errors.Is(err,
//     io.EOF)` against the canonical sentinel.
//   - The Postgres/SQLite `> ` comparison on a TEXT pk is well-defined
//     when the column is a UUIDv5 / hex-ish digest (PipelineGen's
//     canonical asset_id form). Mixed-width identifiers sort
//     lexicographically — for our purposes, mostly-uniform.
//     Compatibility note: the previous in-memory ListAllAssetIDs
//     used DISORDER BY id stable; the new keyset uses ORDER BY id
//     ASC for the SELECT side only. Same canonical ordering in
//     practice (the PK is the iteration axis in both).
package qdrantprojection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
)

// ── BatchReader — keyset-paginated read of media_assets ──────────────

// BatchReader is the canonical keyset-paginated iterator over
// media_assets. It replaces the previous ListAllAssetIDs in-memory
// load (qdrant/asset_store.go) which 100% OOM'd on fleets > ~50k rows.
//
// Lifecycle: construct via NewBatchReader, iter via Next, close via
// the io.Closer embedded on the returned reader. EOF is returned from
// Next as nil row + io.EOF (callers MUST errors.Is(err, io.EOF) —
// checking err == io.EOF returns false because the wrapping copy is
// produced by fmt.Errorf in some return paths).
//
// Bounded memory: the reader holds ONE row slice in memory at a time
// (max batchSize rows). A 5,000,000-row fleet reads in 5,000 batches
// of 1,000 — never holding the entire fleet at once.
type BatchReader struct {
	db        *sql.DB
	batchSize int

	// cursor advances on every successful non-empty Next() call.
	// Empty string at iteration start = "from the beginning".
	cursor string

	// closed stops the iterator; subsequent Next calls return
	// (nil, ErrBatchReaderClosed).
	closed bool
}

// ErrBatchReaderClosed is returned by Next when the reader was
// already closed via Close. Lets callers distinguish "premature close"
// from "iteration finished" (io.EOF).
var ErrBatchReaderClosed = errors.New("qdrantprojection.BatchReader: closed")

// NewBatchReader constructs a reader primed at the beginning of
// media_assets. Use NewBatchReaderFromCheckpoint to resume an in-flight
// or interrupted job.
//
// Panics on db==nil (the canonical panic-on-nil-required-port shape
// mirrors qdrant.NewCollectionManager).
func NewBatchReader(db *sql.DB, batchSize int) *BatchReader {
	if db == nil {
		panic("qdrantprojection.NewBatchReader: db must not be nil")
	}
	if batchSize <= 0 {
		batchSize = 500 // canonical floor; matches the reconciler's batch.
	}
	return &BatchReader{db: db, batchSize: batchSize}
}

// NewBatchReaderFromCheckpoint resumes iteration at lastID. The `0`
// (empty) value means "from the beginning" (fresh job).
func NewBatchReaderFromCheckpoint(db *sql.DB, batchSize int, lastID string) *BatchReader {
	r := NewBatchReader(db, batchSize)
	r.cursor = lastID
	return r
}

// AssetRow is the SELECT projection for a single media_assets row.
// Mirrors the qdrant.IndexWriter.BuildPayload input contract so the
// downstream upsert code in index_writer.go needs NO changes between
// v1 and v2 — only the upstream caller is different.
//
// Lifecycle fields are intentionally omitted: the v2 reindex
// pre-filters on lifecycle_state IN ('ready','active') so the caller
// reads a uniform-ready set. Operators wishing "all rows including
// archived" pass a custom SELECT below.
type AssetRow struct {
	ID          string
	ContentHash string
	Source      string
	Name        string
}

// Next returns the next batch of rows plus the cursor value the caller
// SHOULD persist to qdrantprojection_checkpoints.last_indexed_id on
// commit success. The cursor returned is the LAST id of the returned
// batch (empty if batch is nil = EOF).
//
// EOF semantics:
//   - (nil, "", io.EOF)            : clean iteration end
//   - (nil, "", ErrBatchReaderClosed) : Close was called early
//   - (rows, lastID, nil)          : at least one row returned
//   - (nil, "", err)              : query error (transport, schema)
//
// Caller must check err before reading rows; the returned row slice is
// nil when err is non-nil.
func (r *BatchReader) Next(ctx context.Context) ([]AssetRow, string, error) {
	if r.closed {
		return nil, "", ErrBatchReaderClosed
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id, metadata_json, source
FROM media_assets
WHERE id > ?
  AND lifecycle_state IN ('ready', 'active')
ORDER BY id ASC
LIMIT ?
`, r.cursor, r.batchSize)
	if err != nil {
		return nil, "", fmt.Errorf("qdrantprojection.BatchReader.Next: query: %w", err)
	}
	defer rows.Close()

	out := make([]AssetRow, 0, r.batchSize)
	for rows.Next() {
		var (
			id      string
			metaRaw sql.NullString
			src     sql.NullString
		)
		if err := rows.Scan(&id, &metaRaw, &src); err != nil {
			return nil, "", fmt.Errorf("qdrantprojection.BatchReader.Next: scan: %w", err)
		}
		var (
			hash string
			name string
		)
		// media_assets.metadata_json is the operator's free-form
		// JSON bag. content_hash lives at .$.content_hash. We do
		// NOT decode the whole meta here (a single batch can hold
		// 1000 rows and JSON-decoding all of them is expensive);
		// instead we extract the two slots the v2 reindex needs.
		// Falsy when absent.
		if metaRaw.Valid && metaRaw.String != "" {
			hash = extractContentHash(metaRaw.String)
			name = extractName(metaRaw.String)
		}
		out = append(out, AssetRow{
			ID:          id,
			ContentHash: hash,
			Source:      src.String,
			Name:        name,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("qdrantprojection.BatchReader.Next: rows iter: %w", err)
	}

	// EOF: no rows returned AND cursor was meaningful — we've
	// already read the full set above this slice.
	if len(out) == 0 {
		return nil, "", io.EOF
	}

	// Advance cursor to the LAST id in the batch so resume picks up
	// WHERE this batch left off (strict `>` comparison).
	r.cursor = out[len(out)-1].ID
	return out, r.cursor, nil
}

// Cursor returns the reader's current cursor value. Exposed so callers
// may PEEK at the cursor without invoking Next — primarily for tests
// that want to assert in-flight state without consuming a batch.
func (r *BatchReader) Cursor() string { return r.cursor }

// Close idempotently closes the iterator. After Close, Next returns
// ErrBatchReaderClosed.
func (r *BatchReader) Close() error {
	r.closed = true
	return nil
}

// ── Inline JSON extraction helpers (shared with index_writer_v2.go) ──

// extractContentHash pulls media_assets.metadata_json.$.content_hash.
// Returns "" when the key is absent or the JSON is malformed. Naive
// string scan (NOT full json.Unmarshal) — the reindex hot path
// prioritises throughput over regex-fidelity, and the canonical
// write path always emits `content_hash:"..."` as a plain string
// quoted value.
//
// A future correctness pass should swap to json.Unmarshal + struct
// projection; PR 8 ships the cheap path.
func extractContentHash(raw string) string {
	if raw == "" {
		return ""
	}
	const k = `"content_hash":`
	idx := indexOf(raw, k)
	if idx < 0 {
		return ""
	}
	tail := raw[idx+len(k):]
	tail = trimLeft(tail, " \t")
	if len(tail) == 0 || tail[0] != '"' {
		return ""
	}
	end := indexOf(tail[1:], `"`)
	if end < 0 {
		return ""
	}
	return tail[1 : 1+end]
}

// extractName mirrors extractContentHash for the .$.name slot.
func extractName(raw string) string {
	if raw == "" {
		return ""
	}
	const k = `"name":`
	idx := indexOf(raw, k)
	if idx < 0 {
		return ""
	}
	tail := raw[idx+len(k):]
	tail = trimLeft(tail, " \t")
	if len(tail) == 0 || tail[0] != '"' {
		return ""
	}
	end := indexOf(tail[1:], `"`)
	if end < 0 {
		return ""
	}
	return tail[1 : 1+end]
}

// indexOf + trimLeft are tiny stdlib replacements to keep this file
// zero-import beyond database/sql + errors + fmt + io. Without
// them we'd add strings for two single-call sites.
func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimLeft(s, cutset string) string {
	for len(s) > 0 && indexOf(cutset, string(s[0])) >= 0 {
		s = s[1:]
	}
	return s
}
