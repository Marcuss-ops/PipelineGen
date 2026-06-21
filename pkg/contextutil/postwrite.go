// Package contextutil provides shared context patterns for the PipelineGen
// codebase. The post-write pattern is the canonical way to create a
// save/cleanup context that outlives the caller's request context, so
// side-effects (DB writes, Qdrant reindex, Google Doc creation) are not
// lost when the HTTP client disconnects mid-generation.
//
// Usage:
//
//	saveCtx, cancel := contextutil.PostWriteContext(ctx, log, "save script", 30*time.Second)
//	defer cancel()
//	if err := repo.Save(saveCtx, ...); err != nil { ... }
package contextutil

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// PostWriteContext returns a fresh context with an independent timeout,
// decoupled from the parent context's cancellation. It is intended for
// side-effects that must survive the parent context being cancelled.
//
// When parent is already cancelled, a structured warning is logged via
// log (if non-nil) so operators can distinguish "normal post-write save"
// from "request timed out before save started".
//
// timeout should be generous enough for the target operation (e.g. 30s
// for SQLite WAL writes, 2m for Qdrant reindex). Pass 0 for a context
// without deadline (use sparingly).
func PostWriteContext(parent context.Context, log *zap.Logger, op string, timeout time.Duration) (context.Context, context.CancelFunc) {
	var saveCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		saveCtx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		saveCtx, cancel = context.WithCancel(context.Background())
	}
	if log != nil && parent != nil {
		if err := parent.Err(); err != nil {
			log.Warn("post-generation side-effect: parent context already cancelled, proceeding with independent save context",
				zap.String("op", op),
				zap.Duration("save_timeout", timeout),
				zap.Error(err),
			)
		}
	}
	return saveCtx, cancel
}
