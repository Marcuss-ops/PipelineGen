// Package renderinggen adapts the central RenderingGen queue's public client
// to the script-generation capability. PipelineGen no longer owns the HTTP
// contract: it delegates to github.com/Marcuss-ops/RenderginGen/queue/client
// and only maps between the capability domain types and the queue's wire
// types, so the wire format can never drift between the two codebases.
package renderinggen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
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
		ID:         job.ID,
		JobType:    job.JobType,
		RenderPlan: job.OverlaySpec,
		Assets:     toQueueAssets(job.Assets),
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
	return scriptgen.RenderQueueJob{
		ID:          job.ID,
		OverlaySpec: job.RenderPlan,
		Assets:      fromQueueAssets(job.Assets),
		State:       string(job.State),
		FailReason:  job.FailReason,
		Artifact:    toScriptArtifact(job.Artifact),
	}, nil
}

func toQueueAssets(in []scriptgen.RenderQueueAsset) []queueclient.AssetRef {
	if in == nil {
		return nil
	}
	out := make([]queueclient.AssetRef, len(in))
	for i, a := range in {
		out[i] = queueclient.AssetRef{Hash: a.Hash, LogicalPath: a.URL}
	}
	return out
}

func fromQueueAssets(in []queueclient.AssetRef) []scriptgen.RenderQueueAsset {
	if in == nil {
		return nil
	}
	out := make([]scriptgen.RenderQueueAsset, len(in))
	for i, a := range in {
		out[i] = scriptgen.RenderQueueAsset{Hash: a.Hash, URL: a.LogicalPath}
	}
	return out
}

func toScriptArtifact(in *queueclient.Artifact) *scriptgen.RenderArtifact {
	if in == nil {
		return nil
	}
	return &scriptgen.RenderArtifact{
		ID:                 in.ID,
		Kind:               in.Kind,
		StorageKey:         in.StorageKey,
		URL:                in.ArtifactURL,
		SHA256:             in.ArtifactHash,
		MimeType:           in.ContentType,
		SizeBytes:          in.SizeBytes,
		Width:              in.Width,
		Height:             in.Height,
		FPSNum:             in.FPSNum,
		FPSDen:             in.FPSDen,
		FrameCount:         in.FrameCount,
		DurationUS:         in.DurationUS,
		ProfileID:          in.ProfileID,
		CopyEligible:       in.CopyEligible,
		Codec:              in.Codec,
		CodecProfile:       in.CodecProfile,
		ClosedGOP:          in.ClosedGOP,
		FirstFrameKeyframe: in.FirstFrameKeyframe,
		RenderMS:           metricMillis(in.Metrics, "render_ms"),
		EncodeMS:           metricMillis(in.Metrics, "encode_ms"),
		MaterializeMS:      metricMillisEither(in.Metrics, "materialize_ms", "materialize_us"),
		PlanMS:             metricMillisEither(in.Metrics, "overlay_compile_ms", "overlay_compile_us"),
		ProbeMS:            metricMillisEither(in.Metrics, "probe_ms", "probe_us"),
		HashMS:             metricMillisEither(in.Metrics, "sha256_ms", "sha256_us"),
		UploadMS:           metricMillisEither(in.Metrics, "objectstore_upload_ms", "objectstore_upload_us"),
		DrivePublishMS:     metricMillisEither(in.Metrics, "drive_publish_ms", "drive_upload_us"),
		Backend:            in.Backend,
		ChrononVersion:     in.ChrononVersion,
		DriveFileID:        in.DriveFileID,
		DriveLink:          in.DriveLink,
	}
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

// ClipRenderQueue is the minimal public queue seam used by clip rendering.
type ClipRenderQueue interface {
	Submit(context.Context, scriptgen.RenderQueueJob) error
	Get(context.Context, string) (scriptgen.RenderQueueJob, error)
}

// ClipRenderExecutor submits one complete clip segment to RenderingGen. The
// RenderingGen worker is the sole clip-rendering boundary; it lowers the
// semantic plan and executes it with Chronon. This client never invokes a
// local renderer and fails closed on missing or non-Chronon artifacts.
type ClipRenderExecutor struct {
	queue    ClipRenderQueue
	interval time.Duration
}

func NewClipRenderExecutor(queue ClipRenderQueue) (*ClipRenderExecutor, error) {
	if queue == nil {
		return nil, fmt.Errorf("renderinggen clip executor: queue client is required")
	}
	return &ClipRenderExecutor{queue: queue, interval: 2 * time.Second}, nil
}

func (e *ClipRenderExecutor) SetPollInterval(interval time.Duration) *ClipRenderExecutor {
	if e != nil && interval > 0 {
		e.interval = interval
	}
	return e
}

func (e *ClipRenderExecutor) Render(ctx context.Context, plan cliprender.ClipRenderPlanV1) (*cliprender.RenderOutcome, error) {
	if e == nil || e.queue == nil {
		return nil, fmt.Errorf("%w: RenderingGen queue is not configured", cliprender.ErrBackendUnavailable)
	}
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("renderinggen clip executor: validate plan: %w", err)
	}
	// MapClipPlanToOverlayPlan produces the renderinggen.overlay-plan.v1
	// semantic contract. Sending a raw ClipRenderPlanV1 (no schema_version)
	// would cause the RenderingGen worker to treat it as a Chronon concrete
	// plan pass-through — a silent data corruption. This function is the
	// single authoritative serialisation point for clip render jobs.
	rawPlan, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		return nil, fmt.Errorf("renderinggen clip executor: map plan: %w", err)
	}
	// Build hash-addressed asset refs. LogicalPath uses the content-addressed
	// object-store key so remote RenderingGen workers can materialise each
	// asset by hash — local VPS paths are never forwarded to the queue.
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		return nil, fmt.Errorf("renderinggen clip executor: asset refs: %w", err)
	}
	if err := prefetchClipAssets(ctx, plan, refs); err != nil {
		return nil, fmt.Errorf("renderinggen clip executor: prefetch assets: %w", err)
	}
	assets := make([]queueclient.AssetRef, len(refs))
	for i, r := range refs {
		assets[i] = queueclient.AssetRef{Hash: r.Hash, LogicalPath: r.LogicalPath}
	}
	if err := e.queue.Submit(ctx, scriptgen.RenderQueueJob{ID: plan.RunID, JobType: "render_segment", OverlaySpec: rawPlan, Assets: scriptAssets(assets)}); err != nil && !errors.Is(err, scriptgen.ErrJobExists) {
		return nil, fmt.Errorf("renderinggen clip executor: submit: %w", err)
	}
	completed, err := waitClipQueue(ctx, e.queue, plan.RunID, e.interval)
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
	// VideoZeroCopy stays nil unless RenderingGen/Chronon explicitly
	// certifies it. PipelineGen must never infer this property from backend
	// identity; require_zero_copy therefore fails closed when certification is
	// absent.
	return &cliprender.RenderOutcome{OutputPath: a.URL, SizeBytes: a.SizeBytes, DurationSec: float64(a.DurationUS) / 1e6, Width: uint32(a.Width), Height: uint32(a.Height), FPSNum: uint32(a.FPSNum), FPSDen: uint32(a.FPSDen), Backend: cliprender.RenderBackend(a.Backend), AudioCopyEligible: boolPtr(a.CopyEligible), Metrics: cliprender.NewRenderMetricsV2()}, nil
}

// prefetchClipAssets publishes the already-resolved local assets to the
// content-addressed RenderingGen object store. The queue carries hashes, not
// VPS paths; without this boundary a remote/native worker cannot materialize
// the source and subtitle files. This is deliberately before enqueue so a
// job is never claimed with an incomplete asset set.
func prefetchClipAssets(ctx context.Context, plan cliprender.ClipRenderPlanV1, refs []assetRef) error {
	store := strings.TrimRight(os.Getenv("RENDERINGGEN_STORE_URL"), "/")
	if store == "" {
		store = "http://127.0.0.1:9000"
	}
	paths := map[string]string{plan.Source.SHA256: plan.Source.Path}
	if font, err := watermarkFontAsset(); err == nil {
		paths[font.Hash] = font.LocalPath
	}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset {
		paths[plan.Background.SHA256] = plan.Background.Path
	}
	if plan.Subtitles != nil {
		paths[plan.Subtitles.SHA256] = plan.Subtitles.Path
	}
	if plan.Watermark != nil && plan.Watermark.SHA256 != "" {
		paths[plan.Watermark.SHA256] = plan.Watermark.Path
	}
	for _, ref := range refs {
		path := paths[ref.Hash]
		if path == "" {
			return fmt.Errorf("asset %s has no resolved local path", ref.Hash)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		got := digest.SHA256Bytes(data)
		if !strings.EqualFold(got, ref.Hash) {
			return fmt.Errorf("asset hash mismatch for %s: got %s", path, got)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, store+"/objects/"+ref.Hash, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.ContentLength = int64(len(data))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("upload %s: %w", ref.Hash, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("upload %s: HTTP %d", ref.Hash, resp.StatusCode)
		}
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }

func scriptAssets(in []queueclient.AssetRef) []scriptgen.RenderQueueAsset {
	out := make([]scriptgen.RenderQueueAsset, len(in))
	for i, a := range in {
		out[i] = scriptgen.RenderQueueAsset{Hash: a.Hash, URL: a.LogicalPath}
	}
	return out
}

func waitClipQueue(ctx context.Context, q ClipRenderQueue, id string, interval time.Duration) (scriptgen.RenderQueueJob, error) {
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		job, err := q.Get(ctx, id)
		if err != nil {
			return scriptgen.RenderQueueJob{}, err
		}
		if job.State == string(queueclient.StateCompleted) || job.State == string(queueclient.StateFailed) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return scriptgen.RenderQueueJob{}, ctx.Err()
		case <-t.C:
		}
	}
}

var _ scriptgen.RenderQueueClient = (*Client)(nil)
var _ cliprender.RenderExecutor = (*ClipRenderExecutor)(nil)
