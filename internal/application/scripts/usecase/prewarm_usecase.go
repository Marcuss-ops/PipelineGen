// Package scripts — prewarm_usecase is the use case for the image
// prewarm goroutine that warms the Playwright tab pool before the
// heavy generation phases start.
//
// Wave 14 problem #4 (June 2026): previously this logic lived inline
// in api/script/handler_jobs.go::ScriptFlowHandler.HandleClipScriptGenerateJob:
//
//	if h.clipServices.ImgSvc != nil &&
//	    (spec.GenerateSceneImages || len(spec.ClipIDs) > 0 || spec.NumClips > 0) {
//	    go func() {
//	        prewarmCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
//	        defer cancel()
//	        h.clipServices.ImgSvc.TriggerPrewarm(prewarmCtx, j.ID, 4)
//	    }()
//	}
//
// Two problems with the inline form:
//   - the 15s timeout and 4-count magic numbers were buried in a
//     goroutine inside the handler — invisible to tests, untouchable
//     from elsewhere;
//   - the goroutine was fire-and-forget; the handler had no Wait or
//     in-flight counter, so operators could not reason about progress
//     and tests could not deterministically await completion;
//   - if the spawned goroutine panicked, it was uncaught (no
//     concurrent.SafeGo wrap).
//
// The use case:
//   - owns the timeout (default 15s, overridable), the prewarm count
//     (default 4), and the in-flight WaitGroup;
//   - wraps the goroutine in concurrent.SafeGo so a panic in the
//     prewarm code surfaces a log error instead of crashing the
//     worker;
//   - exposes Start + Wait so callers can deterministically test the
//     goroutine lifecycle (tests Wait() instead of sleeping).
//
// The use case does NOT own:
//   - whether to kick prewarm at all (caller decides — based on
//     spec.GenerateSceneImages / len(spec.ClipIDs) / spec.NumClips);
//   - the parent ctx (caller passes its own).
package usecase

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Default prewarm tunables kept package-private so external callers
// can't see them by accident — but exposed via functional options on
// NewPrewarmUseCase so a test can construct a "1s, count=2" prewarmer.
const (
	DefaultPrewarmTimeout = 15 * time.Second
	DefaultPrewarmCount   = 4
)

// PrewarmImageService is the port the use case depends on. The concrete
// *images.Service satisfies this implicitly (Go structural typing).
type PrewarmImageService interface {
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}

// ErrPrewarmMisconfigured is the sentinel for constructor arguments
// out of range (timeout <= 0, count <= 0).
var ErrPrewarmMisconfigured = errors.New("prewarm: timeout and count must be positive")

// ErrPrewarmUnconfigured is the sentinel for "Start was called on a
// nil use case or one without an image service". Maps to 503 in the
// handler when prewarm is strictly required; usually it's a no-op skip.
var ErrPrewarmUnconfigured = errors.New("prewarm: image service not configured")

// PrewarmUseCase is the orchestrator for image prewarm.
type PrewarmUseCase struct {
	imgSvc  PrewarmImageService
	log     *zap.Logger
	timeout time.Duration
	count   int

	inFlight sync.WaitGroup
}

// NewPrewarmUseCase constructs the prewarmer. The use case is
// nil-safe (a nil imgSvc means Start is a no-op).
func NewPrewarmUseCase(imgSvc PrewarmImageService, log *zap.Logger) *PrewarmUseCase {
	return &PrewarmUseCase{
		imgSvc:  imgSvc,
		log:     log,
		timeout: DefaultPrewarmTimeout,
		count:   DefaultPrewarmCount,
	}
}

// WithTimeout overrides the default 15s timeout. Functional option so
// the ctor signature stays simple.
func (u *PrewarmUseCase) WithTimeout(d time.Duration) *PrewarmUseCase {
	if u != nil && d > 0 {
		u.timeout = d
	}
	return u
}

// WithCount overrides the default 4 count. Functional option so the
// ctor signature stays simple.
func (u *PrewarmUseCase) WithCount(n int) *PrewarmUseCase {
	if u != nil && n > 0 {
		u.count = n
	}
	return u
}

// ShouldStart returns the decision the previous handler took inline
// (the spec.GenerateSceneImages-or-ClipIDs-or-NumClips triple).
// Pulled out so callers don't have to duplicate the boolean expression
// and so unit tests can pin "given Spec X -> ShouldStart".
func ShouldStart(specGenerateSceneImages bool, specClipIDsLen, specNumClips int) bool {
	return specGenerateSceneImages || specClipIDsLen > 0 || specNumClips > 0
}

// Start kicks off the prewarm goroutine. Returns immediately —
// callers can Wait() to drain. Returns nil on the fast path
// (use case unconfigured, image service nil, or ShouldStart == false).
func (u *PrewarmUseCase) Start(ctx context.Context, jobID string, shouldStart bool) error {
	if u == nil {
		return ErrPrewarmUnconfigured
	}
	if !shouldStart {
		return nil
	}
	if u.imgSvc == nil {
		if u.log != nil {
			u.log.Info("prewarm skipped: image service not wired", zap.String("job_id", jobID))
		}
		return nil
	}
	timeout := u.timeout
	count := u.count
	imgSvc := u.imgSvc
	log := u.log

	u.inFlight.Add(1)
	concurrent.SafeGo("script-prewarm", func() {
		defer u.inFlight.Done()
		prewarmCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if log != nil {
			log.Info("prewarm started", zap.String("job_id", jobID),
				zap.Duration("timeout", timeout), zap.Int("count", count))
		}
		imgSvc.TriggerPrewarm(prewarmCtx, jobID, count)
		if log != nil {
			log.Info("prewarm dispatched", zap.String("job_id", jobID))
		}
	})
	return nil
}

// Wait blocks until all in-flight prewarm goroutines have returned.
// Useful in tests (deterministic drain) and at shutdown (graceful
// drain of outstanding prewarm jobs).
func (u *PrewarmUseCase) Wait() {
	if u == nil {
		return
	}
	u.inFlight.Wait()
}
