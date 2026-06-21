package script

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/contextutil"
)

// postWriteTimeout caps how long post-generation side-effects (DB
// writes, Google Doc uploads, cache persists) are allowed to run.
//
// The previous behaviour in this package was to reuse the request
// context for every post-generation write. That looked clean but
// caused silent data loss whenever the HTTP client disconnected
// before the response was sent: the LLM had produced the script,
// the DB save was in flight, and then c.Request.Context() was
// cancelled — taking the save with it. The 30s budget is generous
// for SQLite WAL writes and Google API calls on the local network
// and small enough that a hung save won't pin the worker.
const postWriteTimeout = 30 * time.Second

// withPostWriteContext returns a fresh context using an independent
// 30s-timeout context, decoupled from the caller's request context.
// Delegates to pkg/contextutil.PostWriteContext.
//
// Kept as a convenience wrapper for existing callers in this package.
func withPostWriteContext(parent context.Context, log *zap.Logger, op string) (context.Context, context.CancelFunc) {
	return contextutil.PostWriteContext(parent, log, op, postWriteTimeout)
}
