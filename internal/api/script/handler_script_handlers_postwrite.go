package script

import (

	"context"
	"time"

	"go.uber.org/zap"
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
//
// The timeout lives here, not in the gemmamemory service, because
// the same pattern now needs to apply to scriptsRepo.SaveScript,
// scriptsRepo.SaveResearchCache, and drive.DocClient.CreateDoc —
// all of which are caller-side and were identified as having the
// same vulnerability.
const postWriteTimeout = 30 * time.Second

// withPostWriteContext returns a fresh context using an independent
// 30s-timeout context, decoupled from the caller's request context.
// It is intended for side-effects that must survive the client
// disconnecting while the LLM is still producing output.
//
// op is a short human-readable label used in the diagnostic log
// emitted when the caller's context is already cancelled. log may
// be nil (the warning is skipped in that case).
//
// Usage:
//
//	saveCtx, cancel := withPostWriteContext(ctx, h.log, "save script")
//	defer cancel()
//	if err := h.scriptsRepo.SaveScript(saveCtx, ...); err != nil { ... }
func withPostWriteContext(parent context.Context, log *zap.Logger, op string) (context.Context, context.CancelFunc) {
	saveCtx, cancel := context.WithTimeout(context.Background(), postWriteTimeout)
	if log != nil && parent != nil {
		if err := parent.Err(); err != nil {
			log.Warn("post-generation side-effect: request context already cancelled, proceeding with independent save context",
				zap.String("op", op),
				zap.Duration("save_timeout", postWriteTimeout),
				zap.Error(err),
			)
		}
	}
	return saveCtx, cancel
}
