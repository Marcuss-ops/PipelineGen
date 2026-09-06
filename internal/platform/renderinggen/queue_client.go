// Package renderinggen adapts the central RenderingGen queue's public client
// to the script-generation capability. PipelineGen no longer owns the HTTP
// contract: it delegates to github.com/Marcuss-ops/RenderginGen/queue/client
// and only maps between the capability domain types and the queue's wire
// types, so the wire format can never drift between the two codebases.
package renderinggen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

// objectStoreHTTPClient bounds every object-store HTTP call made by this
// adapter (asset prefetch uploads and certified-artifact downloads). Without a
// timeout a hanging object store would pin the caller — and the lease it holds
// — forever; the worker-side clients use the same horizon.
var objectStoreHTTPClient = &http.Client{Timeout: 5 * time.Minute}

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
		Metrics:            in.Metrics,
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
	Retry(context.Context, string) error
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
	submitErr := e.queue.Submit(ctx, scriptgen.RenderQueueJob{ID: plan.RunID, JobType: "render_segment", OverlaySpec: rawPlan, Assets: scriptAssets(assets)})
	if submitErr != nil && !errors.Is(submitErr, scriptgen.ErrJobExists) {
		return nil, fmt.Errorf("renderinggen clip executor: submit: %w", submitErr)
	}
	if submitErr != nil && errors.Is(submitErr, scriptgen.ErrJobExists) {
		if existing, getErr := e.queue.Get(ctx, plan.RunID); getErr == nil && existing.State == string(queueclient.StateFailed) {
			// A failed re-run of the same clip must not leave the job stuck in
			// FAILED while the caller believes a render is underway: surface a
			// retry failure instead of degrading into the generic
			// "completed without certified artifact" wait timeout.
			if retryErr := e.queue.Retry(ctx, plan.RunID); retryErr != nil {
				return nil, fmt.Errorf("renderinggen clip executor: retry failed for %s: %w", plan.RunID, retryErr)
			}
		}
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
	if err := materializeArtifact(ctx, a.URL, plan.OutputPath, a.SizeBytes, a.SHA256); err != nil {
		return nil, fmt.Errorf("renderinggen clip executor: materialize certified artifact: %w", err)
	}
	// VideoZeroCopy stays nil unless RenderingGen/Chronon explicitly
	// certifies it. PipelineGen must never infer this property from backend
	// identity; require_zero_copy therefore fails closed when certification is
	// absent.
	return &cliprender.RenderOutcome{
		OutputPath:        plan.OutputPath,
		SizeBytes:         a.SizeBytes,
		DurationSec:       float64(a.DurationUS) / 1e6,
		Width:             uint32(a.Width),
		Height:            uint32(a.Height),
		FPSNum:            uint32(a.FPSNum),
		FPSDen:            uint32(a.FPSDen),
		Backend:           cliprender.RenderBackend(a.Backend),
		AudioCopyEligible: boolPtr(a.CopyEligible),
		Metrics:           metricsFromChrononMetrics(a.Metrics, a.FrameCount, a.DurationUS),
	}, nil
}

// metricsFromChrononArtifact is the API bridge for the certified artifact.
// The queue already carries Chronon's flattened numeric projection and the
// full JSONB-backed telemetry document. Never replace it with a fresh empty
// report: doing so silently turns real engine measurements into zeroes in the
// localized_renders API payload.
func metricsFromChrononMetrics(n map[string]float64, frameCount int, durationUS int64) *cliprender.RenderMetricsV2 {
	m := cliprender.NewRenderMetricsV2()
	metric := func(keys ...string) (float64, bool) {
		for _, key := range keys {
			if v, ok := n[key]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
				return v, true
			}
		}
		return 0, false
	}
	set := func(dst *cliprender.Metric, keys ...string) {
		if v, ok := metric(keys...); ok {
			*dst = cliprender.Metric(math.Round(v))
		}
	}
	setFloat := func(dst *float64, keys ...string) {
		if v, ok := metric(keys...); ok {
			*dst = v
		}
	}

	// Queue/worker walls and the Chronon exclusive timeline.
	set(&m.RendererStartupMS, "chronon_exclusive_wall_timeline_startup_ms")
	set(&m.ProbeMS, "chronon_exclusive_wall_timeline_ffprobe_ms")
	set(&m.DecodeMS, "chronon_job_gpu_video_decode_wall_ms", "chronon_job_gpu_decode_submit_ms")
	set(&m.CompositeMS, "chronon_job_gpu_cuda_composite_wall_us")
	if int64(m.CompositeMS) != cliprender.NotInstrumented {
		m.CompositeMS = cliprender.Metric(math.Round(float64(m.CompositeMS) / 1000))
	}
	set(&m.EncodeMS, "chronon_job_encoder_finalize_ms", "chronon_exclusive_wall_timeline_encoder_drain_finalize_ms")
	set(&m.RenderWallMS, "chronon_exclusive_wall_timeline_render_loop_ms")
	set(&m.RenderWallMS, "chronon_job_render_loop_wall_ms")

	// Receipt verification phases from Chronon's `<output>.receipt.json`
	// timing_ms. decode/count_frames run only under the normal/certify
	// policy; under the default fast policy they stay NOT_INSTRUMENTED while
	// probe/sha256/total carry the metadata-only receipt cost. The resolved
	// policy + aggregate status label the run (fast=1/normal=2/certify=3)
	// instead of inferring the policy from the presence of decode timings.
	set(&m.ReceiptSHA256MS, "chronon_receipt_sha256_ms")
	set(&m.ReceiptProbeMS, "chronon_receipt_probe_ms")
	set(&m.ReceiptCountFramesMS, "chronon_receipt_count_frames_ms")
	set(&m.ReceiptDecodeMS, "chronon_receipt_decode_ms")
	set(&m.ReceiptTotalMS, "chronon_receipt_total_ms")
	set(&m.VerificationPolicy, "chronon_receipt_verification_policy")
	set(&m.VerificationPassed, "chronon_receipt_verification_status")

	// GPU counters and resource profile.
	set(&m.GPUUploadBytes, "chronon_job_gpu_gpu_upload_bytes", "chronon_job_gpu_upload_bytes")
	set(&m.GPUReadbackBytes, "chronon_job_gpu_gpu_readback_bytes", "chronon_job_gpu_gpu_readback_bytes")
	set(&m.EncoderStagingCopyBytes, "chronon_job_gpu_encoder_staging_copy_bytes")
	set(&m.VRAMUsedPeakMB, "chronon_job_hardware_vram_used_peak_mb")
	set(&m.GPUUtilizationAvg, "chronon_job_hardware_gpu_utilization_avg")
	set(&m.GPUUtilizationPeak, "chronon_job_hardware_gpu_utilization_peak")
	set(&m.NVENCUtilizationAvg, "chronon_job_hardware_nvenc_utilization_avg")
	set(&m.NVDECUtilizationAvg, "chronon_job_hardware_nvdec_utilization_avg")
	set(&m.CUDACompositeFrames, "chronon_job_gpu_cuda_composite_frames")
	set(&m.NV12ToRGBAFrames, "chronon_job_gpu_nv12_to_rgba_frames")
	set(&m.RGBAToNV12Frames, "chronon_job_gpu_rgba_to_nv12_frames")

	// Artifact facts are authoritative for the output frame count. Summary
	// values are authoritative for engine throughput; they are not recomputed
	// from the worker wall, which would mix queue/download/publish time in.
	if frameCount > 0 {
		m.Frames = frameCount
	}
	setFloat(&m.RenderFPS, "chronon_summary_render_loop_fps", "chronon_summary_render_only_fps")
	setFloat(&m.TotalFPS, "chronon_summary_end_to_end_fps")
	setFloat(&m.RealtimeFactor, "chronon_summary_realtime_factor")
	if m.RealtimeFactor != 0 {
		m.SpeedFactor = m.RealtimeFactor
	}
	if m.RenderWallMS != cliprender.Metric(cliprender.NotInstrumented) && durationUS > 0 {
		m.ProcessingXRT = float64(m.RenderWallMS) / 1000 / (float64(durationUS) / 1e6)
	}
	return m
}

// materializeArtifact turns the queue's certified object-store URL into the
// local run artifact expected by the clip.render pipeline. OutputPath is a
// filesystem path throughout that pipeline; leaking the queue URL past this
// adapter makes probing and final publication try to open an HTTP URL as a
// local file.
func materializeArtifact(ctx context.Context, rawURL, outputPath string, expectedSize int64, expectedSHA string) error {
	if rawURL == "" || outputPath == "" {
		return fmt.Errorf("artifact URL and output path are required")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := objectStoreHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("artifact download HTTP %d", resp.StatusCode)
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if expectedSize > 0 && written != expectedSize {
		return fmt.Errorf("downloaded size %d, want %d", written, expectedSize)
	}
	gotSHA, gotSize, err := digest.SHA256File(outputPath)
	if err != nil {
		return fmt.Errorf("hash downloaded artifact: %w", err)
	}
	if expectedSize > 0 && gotSize != expectedSize {
		return fmt.Errorf("hashed size %d, want %d", gotSize, expectedSize)
	}
	if expectedSHA != "" && !strings.EqualFold(gotSHA, expectedSHA) {
		return fmt.Errorf("artifact hash %s, want %s", gotSHA, expectedSHA)
	}
	return nil
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
		present, err := objectStored(ctx, store, ref.Hash)
		if err != nil {
			return fmt.Errorf("asset %s probe: %w", ref.Hash, err)
		}
		if present {
			continue
		}
		if err := streamPutFile(ctx, store, ref.Hash, path); err != nil {
			return fmt.Errorf("upload %s: %w", ref.Hash, err)
		}
	}
	return nil
}

// objectStored reports whether the object store already holds an object under
// key, using HEAD so no bytes cross the wire for present objects. A 404 (and
// a 405 from a store without HEAD) means absent; any transport error or other
// status fails closed so a job is never enqueued against an unverifiable
// store.
func objectStored(ctx context.Context, store, key string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, store+"/objects/"+key, nil)
	if err != nil {
		return false, err
	}
	resp, err := objectStoreHTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("head %s: %w", key, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return false, nil
	default:
		return false, fmt.Errorf("head %s: HTTP %d", key, resp.StatusCode)
	}
}

// streamPutFile uploads a local file to the object store without loading it
// into RAM: the file handle is the request body and its size is declared up
// front, so the transport streams it straight from disk.
func streamPutFile(ctx context.Context, store, key, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, store+"/objects/"+key, file)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	resp, err := objectStoreHTTPClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
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
