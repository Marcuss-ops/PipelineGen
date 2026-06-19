// Package outbox — DirectIndexer is the documented exception to the
// "everything goes through the outbox" rule.
//
// PR1 invariant: nothing bypasses the outbox except explicit admin
// reindex endpoints (currently: POST /api/admin/reindex, the
// media.reindex job handler, and any endpoint whose auth middleware
// injects the admin reindex context key — see guards below).
//
// DirectIndexer's purpose is to allow a privileged operator to FORCIBLY
// (re)generate embeddings for an existing asset without having to wait
// for the outbox worker, or to bypass the dedup-by-content-hash check
// that the outbox relies on for idempotency. It is NOT a general-purpose
// alternative to Dispatcher.
//
// To prevent accidental use from production ingestion paths, the
// public top-level func bombs with a panic unless the supplied context
// carries the AdminReindexKey. Internal callers (the indexer Service,
// admin reindex handlers, the batch reindex job) set that key before
// invoking IndexDirect. Test code can also stamp the key.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"

	"go.uber.org/zap"
)

// withAdminAuditLogger holds the logger used to audit every stamp of the
// AdminReindexKey. Set via SetAdminReindexAuditLogger from app startup.
// Default is zap.NewNop() so tests and unconfigured callers stay quiet.
var withAdminAuditLogger atomic.Pointer[zap.Logger]

func init() {
	stderrLogger, _ := zap.NewProduction()
	withAdminAuditLogger.Store(stderrLogger)
}

// SetAdminReindexAuditLogger installs a logger that will receive an entry
// every time WithAdminReindex is called. Production wiring should install
// a logger so each admin-reindex stamping is greppable in production logs.
func SetAdminReindexAuditLogger(l *zap.Logger) {
	if l == nil {
		l = zap.NewNop()
	}
	withAdminAuditLogger.Store(l)
}

// AdminReindexKey is the context key that authorises DirectIndexer to run.
// Stamping this key must be guarded by an auth middleware that proves the
// caller is an admin operator; the key MUST NOT be set from HTTP request
// bodies, query parameters, or any untrusted source.
type AdminReindexKey struct{}

// ErrDirectIndexerAbuse is returned when IndexDirect is called without the
// AdminReindexKey in the context. This is a programmer error, not a user
// error — call sites must stamp the key via WithAdminReindex.
var ErrDirectIndexerAbuse = errors.New(
	"outbox.DirectIndexer requires context.WithValue(AdminReindexKey{}) — " +
		"this path is reserved for admin reindex endpoints; ingestion " +
		"callers must use outbox.Dispatcher.EnqueueAndIndex instead",
)

// WithAdminReindex stamps a context with the AdminReindexKey. ONLY call
// this from admin reindex endpoints whose caller has been authenticated
// against VELOX_ADMIN_TOKEN. Anywhere else, do not.
//
// Example in an admin handler:
//
//	ctx := outbox.WithAdminReindex(c.Request.Context())
//	if err := h.directIndexer.IndexDirect(ctx, clipID); err != nil { ... }
//
// IMPORTANT — this is the SINGLE allowed stamping entry-point. Do not
// introduce a second helper that stamps AdminReindexKey (audit will flag
// the duplicate, and code review should block the new helper).
func WithAdminReindex(ctx context.Context) context.Context {
	_, callerFile, callerLine, callerOK := runtime.Caller(1)
	callerStr := "unknown"
	if callerOK {
		callerStr = fmt.Sprintf("%s:%d", callerFile, callerLine)
	}
	withAdminAuditLogger.Load().Warn("admin reindex context stamped",
		zap.Bool("had_admin_key_already", IsAdminReindex(ctx)),
		zap.String("caller", callerStr),
	)
	return context.WithValue(ctx, AdminReindexKey{}, true)
}

// IsAdminReindex reports whether ctx carries the AdminReindexKey. Used by
// tests and by any audit log that needs to distinguish a forced reindex
// from a normal ingestion.
func IsAdminReindex(ctx context.Context) bool {
	v, ok := ctx.Value(AdminReindexKey{}).(bool)
	return ok && v
}

// DirectIndexer forces a synchronous IndexClip on the supplied clip ID,
// bypassing the outbox worker. Use only for admin reindex; ingestion
// callers must use Dispatcher.
type DirectIndexer struct {
	indexer IndexClipper
	log     *zap.Logger
}

// IndexClipper is the minimum surface that DirectIndexer needs from the
// underlying clipindexer.Service. Defined as an interface so tests can
// substitute a stub that records calls without doing real embedding work.
type IndexClipper interface {
	IndexClip(ctx context.Context, clipID string) error
}

// NewDirectIndexer wires a DirectIndexer against the actual indexer.
func NewDirectIndexer(indexer IndexClipper, log *zap.Logger) *DirectIndexer {
	return &DirectIndexer{indexer: indexer, log: log}
}

// IndexDirect performs a synchronous index of clipID, bypassing the
// outbox. The supplied context MUST carry AdminReindexKey (see
// WithAdminReindex) — callers that fail the check receive
// ErrDirectIndexerAbuse instead of running the index.
//
// On success, the embeddings and Qdrant upsert are already done by the
// time IndexDirect returns; the caller can report completion to the
// user immediately (no need to poll).
func (d *DirectIndexer) IndexDirect(ctx context.Context, clipID string) error {
	if d == nil {
		return errors.New("outbox.DirectIndexer is nil")
	}
	if clipID == "" {
		return errors.New("clipID is required")
	}
	if !IsAdminReindex(ctx) {
		d.log.Warn("DirectIndexer abuse guard tripped",
			zap.String("clip_id", clipID),
			zap.Error(ErrDirectIndexerAbuse))
		return fmt.Errorf("clip %s: %w", clipID, ErrDirectIndexerAbuse)
	}
	d.log.Info("admin reindex: synchronous indexing, bypassing outbox",
		zap.String("clip_id", clipID))
	if err := d.indexer.IndexClip(ctx, clipID); err != nil {
		return fmt.Errorf("direct index %s: %w", clipID, err)
	}
	return nil
}
