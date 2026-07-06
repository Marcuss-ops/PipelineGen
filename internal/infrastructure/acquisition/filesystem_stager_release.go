package acquisition

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
)

func (f *FilesystemStager) Release(ctx context.Context, cleanupToken string) error {
	if f == nil {
		return appacq.ErrAcquisitionNotWired
	}
	if cleanupToken == "" {
		return appacq.Wrap(appacq.ErrAcquisitionInvalidToken, "empty CleanupToken")
	}

	f.mu.Lock()
	cached, ok := f.byTok[cleanupToken]
	f.mu.Unlock()
	if ok {
		return f.releaseByContext(ctx, cached)
	}

	// On cache miss, search by filename (the CleanupToken was
	// derived from SourceRef, so we can re-derive the file path
	// from the cache miss's suspected source). We do a simpler
	// walk: scan the stagingRoot for any .meta.json whose inner
	// CleanupToken matches; that's O(N) on staging count, OK for
	// §12-4's per-run staging surface (a few hundred at most).
	matches, err := f.findByToken(cleanupToken)
	if err != nil {
		return appacq.Wrap(appacq.ErrAcquisitionInvalidToken, err.Error())
	}
	if len(matches) == 0 {
		return appacq.Wrap(appacq.ErrAcquisitionInvalidToken, "CleanupToken does not match any registered stage (cache miss + filesystem scan miss)")
	}
	if len(matches) > 1 {
		// Two stages with the SAME CleanupToken — sha-clash.
		// We deliberately fail-closed here instead of releasing
		// either; operator must intervene.
		return appacq.Wrap(appacq.ErrAcquisitionInvalidToken,
			fmt.Sprintf("CleanupToken collision: %d stages share this token (manual reconcile required)", len(matches)))
	}
	return f.releaseByContext(ctx, matches[0])
}

// releaseByContext removes the staged file + metadata. The Called-
// side guards (`Expired`, shared-CleanupToken) are checked here.
func (f *FilesystemStager) releaseByContext(_ context.Context, ctx appacq.PrepareContext) error {
	metaPath := ctx.LocalPath + ".meta.json"
	stagedPath := ctx.LocalPath

	// Expired — the underlying file IS already gone (or about to
	// be). Report a typed error so the caller can branch on the
	// specific failure class.
	if ctx.Expired() {
		// Sweep anyway: TTL GC may have missed the file (clock
		// skew, etc.), in which case we still remove it for
		// idempotency.
		if err := os.RemoveAll(stagedPath); err != nil && !os.IsNotExist(err) {
			return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed,
				fmt.Sprintf("release expired stage: removeAll %q: %v", stagedPath, err))
		}
		_ = os.RemoveAll(metaPath)
		f.forgetToken(ctx.CleanupToken)
		return appacq.Wrap(appacq.ErrAcquisitionExpired, fmt.Sprintf("stage already expired at %s (swept on release)", ctx.ExpiresAt.Format(time.RFC3339)))
	}

	if err := os.RemoveAll(stagedPath); err != nil && !os.IsNotExist(err) {
		return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, fmt.Sprintf("remove staged %q: %v", stagedPath, err))
	}
	if err := os.RemoveAll(metaPath); err != nil && !os.IsNotExist(err) {
		return appacq.Wrap(appacq.ErrAcquisitionPrepareFailed, fmt.Sprintf("remove meta %q: %v", metaPath, err))
	}
	f.forgetToken(ctx.CleanupToken)

	f.log.Info("acquisition release completed",
		zap.String("stage_id", ctx.ID),
		zap.String("local_path", stagedPath),
	)
	return nil
}

// ── Cache plumbing ──────────────────────────────────────────────────
