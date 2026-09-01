// Package sqlite — pool_test.go (FASE 6 Cut 6.2, July 2026).
//
// Minimal regression tests for the canonical DualPool surface
// (internal/platform/sqlite/pool.go) + walPragmas
// enrichment + classifyTxError busy-counter surface.

package sqlite

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// collectHistogramSampleCount reads Histogram.SampleCount from a
// prometheus.Observer handle. Used by the histogram-observation
// assertions below.
//
// Rationale (Cut 6.2 coder-review feedback, July 2026): testutil.CollectAndCount
// returns the count of distinct labelsets, NOT observation count.
// For a Histogram observation, the canonical "exactly N
// observations" assertion must read SampleCount from the metric
// protobuf — not query the vec's labelset count.
//
// godlike/06 SSOT: this is the canonical accessor for histogram
// sample-count assertions in the sqlite package. Other tests in
// the package MUST use this helper (NOT inline dto.Metric +
// prometheus.Histogram casts), to keep the assertion surface DRY.
func collectHistogramSampleCount(observer prometheus.Observer) uint64 {
	h, ok := observer.(prometheus.Histogram)
	if !ok {
		return 0
	}
	pbMetric := &dto.Metric{}
	if err := h.Write(pbMetric); err != nil {
		return 0
	}
	return pbMetric.GetHistogram().GetSampleCount()
}

// ── DualPool construction ────────────────────────────────────────────────

// TestNewDualPool_WALModeApplied pins the load-bearing pragma
// invariant: NewDualPool's enriched URI must include journal_mode(WAL).
func TestNewDualPool_WALModeApplied(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "cut62_wal.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, tmpFile, 2)
	require.NoError(t, err)
	defer pool.Close()
	require.NotNil(t, pool.Writer)
	require.NotNil(t, pool.Reader)

	var journalMode string
	require.NoError(t, pool.Writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode))
	assert.Equal(t, "wal", strings.ToLower(journalMode),
		"DualPool writer must report journal_mode=wal (canonical Cut 6.2 invariant); got %q", journalMode)

	var busyTimeout int
	require.NoError(t, pool.Writer.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout))
	assert.Equal(t, 5000, busyTimeout,
		"DualPool writer must apply busy_timeout=5000 (canonical Cut 6.2 invariant)")
}

// TestNewDualPool_MaxOpenConns_PerPool pins the per-pool sizing
// invariant: writer MaxOpenConns=1, reader MaxOpenConns=N (N>=2).
func TestNewDualPool_MaxOpenConns_PerPool(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "cut62_conns.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const numReaders = 3
	pool, err := NewDualPool(ctx, tmpFile, numReaders)
	require.NoError(t, err)
	defer pool.Close()

	assert.Equal(t, 1, pool.Writer.Stats().MaxOpenConnections,
		"writer pool MaxOpenConnections MUST stay at 1 (canonical single-writer)")
	assert.Equal(t, numReaders, pool.Reader.Stats().MaxOpenConnections,
		"reader pool MaxOpenConnections MUST equal numReaders argument (canonical concurrent-reader affordance)")
	assert.NotSame(t, pool.Writer, pool.Reader,
		"writer and reader must be independent sql.DB pools")
}

func TestAttachDualPoolIsNotUsedForProductionReaderIsolation(t *testing.T) {
	primary, err := NewSQLiteDB(t.TempDir(), "media.db.sqlite", zap.NewNop())
	require.NoError(t, err)
	defer primary.Close()

	pool, err := NewDualPool(context.Background(), primary.Path(), 2)
	require.NoError(t, err)
	defer pool.Close()

	assert.NotSame(t, primary.DB, pool.Reader,
		"production dual pool reader must not alias DatabaseSet.Primary")
	assert.NotSame(t, primary.DB, pool.Writer,
		"production dual pool writer must not alias DatabaseSet.Primary")
}

// TestNewDualPool_EmptyURIFailsClosed pins the godlike/07 contract.
func TestNewDualPool_EmptyURIFailsClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, "", 2)
	require.Error(t, err)
	assert.Nil(t, pool)
	assert.Contains(t, err.Error(), "empty fileUri",
		"empty fileUri MUST report 'empty fileUri' (canonical fail-closed message)")
}

// ── walPragmas enrichment ────────────────────────────────────────────────

// TestWalPragmas_PreservesAllThreePragmas: url.Values.Set on the same
// key REPLACES, url.Values.Add APPENDS. Set 3x wastes 2 pragmas; Add
// 3x preserves all of them in the encoded URI.
//
// AUDIT (PR-CANONICAL-WAL-PRAGMAS-ENCODE, July 2026): walPragmas() uses
// the canonical Go URL composer url.Values.Encode() which percent-
// encodes reserved characters — the parens in `_pragma=journal_mode(WAL)`
// surface as `_pragma=journal_mode%28WAL%29` in the encoded URI. The
// driver (mattn/go-sqlite3) correctly decodes %28/%29 to ( ) at runtime
// (proven by sibling test TestNewDualPool_WALModeApplied which queries
// PRAGMA journal_mode directly). So the assertion target is the
// UNESCAPED form (matches the canonical human-readable intent of the
// pragmas); the encoded form is the wire-shape, not the assertion
// contract. ARCH-ALLOWLISTED: issues.yaml::PRE-EXISTING-1 follow_up
// bullet 3 (assertion fix surface; production is canonical godlike/06
// SSOT go URL composer).
func TestWalPragmas_PreservesAllThreePragmas(t *testing.T) {
	uri := "/tmp/cut62_wal_pragmas.db"
	out, err := walPragmas(uri)
	require.NoError(t, err)

	// Unescape the encoded URI so the assertions operate on the
	// canonical pragma form. _busy_timeout=5000 has no reserved
	// chars so unescape is a no-op for that segment — the
	// back-compat check below still passes identically.
	unescapedOut, err := url.QueryUnescape(out)
	require.NoError(t, err, "url.QueryUnescape must accept walPragmas output verbatim (godlike/07 fail-closed contract)")

	assert.Contains(t, unescapedOut, "_pragma=journal_mode(WAL)",
		"WAL pragma MUST survive encoding (canonical concurrent-reader affordance)")
	assert.Contains(t, unescapedOut, "_pragma=busy_timeout(5000)",
		"busy_timeout pragma MUST survive encoding (canonical retry.IsTransient ErrBusy loop)")
	assert.Contains(t, unescapedOut, "_pragma=synchronous(NORMAL)",
		"synchronous pragma MUST survive encoding (WAL-safe durability)")
	assert.Contains(t, unescapedOut, "_busy_timeout=5000",
		"_busy_timeout parameter MUST survive encoding (legacy driver-level pendant)")

	// Count=1 invariant preserved across the unescape: each pragma
	// was added exactly once via q.Add in walPragmas(); url.Values.Encode
	// rewrites the form but never collapses or duplicates entries.
	assert.Equal(t, 1, strings.Count(unescapedOut, "_pragma=journal_mode(WAL)"),
		"journal_mode pragma MUST appear exactly once")
	assert.Equal(t, 1, strings.Count(unescapedOut, "_pragma=busy_timeout(5000)"),
		"busy_timeout pragma MUST appear exactly once")
	assert.Equal(t, 1, strings.Count(unescapedOut, "_pragma=synchronous(NORMAL)"),
		"synchronous pragma MUST appear exactly once")
}

// ── classifyTxError busy-counter surface ────────────────────────────────

// TestClassifyTxError_IncrementsBusyTotal_Once: a typed *sqlite3.Error
// with Code==ErrBusy or ErrLocked increments sqlite_busy_total{op}
// EXACTLY ONCE per call. Non-busy errors do NOT increment.
func TestClassifyTxError_IncrementsBusyTotal_Once(t *testing.T) {
	busyErr := &sqlite3.Error{Code: sqlite3.ErrBusy}
	lockedErr := &sqlite3.Error{Code: sqlite3.ErrLocked}
	otherErr := &sqlite3.Error{Code: sqlite3.ErrFull}
	plainErr := errors.New("untyped error")

	preWriter := testutil.ToFloat64(observability.SqliteBusyTotal.WithLabelValues("writer"))
	preReader := testutil.ToFloat64(observability.SqliteBusyTotal.WithLabelValues("reader"))

	classifyTxError("writer", busyErr)
	classifyTxError("writer", lockedErr)
	classifyTxError("writer", otherErr) // NOT a busy error
	classifyTxError("writer", plainErr) // NOT a typed err
	classifyTxError("reader", busyErr)

	postWriter := testutil.ToFloat64(observability.SqliteBusyTotal.WithLabelValues("writer"))
	postReader := testutil.ToFloat64(observability.SqliteBusyTotal.WithLabelValues("reader"))

	// writer: 2 increments (ErrBusy + ErrLocked).
	assert.Equal(t, preWriter+2, postWriter,
		"classifyTxError MUST increment sqlite_busy_total{op=writer} on ErrBusy AND ErrLocked (not on ErrFull or untyped errors)")
	// reader: 1 increment (ErrBusy only).
	assert.Equal(t, preReader+1, postReader,
		"classifyTxError MUST increment sqlite_busy_total{op=reader} on ErrBusy")
}

// ── instrumentedTx Commit/Rollback observation ──────────────────────────

// TestInstrumentedTx_CommitObservesOkOutcome: a successful Commit
// observes tx_duration_seconds{op=writer, outcome=ok} exactly once.
// Uses collectHistogramSampleCount (NOT testutil.CollectAndCount —
// that wraps the labelset count, not observation count).
func TestInstrumentedTx_CommitObservesOkOutcome(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "cut62_commit.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, tmpFile, 2)
	require.NoError(t, err)
	defer pool.Close()

	preOK := collectHistogramSampleCount(observability.TxDurationSeconds.WithLabelValues("writer", "ok"))

	tx, err := pool.BeginWriterTx(ctx)
	require.NoError(t, err)

	// A no-op commit (no SQL between BeginTx and Commit) is the
	// canonical "ultra-short tx" happy path.
	require.NoError(t, tx.Commit())

	postOK := collectHistogramSampleCount(observability.TxDurationSeconds.WithLabelValues("writer", "ok"))

	assert.Equal(t, preOK+1, postOK,
		"successful Commit MUST observe tx_duration_seconds{op=writer, outcome=ok} exactly once")
}

// TestInstrumentedTx_RollbackObservesErrOutcome: a Rollback observes
// tx_duration_seconds{op=writer, outcome=err} exactly once.
func TestInstrumentedTx_RollbackObservesErrOutcome(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "cut62_rollback.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := NewDualPool(ctx, tmpFile, 2)
	require.NoError(t, err)
	defer pool.Close()

	preErr := collectHistogramSampleCount(observability.TxDurationSeconds.WithLabelValues("writer", "err"))

	tx, err := pool.BeginWriterTx(ctx)
	require.NoError(t, err)

	// Explicit rollback. Subsequent commit returns sql.ErrTxDone
	// (a benign sentinel — the type assertion in Rollback ignores
	// it from the busy-counter surface).
	require.NoError(t, tx.Rollback())

	postErr := collectHistogramSampleCount(observability.TxDurationSeconds.WithLabelValues("writer", "err"))

	assert.Equal(t, preErr+1, postErr,
		"rollback MUST observe tx_duration_seconds{op=writer, outcome=err} exactly once")
}
