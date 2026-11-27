package renderinggen

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// Settle is the POST-SUBMIT half of the boundary: wait for the remote render's
// terminal state, require the certified Chronon artifact, and project the
// render outcome carrying the DURABLE LOCATOR of the artifact (storage key,
// URL, content type, certified digest, size and facts).
//
// It deliberately does NOT download the artifact: the object store is the
// canonical durable home of the rendered bytes, and the clip.render completion
// path commits that locator and lets the Drive outbox stream object-store →
// Drive. A consumer that genuinely needs a local file (localization) calls
// Materialize, which fetches and verifies on demand. This removes the eager
// 500 MB download plus the workspace→staging copy that the audit flagged.
//
// It is the ONLY place that blocks on RenderingGen, which is why the async
// boundary can hand it to a continuation job instead of holding the submission's
// worker slot. Resumable: it addresses plan.RunID, so it needs no process-local
// state from the submit call.
func (e *ClipRenderExecutor) Settle(ctx context.Context, plan cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
	return e.SettleWithJobID(ctx, plan, plan.RunID)
}

// SettleWithin is the BOUNDED settle: the deferrable form of SettleWithJobID.
//
// It exists so the settle continuation stops parking a worker lane on the whole
// remote render. A render that finishes inside the budget behaves exactly like
// the blocking form; one that does not is reported as cliprender.ErrRenderPending
// WITH nothing else changed, so the caller can hand its attempt back (a
// deferral, which spends no retry budget) and ask again later. budget <= 0 is
// the historical blocking wait.
func (e *ClipRenderExecutor) SettleWithin(ctx context.Context, plan cliprender.ClipRenderPlanV1, renderJobID string, budget time.Duration) (*cliprender.RenderOutcome, error) {
	return e.settle(ctx, plan, renderJobID, budget)
}

// SettleWithJobID is the restart-safe settle path for chunk families whose
// assembly anchor is derived from the sealed plan digest rather than RunID.
func (e *ClipRenderExecutor) SettleWithJobID(ctx context.Context, plan cliprender.ClipRenderPlanV1, renderJobID string) (*cliprender.RenderOutcome, error) {
	return e.settle(ctx, plan, renderJobID, 0)
}

// settle is the ONE settle implementation: the blocking and the bounded entries
// differ only by the wait budget, so they cannot disagree about what a certified
// terminal render means.
func (e *ClipRenderExecutor) settle(ctx context.Context, plan cliprender.ClipRenderPlanV1, renderJobID string, waitBudget time.Duration) (*cliprender.RenderOutcome, error) {
	if e == nil || e.queue == nil {
		return nil, fmt.Errorf("%w: RenderingGen queue is not configured", cliprender.ErrBackendUnavailable)
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("renderinggen clip executor: validate plan: %w", err)
	}
	if strings.TrimSpace(renderJobID) == "" {
		return nil, fmt.Errorf("renderinggen clip executor: remote render job id is required")
	}
	// The wait is the canonical capability wait (renderqueue.
	// WaitRenderQueueTerminalBounded, which delegates to WaitRenderQueueTerminal
	// for budget <= 0), not a local copy: the settle continuation and the
	// overlay enqueue path must agree on what "terminal" means. A budget expiry
	// is translated into the capability-owned pending sentinel — the transport's
	// signal must never leak into the capability's error vocabulary.
	// A settle can park a worker lane on a render that never advances, so it
	// ALWAYS carries the progress watchdog. The historical branch passed budget
	// 0 after the settle window closed, which selected the plain long poll and
	// lost the watchdog exactly when a stuck render needed it most (CR-03).
	// With budget 0 the watched wait has no deadline: a progressing render is
	// waited out, a stalled one is cancelled after DefaultRenderStallAfter.
	completed, _, err := renderqueue.WaitRenderQueueTerminalWatched(
		ctx, e.queue, renderJobID, e.interval, waitBudget, renderqueue.DefaultRenderStallAfter)
	if err != nil {
		if errors.Is(err, renderqueue.ErrRenderQueuePending) {
			return nil, fmt.Errorf("%w: job %s: %v", cliprender.ErrRenderPending, renderJobID, err)
		}
		if errors.Is(err, renderqueue.ErrRenderQueueStalled) {
			observability.SettleProgressStallsTotal.WithLabelValues("clip.render").Inc()
			var stalled *renderqueue.RenderStalledError
			if errors.As(err, &stalled) && stalled.CancelErr == nil {
				observability.SettleCancelledTotal.WithLabelValues("clip.render").Inc()
			}
			return nil, fmt.Errorf("%w: %v", cliprender.ErrRenderStalled, err)
		}
		return nil, fmt.Errorf("renderinggen clip executor: wait: %w", err)
	}
	if completed.State != string(queueclient.StateCompleted) || completed.Artifact == nil {
		return nil, fmt.Errorf("renderinggen clip executor: job %s completed without certified artifact", plan.RunID)
	}
	a := completed.Artifact
	if a.SHA256 == "" || a.SizeBytes <= 0 || a.URL == "" {
		return nil, fmt.Errorf("renderinggen clip executor: job %s returned incomplete artifact certification", plan.RunID)
	}
	if a.Backend != string(cliprender.BackendChrononVulkan) {
		return nil, fmt.Errorf("renderinggen clip executor: job %s rendered with backend %q; clip.render requires Chronon (%s)", plan.RunID, a.Backend, cliprender.BackendChrononVulkan)
	}
	// The complete certified fact set is fail-closed: a malformed
	// certification document is a render error, never a silently missing
	// certification that would let the contract gate skip its dimensions.
	facts, err := decodeCertifiedFacts(a.OutputFacts)
	if err != nil {
		return nil, fmt.Errorf("renderinggen clip executor: decode certified output facts: %w", err)
	}
	metrics := metricsFromChrononMetrics(a.Metrics, a.FrameCount, a.DurationUS)
	// Complete the render-admission wait. metricsFromChrononMetrics already
	// projected the wait the RENDERER measured (a fully prepared job with no
	// free GPU lane); what only the producer can see is the earlier half: the
	// queue held the job with no free worker at all (queued → claimed). The two
	// intervals are consecutive, so the field is their sum — "how long this
	// render waited before it started rendering", which is exactly the time a
	// settle wall spends without making progress.
	if !completed.QueuedAt.IsZero() && !completed.StartedAt.IsZero() {
		wait := completed.StartedAt.Sub(completed.QueuedAt)
		if wait < 0 {
			wait = 0
		}
		if int64(metrics.ChrononQueueWaitMS) == cliprender.NotInstrumented {
			metrics.ChrononQueueWaitMS = cliprender.Metric(wait.Milliseconds())
		} else {
			metrics.ChrononQueueWaitMS += cliprender.Metric(wait.Milliseconds())
		}
	}
	// The zero-copy certification surface was removed with the PATH B CUDA
	// hybrid: RenderingGen/Chronon never certifies video_zero_copy over this
	// transport, so a request that demands it fails closed in the worker.
	return &cliprender.RenderOutcome{
		// Locator-first: no local materialization. The certified bytes live in
		// RenderingGen's object store under StorageKey / ArtifactURL.
		OutputPath:  "",
		SizeBytes:   a.SizeBytes,
		SHA256:      strings.ToLower(strings.TrimSpace(a.SHA256)),
		StorageKey:  a.StorageKey,
		ArtifactURL: a.URL,
		ContentType: a.MimeType,
		DurationSec: float64(a.DurationUS) / 1e6,
		Width:       uint32(a.Width),
		Height:      uint32(a.Height),
		FPSNum:      uint32(a.FPSNum),
		FPSDen:      uint32(a.FPSDen),
		// Certified structural facts: the render boundary probed these on the
		// exact bytes it uploaded, so downstream contract validation consumes
		// them instead of guessing (the local Rust probe reports no profile).
		Container:         a.Container,
		VideoCodec:        a.Codec,
		VideoProfile:      a.CodecProfile,
		PixelFormat:       a.PixelFormat,
		AudioStreams:      a.AudioStreams,
		Facts:             facts,
		Backend:           cliprender.RenderBackend(a.Backend),
		AudioCopyEligible: boolPtr(a.CopyEligible),
		Metrics:           metrics,
		// Raw deep-profile sidecar reference preserved by RenderingGen
		// (content-addressed; the per-frame array is never inlined).
		ChrononTimingStorageKey:  a.ChrononTimingStorageKey,
		ChrononTimingURL:         a.ChrononTimingURL,
		ChrononTimingSHA256:      a.ChrononTimingSHA256,
		ChrononTimingSizeBytes:   a.ChrononTimingSizeBytes,
		ChrononTimingContentType: a.ChrononTimingContentType,
	}, nil
}
