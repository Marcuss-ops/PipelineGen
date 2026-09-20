// Package renderinggen adapts the central RenderingGen queue's public client
// to the script-generation capability. PipelineGen no longer owns the HTTP
// contract: it delegates to github.com/Marcuss-ops/RenderingGen/queue/client
// and only maps between the capability domain types and the queue's wire
// types, so the wire format can never drift between the two codebases.
package renderinggen

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
	"golang.org/x/sync/errgroup"
)

// Client adapts the queue's public client to scriptgen.RenderQueueClient.
type Client struct {
	q        *queueclient.Client
	prefetch *AssetPrefetcher
}

// AssetPrefetcher warms every RenderQueueJob asset without changing queue
// state. It is injected by the composition root and shares the canonical
// overlay AssetPreparer used by overlay.prepare.
type AssetPrefetcher struct {
	prepare func(context.Context, []scriptgen.RenderQueueAsset) error
}

func NewAssetPrefetcher(prepare func(context.Context, []scriptgen.RenderQueueAsset) error) *AssetPrefetcher {
	return &AssetPrefetcher{prepare: prepare}
}

func (p *AssetPrefetcher) Prefetch(ctx context.Context, assets []scriptgen.RenderQueueAsset) error {
	if p == nil || p.prepare == nil {
		return nil
	}
	return p.prepare(ctx, assets)
}

func (c *Client) SetAssetPrefetcher(prefetch *AssetPrefetcher) {
	if c != nil {
		c.prefetch = prefetch
	}
}

// New creates a queue client for the given RenderingGen queue endpoint.
func New(baseURL string) *Client {
	return &Client{q: queueclient.New(baseURL)}
}

// Submit enqueues a job. A 409 (job already exists) is surfaced as
// scriptgen.ErrJobExists so the enqueuer treats replays as idempotent.
func (c *Client) Submit(ctx context.Context, job scriptgen.RenderQueueJob) error {
	if c == nil || c.q == nil {
		return fmt.Errorf("renderinggen submit: client is not configured")
	}
	if c.prefetch != nil {
		if err := c.prefetch.Prefetch(ctx, job.Assets); err != nil {
			return fmt.Errorf("renderinggen asset prefetch: %w", err)
		}
	}
	err := c.q.Submit(ctx, queueclient.Job{
		ID:          job.ID,
		JobType:     job.JobType,
		ParentJobID: job.ParentJobID,
		ChunkIndex:  job.ChunkIndex,
		FrameRange:  toQueueFrameRange(job.FrameRange),
		RenderPlan:  job.OverlaySpec,
		Assets:      toQueueAssets(job.Assets),
	})
	if errors.Is(err, queueclient.ErrJobExists) {
		return fmt.Errorf("%w: job %s", scriptgen.ErrJobExists, job.ID)
	}
	if err != nil {
		return fmt.Errorf("renderinggen submit: %w", err)
	}
	return nil
}

// Get returns the current state of a job, including its artifact once done.
func (c *Client) Get(ctx context.Context, id string) (scriptgen.RenderQueueJob, error) {
	job, err := c.q.Get(ctx, id)
	if err != nil {
		return scriptgen.RenderQueueJob{}, fmt.Errorf("renderinggen get: %w", err)
	}
	return toScriptJob(job), nil
}

func (c *Client) Children(ctx context.Context, parentID string) ([]scriptgen.RenderQueueJob, error) {
	if c == nil || c.q == nil {
		return nil, fmt.Errorf("renderinggen children: client is not configured")
	}
	jobs, err := c.q.Children(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("renderinggen children: %w", err)
	}
	out := make([]scriptgen.RenderQueueJob, len(jobs))
	for i := range jobs {
		out[i] = toScriptJob(jobs[i])
	}
	return out, nil
}

// WaitTerminal implements scriptgen.RenderQueueWaiter over the queue's
// job-status long poll (GET /jobs/{id}/wait). The enqueuer observes a
// terminal render at the state transition instead of at the next polling
// tick; the queue client degrades to polling on its own when the wait route
// is absent (older server).
func (c *Client) WaitTerminal(ctx context.Context, id string) (scriptgen.RenderQueueJob, error) {
	if c == nil || c.q == nil {
		return scriptgen.RenderQueueJob{}, fmt.Errorf("renderinggen wait: client is not configured")
	}
	job, err := c.q.WaitTerminal(ctx, id)
	if err != nil {
		return scriptgen.RenderQueueJob{}, fmt.Errorf("renderinggen wait: %w", err)
	}
	return toScriptJob(job), nil
}

// toScriptJob maps a queue wire job onto the capability's typed job. It is the
// single projection shared by Get and WaitTerminal.
func toScriptJob(job queueclient.Job) scriptgen.RenderQueueJob {
	return scriptgen.RenderQueueJob{
		ID:          job.ID,
		JobType:     job.JobType,
		ParentJobID: job.ParentJobID,
		ChunkIndex:  job.ChunkIndex,
		FrameRange:  fromQueueFrameRange(job.FrameRange),
		OverlaySpec: job.RenderPlan,
		Assets:      fromQueueAssets(job.Assets),
		State:       string(job.State),
		FailReason:  job.FailReason,
		Artifact:    toScriptArtifact(job.Artifact),
		// Queue-owned lifecycle timestamps: the admission/service split the
		// producer cannot observe from the artifact alone.
		QueuedAt:    job.QueuedAt,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
	}
}

func fromQueueFrameRange(in *queueclient.FrameRange) *scriptgen.RenderFrameRange {
	if in == nil {
		return nil
	}
	return &scriptgen.RenderFrameRange{Start: in.Start, End: in.End}
}

// Retry resets a failed job back to pending state.
func (c *Client) Retry(ctx context.Context, id string) error {
	if c == nil || c.q == nil {
		return fmt.Errorf("renderinggen retry: client is not configured")
	}
	return c.q.Retry(ctx, id)
}

func toQueueAssets(in []scriptgen.RenderQueueAsset) []queueclient.AssetRef {
	if in == nil {
		return nil
	}
	out := make([]queueclient.AssetRef, len(in))
	for i, a := range in {
		// The canonical identity supplies the digest, so the wire never sees a
		// second spelling of one content address. Ref() already returns
		// canonicalised values; re-canonicalising here would be a second
		// (silently different) owner of the spelling rule.
		out[i] = queueclient.AssetRef{Hash: a.Ref().SHA256, LogicalPath: a.URL, SourceURL: a.SourceURL}
	}
	return out
}

func fromQueueAssets(in []queueclient.AssetRef) []scriptgen.RenderQueueAsset {
	if in == nil {
		return nil
	}
	out := make([]scriptgen.RenderQueueAsset, len(in))
	for i, a := range in {
		out[i] = scriptgen.NewRenderQueueAsset(
			kernelasset.Ref{AssetID: a.LogicalPath, SHA256: a.Hash}, a.LogicalPath, a.SourceURL)
	}
	return out
}

// metricMillis reads a millisecond metric from the worker's metrics map,
// rounding down to whole milliseconds. Absent keys yield 0 (unreported).
func metricMillis(m map[string]float64, key string) int64 {
	if m == nil {
		return 0
	}
	return int64(m[key])
}

// metricMillisEither reads a worker-reported phase duration preferring the
// millisecond key and falling back to the microsecond key (the RenderingGen
// worker reports materialize/render/hash/upload phases in microseconds and
// the drive phase in milliseconds). Absent keys yield 0 (unreported).
func metricMillisEither(m map[string]float64, msKey, usKey string) int64 {
	if m == nil {
		return 0
	}
	if v := int64(m[msKey]); v > 0 {
		return v
	}
	if v := int64(m[usKey]); v > 0 {
		return v / 1000
	}
	return 0
}

// The queue seam for clip rendering is the CANONICAL capability port
// (scriptgen.RenderQueueClient, with scriptgen.RenderQueueWaiter and
// scriptgen.RenderQueueRetrier as its optional capabilities). This file used to
// declare a second, narrower port (ClipRenderQueue: Submit + Get + Retry) for
// the same remote service — one fact with two owners, so "what the queue can
// do" had two answers that could drift, and the retry-recovery rule had a third
// copy at each call site. The port is owned by the capability that consumes it;
// this package only implements it (*Client satisfies all three).

// ClipRenderExecutor submits one complete clip segment to RenderingGen. The
// RenderingGen worker is the sole clip-rendering boundary; it lowers the
// semantic plan and executes it with Chronon. This client never invokes a
// local renderer and fails closed on missing or non-Chronon artifacts.
type ClipRenderExecutor struct {
	queue          scriptgen.RenderQueueClient
	interval       time.Duration
	chunkDuration  time.Duration
	chunkAlignment int64
	chunkMax       int
}

func NewClipRenderExecutor(queue scriptgen.RenderQueueClient) (*ClipRenderExecutor, error) {
	if queue == nil {
		return nil, fmt.Errorf("renderinggen clip executor: queue client is required")
	}
	return &ClipRenderExecutor{queue: queue, interval: 500 * time.Millisecond}, nil
}

func (e *ClipRenderExecutor) SetPollInterval(interval time.Duration) *ClipRenderExecutor {
	if e != nil && interval > 0 {
		e.interval = interval
	}
	return e
}

// SetChunking enables the conservative production chunk policy for long,
// source-only clips. Composition plans remain exclusive.
func (e *ClipRenderExecutor) SetChunking(chunkDuration time.Duration, alignmentFrames int64, chunkMax int) *ClipRenderExecutor {
	if e == nil {
		return e
	}
	if chunkDuration > 0 && alignmentFrames > 0 && chunkMax > 1 {
		e.chunkDuration = chunkDuration
		e.chunkAlignment = alignmentFrames
		e.chunkMax = chunkMax
	}
	return e
}

// RenderJobID returns the durable remote address for this plan. Plain renders
// use RunID; chunk families use their content-addressed assembly anchor.
func (e *ClipRenderExecutor) RenderJobID(plan cliprender.ClipRenderPlanV1) string {
	if requested, alignment, ok := e.chunkPolicy(plan); ok {
		if set, err := cliprender.BuildChunkSet(plan, requested, alignment); err == nil {
			return set.AnchorJobID()
		}
	}
	return plan.RunID
}

// Submit is the PRE-RENDER half of the boundary (Wave B): validate the plan,
// map it onto the renderinggen.overlay-plan.v1 contract, resolve + prefetch its
// content-addressed assets and enqueue the remote render. It returns as soon as
// the remote job is ACCEPTED — it never waits for the render, which is what
// lets the caller release its worker slot.
//
// Idempotent by construction: the remote job id is plan.RunID, so a retried
// submit addresses the same remote job (the queue answers 409 → ErrJobExists)
// and a FAILED remote job is reset to pending rather than left stuck.
func (e *ClipRenderExecutor) Submit(ctx context.Context, plan cliprender.ClipRenderPlanV1) error {
	if e == nil || e.queue == nil {
		return fmt.Errorf("%w: RenderingGen queue is not configured", cliprender.ErrBackendUnavailable)
	}
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("renderinggen clip executor: validate plan: %w", err)
	}
	if requested, alignment, ok := e.chunkPolicy(plan); ok {
		if _, err := e.SubmitChunked(ctx, plan, requested, alignment); err != nil {
			return fmt.Errorf("renderinggen clip executor: submit chunk family: %w", err)
		}
		return nil
	}
	// MapClipPlanToOverlayPlan produces the renderinggen.overlay-plan.v1
	// semantic contract. Sending a raw ClipRenderPlanV1 (no schema_version)
	// would cause the RenderingGen worker to treat it as a Chronon concrete
	// plan pass-through — a silent data corruption. This function is the
	// single authoritative serialisation point for clip render jobs.
	rawPlan, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		return fmt.Errorf("renderinggen clip executor: map plan: %w", err)
	}
	// Build hash-addressed asset refs. LogicalPath uses the content-addressed
	// object-store key so remote RenderingGen workers can materialise each
	// asset by hash — local VPS paths are never forwarded to the queue.
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		return fmt.Errorf("renderinggen clip executor: asset refs: %w", err)
	}
	if err := prefetchClipAssets(ctx, plan, refs); err != nil {
		return fmt.Errorf("renderinggen clip executor: prefetch assets: %w", err)
	}
	assets := make([]queueclient.AssetRef, len(refs))
	for i, r := range refs {
		assets[i] = queueclient.AssetRef{Hash: r.Hash, LogicalPath: r.LogicalPath}
	}
	submitErr := e.queue.Submit(ctx, scriptgen.RenderQueueJob{ID: plan.RunID, JobType: "render_segment", OverlaySpec: rawPlan, Assets: scriptAssets(assets)})
	if submitErr != nil && !errors.Is(submitErr, scriptgen.ErrJobExists) {
		return fmt.Errorf("renderinggen clip executor: submit: %w", submitErr)
	}
	if submitErr != nil && errors.Is(submitErr, scriptgen.ErrJobExists) {
		// A failed re-run of the same clip must not leave the job stuck in
		// FAILED while the caller believes a render is underway. The rule is
		// owned by the capability (scriptgen.RearmFailedRenderJob), so the
		// overlay enqueuer and this executor cannot answer it differently.
		if rearmErr := scriptgen.RearmFailedRenderJob(ctx, e.queue, plan.RunID); rearmErr != nil {
			return fmt.Errorf("renderinggen clip executor: %w", rearmErr)
		}
	}
	return nil
}

// chunkPolicy is transport optimisation only: a single source-video graph is
// safe for packet-copy assembly. Watermarks, burned subtitles, backgrounds,
// overlays and non-full-frame scaling require FullGraph and remain exclusive.
func (e *ClipRenderExecutor) chunkPolicy(plan cliprender.ClipRenderPlanV1) (int, int64, bool) {
	if e == nil || e.chunkDuration <= 0 || e.chunkAlignment <= 0 || e.chunkMax < 2 {
		return 0, 0, false
	}
	if plan.Background != nil && plan.Background.Mode != "" && plan.Background.Mode != cliprender.BackgroundModeNone {
		return 0, 0, false
	}
	if plan.Watermark != nil || plan.Overlay != nil {
		return 0, 0, false
	}
	if plan.Subtitles != nil && plan.Subtitles.Mode != "sidecar" {
		return 0, 0, false
	}
	if plan.Output.ForegroundScalePercent != 0 && plan.Output.ForegroundScalePercent != 100 {
		return 0, 0, false
	}
	chunkMS := e.chunkDuration.Milliseconds()
	if chunkMS <= 0 || plan.DurationMS <= chunkMS {
		return 0, 0, false
	}
	requested := int((plan.DurationMS + chunkMS - 1) / chunkMS)
	if requested > e.chunkMax {
		requested = e.chunkMax
	}
	if requested < 2 {
		return 0, 0, false
	}
	return requested, e.chunkAlignment, true
}

// SubmitChunked is the explicit I1 opt-in. It requires the queue's atomic
// batch capability and therefore cannot silently downgrade to N independent
// submissions (which would reintroduce the anchor-claim race).
func (e *ClipRenderExecutor) SubmitChunked(ctx context.Context, plan cliprender.ClipRenderPlanV1, requestedChunks int, alignmentFrames int64) (cliprender.ChunkSet, error) {
	if e == nil || e.queue == nil {
		return cliprender.ChunkSet{}, fmt.Errorf("%w: RenderingGen queue is not configured", cliprender.ErrBackendUnavailable)
	}
	batch, ok := e.queue.(scriptgen.RenderQueueBatchSubmitter)
	if !ok {
		return cliprender.ChunkSet{}, fmt.Errorf("renderinggen clip executor: queue does not support atomic chunk families")
	}
	producer, err := NewChunkProducer(batch)
	if err != nil {
		return cliprender.ChunkSet{}, err
	}
	set, err := cliprender.BuildChunkSet(plan, requestedChunks, alignmentFrames)
	if err != nil {
		return cliprender.ChunkSet{}, fmt.Errorf("renderinggen clip executor: plan chunks: %w", err)
	}
	if _, err = producer.Submit(ctx, plan, requestedChunks, alignmentFrames); err == nil {
		return set, nil
	}
	if !errors.Is(err, scriptgen.ErrJobExists) {
		return cliprender.ChunkSet{}, err
	}
	if rearmErr := rearmChunkFamily(ctx, e.queue, set.AnchorJobID(), set.Chunks); rearmErr != nil {
		return cliprender.ChunkSet{}, rearmErr
	}
	return set, nil
}

func rearmChunkFamily(ctx context.Context, queue scriptgen.RenderQueueClient, anchorID string, chunks []cliprender.Chunk) error {
	childrenReader, ok := queue.(scriptgen.RenderQueueChildrenReader)
	if !ok {
		return fmt.Errorf("renderinggen chunk family already exists but queue cannot inspect children")
	}
	retrier, ok := queue.(scriptgen.RenderQueueRetrier)
	if !ok {
		return fmt.Errorf("renderinggen chunk family already exists but queue cannot retry failed members")
	}
	parent, err := queue.Get(ctx, anchorID)
	if err != nil {
		return fmt.Errorf("renderinggen chunk family parent: %w", err)
	}
	if parent.State == string(queueclient.StateFailed) {
		return fmt.Errorf("renderinggen chunk family anchor %s is failed; finalizer retry requires operator action", parent.ID)
	}
	children, err := childrenReader.Children(ctx, anchorID)
	if err != nil {
		return err
	}
	if len(children) != len(chunks) {
		return fmt.Errorf("renderinggen chunk family has %d children, want %d", len(children), len(chunks))
	}
	for _, child := range children {
		if child.State != string(queueclient.StateFailed) {
			continue
		}
		if err := retrier.Retry(ctx, child.ID); err != nil {
			return fmt.Errorf("renderinggen retry chunk %s: %w", child.ID, err)
		}
	}
	return nil
}

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

// SettleWithJobID is the restart-safe settle path for chunk families whose
// assembly anchor is derived from the sealed plan digest rather than RunID.
func (e *ClipRenderExecutor) SettleWithJobID(ctx context.Context, plan cliprender.ClipRenderPlanV1, renderJobID string) (*cliprender.RenderOutcome, error) {
	if e == nil || e.queue == nil {
		return nil, fmt.Errorf("%w: RenderingGen queue is not configured", cliprender.ErrBackendUnavailable)
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("renderinggen clip executor: validate plan: %w", err)
	}
	if strings.TrimSpace(renderJobID) == "" {
		return nil, fmt.Errorf("renderinggen clip executor: remote render job id is required")
	}
	// The wait is the canonical capability wait (scriptgen.
	// WaitRenderQueueTerminal), not a local copy: the settle continuation and
	// the overlay enqueue path must agree on what "terminal" means.
	completed, _, err := scriptgen.WaitRenderQueueTerminal(ctx, e.queue, renderJobID, e.interval)
	if err != nil {
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

// Materialize is the LAZY half of the locator-first boundary: it downloads the
// certified artifact located by outcome into destPath, verifying size + digest
// while streaming. It is what a consumer that genuinely needs a local file
// (localization today) calls; the canonical clip.render completion path never
// does, because it commits the locator and the Drive outbox streams from the
// object store. Fail-closed: an outcome with no locator, or a fetch that does
// not match the certified size/digest, is a typed error.
func (e *ClipRenderExecutor) Materialize(ctx context.Context, outcome *cliprender.RenderOutcome, destPath string) (string, error) {
	if outcome == nil {
		return "", fmt.Errorf("renderinggen clip executor: materialize: outcome is nil")
	}
	if strings.TrimSpace(outcome.ArtifactURL) == "" {
		return "", fmt.Errorf("renderinggen clip executor: materialize: outcome carries no artifact URL")
	}
	if strings.TrimSpace(destPath) == "" {
		return "", fmt.Errorf("renderinggen clip executor: materialize: destination path is required")
	}
	sha, size, err := materializeArtifact(ctx, outcome.ArtifactURL, destPath, outcome.SizeBytes, outcome.SHA256)
	if err != nil {
		return "", fmt.Errorf("renderinggen clip executor: materialize certified artifact: %w", err)
	}
	outcome.OutputPath = destPath
	if sha != "" {
		outcome.SHA256 = sha
	}
	if size > 0 {
		outcome.SizeBytes = size
	}
	return destPath, nil
}

var _ cliprender.RenderArtifactMaterializer = (*ClipRenderExecutor)(nil)

// materializeArtifact fetches the certified object-store artifact into a local
// file. It is the shared body of the lazy Materialize path (the eager Settle
// download it used to serve is gone). Single-pass: hashes while streaming
// (network → disk + SHA-256 in one pass, no re-read).
//
// It returns the CERTIFIED digest and byte count of the materialized file:
// the digest is computed from the exact bytes written to disk in the same
// io.Copy that produced them and verified against the queue's expected
// digest, so downstream publication reuses it instead of re-reading the
// artifact (the previous Seek(0)+SHA256Reader form doubled the disk I/O of
// every certified download and forced the publisher into a third read).
func materializeArtifact(ctx context.Context, rawURL, outputPath string, expectedSize int64, expectedSHA string) (string, int64, error) {
	if rawURL == "" || outputPath == "" {
		return "", 0, fmt.Errorf("artifact URL and output path are required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", 0, fmt.Errorf("create output directory: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := objectStoreHTTPClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", 0, fmt.Errorf("artifact download HTTP %d", resp.StatusCode)
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, err
	}
	hasher := digest.NewSHA256()
	written, copyErr := io.Copy(io.MultiWriter(file, hasher), resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	gotSHA := hex.EncodeToString(hasher.Sum(nil))
	if expectedSize > 0 && written != expectedSize {
		return "", 0, fmt.Errorf("downloaded size %d, want %d", written, expectedSize)
	}
	if expectedSHA != "" && !strings.EqualFold(gotSHA, expectedSHA) {
		return "", 0, fmt.Errorf("artifact hash %s, want %s", gotSHA, expectedSHA)
	}
	return gotSHA, written, nil
}

// prefetchClipAssets publishes the already-resolved local assets to the
// content-addressed RenderingGen object store. The queue carries hashes, not
// VPS paths; without this boundary a remote/native worker cannot materialize
// the source and subtitle files. This is deliberately before enqueue so a
// job is never claimed with an incomplete asset set.
//
// Each asset is staged with a HEAD-before-PUT probe: an object that already
// exists under its content address is shared with every worker, so re-reading
// and re-uploading it would burn bandwidth, a full RAM buffer and a disk pass
// for zero benefit. Only absent objects are uploaded, streamed straight from
// the source file (constant memory — the historical os.ReadFile path buffered
// every asset in RAM). The declared SHA-256s are certified upstream by the
// plan resolver, so no full-file hash pass is repeated at this boundary; the
// RenderingGen worker independently re-verifies the content address when it
// materializes the asset, so wrong bytes can never reach Chronon.
func prefetchClipAssets(ctx context.Context, plan cliprender.ClipRenderPlanV1, refs []assetRef) error {
	store := strings.TrimRight(os.Getenv("RENDERINGGEN_STORE_URL"), "/")
	if store == "" {
		store = "http://127.0.0.1:9000"
	}
	paths := map[string]string{plan.Source.SHA256: plan.Source.Path}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset {
		paths[plan.Background.SHA256] = plan.Background.Path
	}
	if plan.Subtitles != nil {
		paths[plan.Subtitles.SHA256] = plan.Subtitles.Path
	}
	if plan.Watermark != nil && plan.Watermark.SHA256 != "" {
		paths[plan.Watermark.SHA256] = plan.Watermark.Path
	}
	// The source, background, subtitle and watermark/font objects are
	// independent. Probe/upload them concurrently, but keep a small bound so
	// one render cannot monopolise the object-store connection pool. This is
	// the clip-render equivalent of the shared prefetcher's four-transfer cap.
	var group errgroup.Group
	group.SetLimit(4)
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref := ref
		key := strings.ToLower(strings.TrimSpace(ref.Hash))
		if key == "" {
			continue
		}
		// Deduplicate by content address (never by name/path): the same bytes
		// reachable from several plan slots (e.g. a font referenced by both the
		// watermark and the burn-in subtitles) are staged exactly once.
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		group.Go(func() error {
			path := paths[ref.Hash]
			if path == "" {
				// overlayPlanAssets carries a LocalPath for assets it reads directly
				// (subtitle/watermark fonts); the paths map above only knows the
				// plan-owned files. Prefer the ref's own source so a Poppins
				// subtitle font is uploaded like any other font.
				path = ref.LocalPath
			}
			if path == "" {
				return fmt.Errorf("asset %s has no resolved local path", ref.Hash)
			}
			present, err := objectStored(ctx, store, ref.Hash)
			if err != nil {
				return fmt.Errorf("asset %s probe: %w", ref.Hash, err)
			}
			if present {
				return nil
			}
			if err := streamPutFile(ctx, store, ref.Hash, path); err != nil {
				return fmt.Errorf("upload %s: %w", ref.Hash, err)
			}
			return nil
		})
	}
	return group.Wait()
}

func boolPtr(b bool) *bool { return &b }

func scriptAssets(in []queueclient.AssetRef) []scriptgen.RenderQueueAsset {
	out := make([]scriptgen.RenderQueueAsset, len(in))
	for i, a := range in {
		out[i] = scriptgen.NewRenderQueueAsset(
			kernelasset.Ref{AssetID: a.LogicalPath, SHA256: a.Hash}, a.LogicalPath, a.SourceURL)
	}
	return out
}

// waitClipQueue used to live here: a second implementation of "wait for the
// remote render to reach a terminal state", with its own jitter, its own
// interval clamps and no wait metrics. It is DELETED — the canonical wait is
// scriptgen.WaitRenderQueueTerminal, called from ClipRenderExecutor.Settle.

var (
	_ scriptgen.RenderQueueClient         = (*Client)(nil)
	_ scriptgen.RenderQueueBatchSubmitter = (*Client)(nil)
	_ scriptgen.RenderQueueWaiter         = (*Client)(nil)
	_ scriptgen.RenderQueueRetrier        = (*Client)(nil)
	_ cliprender.RenderExecutor           = (*ClipRenderExecutor)(nil)
)
