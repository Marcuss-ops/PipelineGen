// Package logsink implements the typed RequestLogSink port backed by
// a single SQLite *sql.DB. The implementation mirrors the original
// middleware_logger.go behaviour verbatim (channel-buffered non-blocking
// enqueue, 200-row batch, 100ms tick, transactional Prepare/Exec,
// drain-on-stop, dropped-log accounting) but the per-row INSERT stays
// in this infrastructure package while callers see only the typed
// requestlog.RequestLogSink surface.
package logsink

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	appmw "github.com/Marcuss-ops/PipelineGen/internal/application/middleware/requestlog"
)

// SQLiteRequestLogSink implements requestlog.RequestLogSink over a
// single *sql.DB. Defaults: 5000-row buffered channel, 200-row batch,
// 100ms tick. The background writer starts on the first Log call
// (so callers that only ever use FlushBatch pay no goroutine cost).
type SQLiteRequestLogSink struct {
	db          *sql.DB
	zlog        *zap.Logger
	channelCap  int
	batchSize   int
	flushPeriod time.Duration

	channel    chan appmw.RequestLogEntry
	stopChan   chan struct{}
	writerWG   sync.WaitGroup
	droppedLog uint64

	stopOnce  sync.Once
	startOnce sync.Once
}

// Compile-time assertion: SQLiteRequestLogSink satisfies the port.
// Any drift in the port signature is caught by `go build` immediately.
var _ appmw.RequestLogSink = (*SQLiteRequestLogSink)(nil)

// NewSQLiteRequestLogSink constructs an sqlite-backed sink.
func NewSQLiteRequestLogSink(db *sql.DB, zaplog *zap.Logger) *SQLiteRequestLogSink {
	if zaplog == nil {
		zaplog = zap.NewNop()
	}
	return &SQLiteRequestLogSink{
		db:          db,
		zlog:        zaplog,
		channelCap:  5000,
		batchSize:   200,
		flushPeriod: 100 * time.Millisecond,
		channel:     make(chan appmw.RequestLogEntry, 5000),
		stopChan:    make(chan struct{}),
	}
}

// DroppedLogs returns the number of entries dropped due to backpressure.
func (s *SQLiteRequestLogSink) DroppedLogs() uint64 {
	return atomic.LoadUint64(&s.droppedLog)
}

// Log enqueues an entry without blocking. If the channel is full the
// record is dropped silently and the counter is incremented.
func (s *SQLiteRequestLogSink) Log(ctx context.Context, entry appmw.RequestLogEntry) error {
	s.startWriterOnce()
	select {
	case s.channel <- entry:
		return nil
	default:
		atomic.AddUint64(&s.droppedLog, 1)
		return nil
	}
}

// FlushBatch persists entries in a single transaction. Per-row
// stmt.Exec failures are logged-and-skipped, so a single corrupt row
// does not abort the whole batch (matches the legacy
// middleware_logger.go::flushLogs semantics).
func (s *SQLiteRequestLogSink) FlushBatch(ctx context.Context, batch []appmw.RequestLogEntry) error {
	if len(batch) == 0 {
		return nil
	}
	if s.db == nil {
		return fmt.Errorf("sqlite_request_log_sink: nil DB")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("sqlite_request_log_sink: failed to start log transaction: %v", err)
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
      INSERT INTO api_requests
      (request_id, method, path, status, duration_ms, client_ip, user_id, bytes_in, bytes_out, user_agent, error)
      VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		log.Printf("sqlite_request_log_sink: failed to prepare log statement: %v", err)
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, e := range batch {
		_, err := stmt.ExecContext(ctx,
			e.RequestID, e.Method, e.Path, e.Status,
			float64(e.Duration.Microseconds())/1000.0,
			e.IP, e.UserID, e.BytesIn, e.BytesOut, e.UA, e.Err,
		)
		if err != nil {
			s.zlog.Warn("Failed to execute log insert", zap.Error(err))
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("sqlite_request_log_sink: failed to commit logs: %v", err)
		return err
	}
	return nil
}

// Stop drains pending entries then shuts down the background worker.
// Idempotent — safe to call from shutdown stacks and from tests.
func (s *SQLiteRequestLogSink) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		close(s.stopChan)
		s.writerWG.Wait()
	})
	return nil
}

// startWriterOnce launches the background batch worker on first
// Log call. Idempotent via sync.Once.
func (s *SQLiteRequestLogSink) startWriterOnce() {
	s.startOnce.Do(func() {
		s.writerWG.Add(1)
		go s.writer()
	})
}

// writer implements the leader loop: buffer entries until batchSize
// is reached or the flush tick fires, then FlushBatch the buffered
// set. Drain-on-stop pulls the remaining entries before returning.
func (s *SQLiteRequestLogSink) writer() {
	defer s.writerWG.Done()
	ticker := time.NewTicker(s.flushPeriod)
	defer ticker.Stop()
	batch := make([]appmw.RequestLogEntry, 0, s.batchSize)
	for {
		select {
		case e := <-s.channel:
			batch = append(batch, e)
			if len(batch) >= s.batchSize {
				_ = s.FlushBatch(context.Background(), batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				_ = s.FlushBatch(context.Background(), batch)
				batch = batch[:0]
			}
		case <-s.stopChan:
			// Drain remaining entries without closing the channel
			// (allows restart/reuse in tests). In production, the
			// process is exiting anyway.
		drain:
			for {
				select {
				case e := <-s.channel:
					batch = append(batch, e)
					if len(batch) >= s.batchSize {
						_ = s.FlushBatch(context.Background(), batch)
						batch = batch[:0]
					}
				default:
					break drain
				}
			}
			if len(batch) > 0 {
				_ = s.FlushBatch(context.Background(), batch)
			}
			return
		}
	}
}
