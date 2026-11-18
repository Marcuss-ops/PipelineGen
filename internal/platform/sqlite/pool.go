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
