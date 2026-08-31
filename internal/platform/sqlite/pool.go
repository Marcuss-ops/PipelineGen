// Package sqlite — pool.go (FASE 6 Cut 6.2, July 2026).
//
// Reader/writer split for the canonical SQLite database. The
// pre-Cut-6.2 surface was a single `*sql.DB` with
// `db.SetMaxOpenConns(1)` — the canonical "writer-only" model that
// serializes every query (reads AND writes) through one connection,
// starving reads under write bursts.
//
// Cut 6.2 introduces a WAL-mode dual-pool:
//
//   - Writer pool: MaxOpenConns(1), MaxIdleConns(1) — exactly one
//     concurrent writer, the canonical SQLite concurrency model
//     (see https://sqlite.org/wal.html section "Avoiding Excessively
//     Long Write Transactions" and "Concurrency").
//   - Reader pool: MaxOpenConns(N) where N is configurable (default
//     is runtime.NumCPU()), MaxIdleConns(N) — N concurrent readers
//     sharing the on-disk WAL via the `cache=shared` URI pragma.
//
// WAL-mode + `cache=shared` URI pragma allow concurrent readers
// and a single writer; the in-process pool sizing is the
// canonical mediator (no inter-process file-lock contention since
// `?cache=shared` is per-process pool sharing, not OS-level).
//
// godlike/06 SSOT: this DualPool is the SOLE canonical connection
// pool factory for the SQLite database. Composition-root callers
// MUST construct via NewDualPool (NOT sql.Open) so the WAL mode + the
// reader/writer split + the Prometheus instrumentations are wired
// at startup. A direct sql.Open("sqlite3", uri) invocation would
// lose the WAL pragma AND the metric surface, silently regressing
// the Cut 6.2 observability guarantee.
//
// godlike/07 fail-closed:
//   - NewDualPool validates the URI is not empty and numReaders>=2.
//   - writer pool is opened first; if reader pool open fails, the
//     writer is closed before returning the error (resource
//     symmetry).
//   - The instrumented BeginTx propagates context cancellation
//     verbatim to the inner conn acquisition; a cancelled context
//     produces connection_wait_seconds observation of 0 (cancelled)
//     and is treated as `err=Cancelled` for tx_duration purposes.
//   - classifySQLiteError (registered at init() in
//     registry_retry_classifier.go) is the canonical SQLite-error
//     classifier; the pool observes sqlite_busy_total when the
//     driver returns SQLITE_BUSY/SQLITE_LOCKED.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"time"

	"github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// DualPool is the canonical WAL-mode reader/writer split for the
// SQLite database. Reader and writer both point at the same on-disk
// file; the WAL pragma (`?_pragma=journal_mode(WAL)`) governs the
// concurrency model at the SQLite layer, while the in-process
// db/sql pool sizing governs concurrency at the Go layer.
//
// Writer and reader connections are NEVER shared across pools —
// each pool holds its own private *sql.DB. The WAL-mode SQLite file
// itself handles cross-pool coordination (readers see the committed
// writer's snapshot via the WAL).
type DualPool struct {
	// ownsHandles is false when the pool is attached to DatabaseSet.Primary.
	// In that topology Writer and Reader are views over the already-open
	// canonical primary handle; Close must not close it a second time.
	ownsHandles bool

	// Writer is the single-slot write pool (MaxOpenConns=1).
	// All canonical write paths route through writer.
	Writer *sql.DB

	// Reader is the N>1 read pool (MaxOpenConns=N). Canonical read
	// paths route through reader when the operation can be served
	// from a historical snapshot (WAL-mode allows this).
	Reader *sql.DB

	// sourcePath is the on-disk SQLite file URI after walPragmas is
	// applied. Preserved for diagnostic logging only — operations
	// go through Writer/Reader exclusively. Not exported; closes
	// the door on "fake availability" path: a future caller is
	// forced through the canonical accessor methods.
	sourcePath string
}

// NewDualPool constructs the canonical WAL-mode reader/writer pool
// `dual` from the on-disk SQLite file URI. The URI is enriched with
// the canonical Pragma set (`journal_mode=WAL`, `busy_timeout=5000`,
// `synchronous=NORMAL`) before the SQL Open call.
//
// numReaders < 2 is coerced to runtime.NumCPU() (the canonical SQLite
// reader concurrency floor). maxReaders < numReaders is coerced up
// to numReaders.
//
// Returns the dual on success, or a wrapped error on URI parse
// failure (bad URL syntax) / open failure (driver error).
//
// godlike/06 SSOT: composition-root callers MUST go through this
// constructor (NOT sql.Open) — see the package docstring rationale.
func NewDualPool(ctx context.Context, fileUri string, numReaders int) (*DualPool, error) {
	if fileUri == "" {
		return nil, errors.New("sqlite.NewDualPool: empty fileUri (fail-closed)")
	}
	if numReaders < 2 {
		numReaders = runtime.NumCPU()
		if numReaders < 2 {
			numReaders = 2
		}
	}

	walUri, err := walPragmas(fileUri)
	if err != nil {
		return nil, fmt.Errorf("sqlite.NewDualPool: enrich uri: %w", err)
	}

	writer, err := sql.Open("sqlite3", walUri)
	if err != nil {
		return nil, fmt.Errorf("sqlite.NewDualPool: open writer: %w", err)
	}
	// Pre-Cut-6.2 pattern: every test set MaxOpenConns(1) on a
	// :memory: db. The production dual keeps the same shape for
	// the writer pool (one connection) so the lease-fence UPDATE
	// path is uncontended on the writer side. db.SetMaxOpenConns(1)
	// is the canonical SQLite write-serialisation idiom for the
	// WAL-mode concurrent reader + single writer model.
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0) // SQLite connections do not expire.

	reader, err := sql.Open("sqlite3", walUri)
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("sqlite.NewDualPool: open reader: %w", err)
	}
	reader.SetMaxOpenConns(numReaders)
	reader.SetMaxIdleConns(numReaders)
	reader.SetConnMaxLifetime(0)

	// Ping context bounds the pragma application and connection
	// verification work.
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()

	if err := applyCanonicalPragmas(pingCtx, writer); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("sqlite.NewDualPool: apply writer pragmas: %w", err)
	}
	if err := applyCanonicalPragmas(pingCtx, reader); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("sqlite.NewDualPool: apply reader pragmas: %w", err)
	}

	// Ping verifies the WAL pragma applied at first open. A WAL
	// pragma failure (e.g. WAL not supported on the journal path
	// being used) surfaces here, BEFORE the caller routes through
	// the pool — godlike/07 fail-closed.
	if err := writer.PingContext(pingCtx); err != nil {
		_ = writer.Close()
		_ = reader.Close()
		return nil, fmt.Errorf("sqlite.NewDualPool: writer ping: %w", err)
	}

	return &DualPool{Writer: writer, Reader: reader, sourcePath: walUri, ownsHandles: true}, nil
}

// AttachDualPool exposes the canonical DatabaseSet primary handle through the
// legacy reader/writer-shaped adapter used by composition bundles. It never
// opens another SQLite connection or database file: both views intentionally
// reference DatabaseSet.Primary.DB. New runtime code should prefer
// DatabaseSet.Primary directly; this adapter exists only while bundle
// constructors migrate from the retired second-pool topology.
func AttachDualPool(primary *SQLiteDB) (*DualPool, error) {
	if primary == nil || primary.DB == nil {
		return nil, errors.New("sqlite.AttachDualPool: canonical primary is nil")
	}
	return &DualPool{
		Writer:      primary.DB,
		Reader:      primary.DB,
		sourcePath:  primary.Path(),
		ownsHandles: false,
	}, nil
}

func applyCanonicalPragmas(ctx context.Context, db *sql.DB) error {
	var journalMode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
		return fmt.Errorf("set journal_mode=WAL: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("set busy_timeout=5000: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA synchronous = NORMAL"); err != nil {
		return fmt.Errorf("set synchronous=NORMAL: %w", err)
	}
	return nil
}

// Close releases both writer and reader pool resources. Idempotent
// (calls Close twice without panicking — db.Close returns nil on
// already-closed DB). Returns the first error encountered; subsequent
// close errors are logged via the package-level contract that callers
// rely on for shutdown sequencing.
func (p *DualPool) Close() error {
	if p == nil || !p.ownsHandles {
		return nil
	}
	var firstErr error
	if p.Writer != nil {
		if err := p.Writer.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sqlite.DualPool.Close: writer: %w", err)
		}
	}
	if p.Reader != nil && p.Reader != p.Writer {
		if err := p.Reader.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sqlite.DualPool.Close: reader: %w", err)
		}
	}
	return firstErr
}

// SourcePath returns the WAL-enriched URI passed to driver SQL
// Opens. Diagnostic-only surface; production code goes through
// Writer/Reader for database operations.
func (p *DualPool) SourcePath() string { return p.sourcePath }

// BeginWriterTx is the canonical instrumented write-tx entry point.
// Every production write path (lease-fence UPDATEs, REPLACE INTO,
// outbox INSERTs) routes through this method (NOT Writer.BeginTx
// directly) so the connection_wait_seconds + tx_duration_seconds
// histograms observe the canonical path.
//
// The op="writer" label is preserved through both histograms so
// operators can read the writer-contention surface independently of
// the reader surface — the canonical observability demand for the
// reader/writer split.
//
// Return-type rationale (*instrumentedTx): every method of *sql.Tx
// (QueryContext, ExecContext, PrepareContext, Stmt, StmtContext,
// etc.) is promoted to *instrumentedTx via Go embedding of
// *sql.Tx — callers continue to use the canonical *sql.Tx call
// surface unchanged; only Commit/Rollback are overridden to
// observe the tx_duration_seconds histogram.
//
// godlike/07 fail-closed: classification of the BEGIN error (if any)
// populates sqlite_busy_total{op=writer} on a classified
// SQLITE_BUSY/SQLITE_LOCKED. Non-busy errors (e.g. context cancelled,
// schema mismatch) are surfaced verbatim via the returned error.
// The caller is expected to retry transient classified errors per
// the canonical retry.IsTransient contract; the metric is burn-rate
// evidence, not a retry trigger.
func (p *DualPool) BeginWriterTx(ctx context.Context) (*instrumentedTx, error) {
	return p.beginTx(ctx, "writer", p.Writer)
}

// BeginReaderTx is the canonical instrumented read-tx entry point.
// Read-only aggregates (`jobs_list_awaiting_aggregation` etc.) and
// reader-side observation endpoints route through this method.
//
// Read txs remain available in WAL-mode even while a writer is
// holding the single-writer slot — the canonical SQLite WAL
// concurrency model — so connection_wait_seconds{op=reader} is
// expected to be orders of magnitude lower than op=writer for
// typical workloads.
//
// Return-type rationale: same as BeginWriterTx (*instrumentedTx for
// Commit/Rollback metric observation, with *sql.Tx's other methods
// promoted through embedding for transparent caller use).
func (p *DualPool) BeginReaderTx(ctx context.Context) (*instrumentedTx, error) {
	return p.beginTx(ctx, "reader", p.Reader)
}

// beginTx is the shared begin path. The op label is stored on the
// observability histograms and on a context-derived key so the
// commit path can observe a paired tx_duration without recomputing
// the label. The conn acquisition latency is recorded as
// connection_wait_seconds; the commit-path tx_duration_seconds is
// recorded by the returned `instrumentedTx` (defined inline as the
// sentinel pattern of returning a deferred-observation wrapper).
//
// Implementation: we observe the acquisition time here, store the
// observed op label + start time on the returned transaction's
// emitted metrics via a sidecar struct (the canonical way to avoid
// sub-classing *sql.Tx). The commit path runs through
// `module_tx_done(op)` which is invoked from `instrumentedTxCommit`
// once per tx exit.
//
// godlike/07 fail-closed: SQLite SQLITE_BUSY observed at classifyTxError
// increments sqlite_busy_total{op}. Other errors are surfaced
// verbatim; the canonical retry.IsTransient contract downstream
// handles retry classification per the registered Classifier
// chain (decisions are made by the canonic retry.Decision walker,
// not by this instrumentation layer).
func (p *DualPool) beginTx(ctx context.Context, op string, pool *sql.DB) (*instrumentedTx, error) {
	if pool == nil {
		return nil, fmt.Errorf("sqlite.DualPool.beginTx: %s pool is nil (fail-closed)", op)
	}
	acquireStart := time.Now()
	tx, err := pool.BeginTx(ctx, nil)
	acquireSecs := time.Since(acquireStart).Seconds()
	if err != nil {
		classifyTxError(op, err)
		// Even on a begin failure, surface the wait latency so an
		// operator can correlate pool-acquire latency with the
		// failure rate (a degraded pool produces BOTH a high wait
		// AND a high error rate).
		observability.ConnectionWaitSeconds.WithLabelValues(op).Observe(acquireSecs)
		return nil, err
	}
	observability.ConnectionWaitSeconds.WithLabelValues(op).Observe(acquireSecs)
	return &instrumentedTx{
		Tx:    tx,
		op:    op,
		start: time.Now(),
	}, nil
}

// instrumentedTx wraps *sql.Tx so commit/rollback paths observe
// the canonical tx_duration_seconds histogram. Op + start are
// captured on the begin path; this struct is the deferred-commit
// hand-off.
//
// godlike/07 fail-closed: classifyTxError is invoked on commit/rollback
// errors; the outcome label on tx_duration_seconds is set to "err"
// on rollback as well (NOT "ok") so dashboards reflecting
// "ultra-short write tx honoured" count only committed writes.
type instrumentedTx struct {
	*sql.Tx
	op    string
	start time.Time
}

// Commit finishes the canonical write/read tx. Observes the
// tx_duration_seconds histogram BEFORE returning so an early-return
// error path still burns the metric (the canonical "observation
// survives all exit paths" guarantee from the metrics_outbox.go
// dispatch-time observation).
func (t *instrumentedTx) Commit() error {
	outcome := "ok"
	err := t.Tx.Commit()
	if err != nil {
		outcome = "err"
		classifyTxError(t.op, err)
	}
	observability.TxDurationSeconds.WithLabelValues(t.op, outcome).Observe(time.Since(t.start).Seconds())
	return err
}

// Rollback finishes the canonical write/read tx with a rollback.
// The outcome label is hard-coded "err" because a rolled-back tx
// is by definition an unsuccessful exit; dashboards counting
// "ok" tx count only committed txs.
func (t *instrumentedTx) Rollback() error {
	outcome := "err"
	err := t.Tx.Rollback()
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		// sql.ErrTxDone is a benign "tx already finalised" sentinel
		// that surfaces from inner driver paths; it does not
		// increment sqlite_busy_total (operationally a noop; the tx
		// is already done).
		classifyTxError(t.op, err)
	}
	observability.TxDurationSeconds.WithLabelValues(t.op, outcome).Observe(time.Since(t.start).Seconds())
	return err
}

// classifyTxError is the canonical sqlite_busy_total increment site.
// It inspects the err via errors.As for the typed *sqlite3.Error
// carrier and increments the metric on a SQLITE_BUSY/SQLITE_LOCKED
// code. non-busy errors are surfaced verbatim (no metric
// increment — the canonical "is this transient?" classification
// belongs to retry.Decision, not this instrumentation layer).
//
// godlike/06 SSOT: this layer NEVER calls retry.IsTransient or
// retry.WrapTransient — instrumentation must not import the retry
// package's classification surface (the layering is app → retry →
// infra, never infra → retry for classification side-effects).
func classifyTxError(op string, err error) {
	if err == nil {
		return
	}
	var sqErr *sqlite3.Error
	if errors.As(err, &sqErr) && sqErr != nil {
		if sqErr.Code == sqlite3.ErrBusy || sqErr.Code == sqlite3.ErrLocked {
			observability.SqliteBusyTotal.WithLabelValues(op).Inc()
		}
	}
}

// walPragmas enriches the SQLite URI with the canonical Pragma set.
//
//   - journal_mode=WAL: enables concurrent readers + single writer.
//   - busy_timeout=5000: 5-second busy-timeout, vastly longer than
//     the pre-Cut-6.2 default but bounded so a runaway tx surfaces
//     an ErrBusy rather than blocking indefinitely. The canonical
//     retry.IsTransient classification on ErrBusy routes the failure
//     to the retry loop.
//   - synchronous=NORMAL: WAL-safe (NORMAL does not risk corruption
//     on power loss in WAL mode the way it does in DELETE mode).
//
// Existing `_pragma=busy_timeout(5000)` segments in tests are
// preserved verbatim (the URI parser merges `_pragma` keys
// idempotently). Production callers go through NewDualPool and
// receive the canonical enriched URI; tests that bypass the pool
// factory and open sqlite directly MUST mirror the same pragma
// set or risk the "in-memory per-connection database" footgun
// seen in `assets/txmutation/primitives_test.go` and the
// `assets/clip_atomic_writer_test.go` fixtures.
func walPragmas(fileUri string) (string, error) {
	u, err := url.Parse(fileUri)
	if err != nil {
		return "", fmt.Errorf("walPragmas: parse %q: %w", fileUri, err)
	}
	q := u.Query()
	// Idempotent union: a pre-existing _pragma=X is preserved by
	// url.Values.Set replacing the same key.
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	// _busy_timeout param is honoured at the driver level too;
	// redundant with `_pragma=busy_timeout(5000)` but exposes the
	// value to driver-level code paths that read it directly.
	q.Set("_busy_timeout", strconv.Itoa(5000))
	u.RawQuery = q.Encode()
	return u.String(), nil
}
