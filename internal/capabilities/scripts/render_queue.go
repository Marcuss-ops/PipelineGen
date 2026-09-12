package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// ErrJobExists is returned by RenderQueueClient.Submit when a job with the
// same ID was already enqueued. The queue enqueuer treats this as success and
// proceeds to wait on the existing job, making retries idempotent.
var ErrJobExists = errors.New("render job already exists")

// defaultQueuePollInterval is how long the queue enqueuer waits between
// status polls while the render is in flight. It is only exercised by the
// polling fallback: the primary path is the queue's job-status long poll
// (RenderQueueWaiter), which observes a terminal job at the transition. The
// fallback cadence is kept short so an older queue deployment without the
// wait route does not reintroduce multi-second tail latency.
const defaultQueuePollInterval = 250 * time.Millisecond

// RenderQueueAsset points at an input asset the central queue worker must
// fetch. Hash is the object-store lookup key (the SHA-256 of the file).
type RenderQueueAsset struct {
	Hash      string `json:"hash"`
	URL       string `json:"url,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	// LocalPath is producer-side only. The adapter stages it into the object
	// store and omits it from the RenderingGen wire asset reference.
	LocalPath string `json:"-"`
}

// RenderQueueJob is the queue-side view of a submitted render job. It is the
// wire contract with the central RenderingGen queue (POST /jobs and
// GET /jobs/{id}). JobType is the canonical overlay job type
// (overlay.prepare / overlay.render) the queue worker dispatches on.
type RenderQueueJob struct {
	ID          string             `json:"id"`
	JobType     string             `json:"job_type,omitempty"`
	OverlaySpec json.RawMessage    `json:"overlay_spec"`
	Assets      []RenderQueueAsset `json:"assets"`
	State       string             `json:"state"`
	FailReason  string             `json:"fail_reason,omitempty"`
	Artifact    *RenderArtifact    `json:"artifact,omitempty"`
}

// RenderQueueClient is the narrow port for the central RenderingGen queue.
// The capability stays independent of HTTP; the concrete client lives in
// internal/platform/renderinggen.
type RenderQueueClient interface {
	// Submit enqueues a job. It returns ErrJobExists when a job with the
	// same ID is already present (idempotent replay).
	Submit(ctx context.Context, job RenderQueueJob) error
	// Get returns the current state of a job, including its artifact once
	// the job completes.
	Get(ctx context.Context, id string) (RenderQueueJob, error)
}

// RenderQueueWaiter is the optional event-driven completion capability. A
// queue client that implements it lets the enqueuer observe a terminal render
// at the state transition instead of sampling the job on a cadence: the
// RenderingGen queue exposes GET /jobs/{id}/wait and the adapter blocks on it.
// Clients that do not implement it (older deployments, test doubles) keep the
// polling loop, so the capability is additive and never required.
type RenderQueueWaiter interface {
	// WaitTerminal blocks until the job reaches a terminal state (completed,
	// failed or cancelled) or ctx ends, and returns the last observed job.
	WaitTerminal(ctx context.Context, id string) (RenderQueueJob, error)
}

// QueueRenderEnqueuer adapts the central RenderingGen queue for the Chronon
// overlay render path. It submits the SEMANTIC OverlayPlan; RenderingGen is the
// sole owner of the semantic→chronon.render-plan.v2 lowering, and blocks until
// the render completes, returning the certified artifact reference. The
// removed video render enqueue path is no longer part of PipelineGen.
// RenderCompletionMetrics separates the worker-reported Chronon duration from
// the client-side wait used to observe the queue. PollingSleep is the time
// deliberately spent sleeping between status requests, so it is the direct
// measurable impact of the polling cadence (and not Chronon work).
type RenderCompletionMetrics struct {
	CompletionWait time.Duration
	PollingSleep   time.Duration
	PollInterval   time.Duration
	PollCount      int
}

type QueueRenderEnqueuer struct {
	client       RenderQueueClient
	pollInterval time.Duration
	publisher    OverlayArtifactPublisher
	// freshRender forces each submission through this enqueuer to receive a
	// new queue identity. It is used for overlay artifacts: input assets are
	// reusable, but the rendered video must always be produced by a new
	// Chronon call. The flag is per-instance: only the enqueuer wired to the
	// overlay render path enables it; idempotent callers keep the default
	// plan-id identity and the ErrJobExists recovery path.
	freshRender bool
	// freshSeq is the per-instance monotonic counter that disambiguates the
	// fresh queue identity. It replaces the former package-level global so
	// concurrent enqueuers (and tests) cannot interfere with one another.
	freshSeq atomic.Uint64
	// recorder optionally persists one analytics row per completed attempt.
	// Nil means analytics are not recorded (no-op, not a failure).
	recorder RenderAttemptRecorder
}

// NewQueueRenderEnqueuer creates a queue-backed Chronon render enqueuer.
func NewQueueRenderEnqueuer(client RenderQueueClient) (*QueueRenderEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue render enqueuer requires a queue client")
	}
	return &QueueRenderEnqueuer{client: client, pollInterval: defaultQueuePollInterval}, nil
}

// SetPollInterval tunes the observation cadence independently from the
// RenderingGen worker. A short interval removes avoidable tail latency after
// Chronon finishes; a caller may leave it unset to retain the safe default.
func (e *QueueRenderEnqueuer) SetPollInterval(interval time.Duration) {
	if e != nil && interval > 0 {
		e.pollInterval = interval
	}
}

// SetRecorder attaches the optional analytics recorder. Production
// composition injects the SQLite-backed recorder; tests may inject a fake or
// leave it nil to skip analytics.
func (e *QueueRenderEnqueuer) SetRecorder(r RenderAttemptRecorder) {
	if e == nil {
		return
	}
	e.recorder = r
}

// SetArtifactPublisher attaches the required post-render publication side
// effect. It is optional for unit-test compositions and enabled by the
// production composition root when Drive is configured.
func (e *QueueRenderEnqueuer) SetArtifactPublisher(p OverlayArtifactPublisher) {
	if e != nil {
		e.publisher = p
	}
}

// SetFreshRender controls whether EnqueueChrononPlan creates a new queue job
// for every call. Overlay production enables this so a completed job from an
// older test or retry can never be returned as the current render artifact.
// The default remains false for callers that explicitly rely on queue-level
// idempotency and ErrJobExists recovery. The returned RenderReference.JobID
// and the analytics attempt_id always carry the real queue job id.
func (e *QueueRenderEnqueuer) SetFreshRender(on bool) {
	if e != nil {
		e.freshRender = on
	}
}

// EnqueueChrononPlan submits the semantic OverlayPlan to RenderingGen. The
// worker is the sole owner of the semantic→Chronon v2 compilation and writes
// the concrete plan it actually executes. Keeping this boundary semantic is
// important: sending PipelineGen's old v1 concrete document makes the worker
// validate it against the v2 schema and fail before Chronon starts.
func (e *QueueRenderEnqueuer) EnqueueChrononPlan(ctx context.Context, plan capoverlay.OverlayPlan) (RenderReference, error) {
	if e == nil || e.client == nil {
		return RenderReference{}, fmt.Errorf("queue render enqueuer is not configured")
	}
	semanticPlan := plan
	// SSOT: backgrounds are declared only through the plan's first-class
	// Background block. The legacy "item with template BACKGROUND" spelling
	// was removed — producers must set Background explicitly; the enqueue
	// boundary no longer rewrites the plan's items.
	spec, err := json.Marshal(semanticPlan)
	if err != nil {
		return RenderReference{}, fmt.Errorf("marshal semantic chronon plan: %w", err)
	}
	assets := make([]RenderQueueAsset, 0, len(plan.Items)+1)
	seen := make(map[string]struct{})
	addAsset := func(ref capoverlay.OverlayAssetRef) {
		if ref.SHA256 == "" {
			return
		}
		hash := strings.ToLower(ref.SHA256)
		if _, ok := seen[hash]; ok {
			return
		}
		seen[hash] = struct{}{}
		sourceURL := strings.TrimSpace(ref.URL)
		logicalPath := semanticAssetLogicalPath(ref)
		// RenderingGen's compiled Chronon plan addresses assets by its
		// canonical semantic path. Send that same path in the queue manifest
		// and preserve the provider URL separately for worker self-healing.
		// Without this identity match, a fresh Drive image can be downloaded
		// successfully but still be looked up under a different workspace path.
		asset := RenderQueueAsset{Hash: hash, URL: logicalPath}
		asset.LocalPath = ref.LocalPath
		if strings.HasPrefix(sourceURL, "http") {
			asset.SourceURL = sourceURL
		}
		assets = append(assets, asset)
	}
	if semanticPlan.Background != nil {
		for _, ref := range semanticPlan.Background.AssetRefs {
			addAsset(ref)
		}
	}
	for _, item := range semanticPlan.Items {
		for _, ref := range item.AssetRefs {
			addAsset(ref)
		}
	}
	// Chronon's VisualPresetRegistry preflights the preset-owned font before
	// applying a layer's explicit font_asset override. Keep both content-
	// addressed fonts in the queue payload: the explicit PipelineGen font
	// remains authoritative for the layer, while Poppins satisfies the
	// registry dependency during plan preparation.
	for _, item := range semanticPlan.Items {
		if item.Text != "" || item.TemplateID == "IMPORTANT_WORD" || item.TemplateID == "IMPORTANT_PHRASE" || item.TemplateID == "lower_third" {
			if _, ok := seen[capoverlay.GoldenPresetFontHash]; !ok {
				assets = append(assets, RenderQueueAsset{Hash: capoverlay.GoldenPresetFontHash, URL: capoverlay.CanonicalPresetFontPath})
			}
			break
		}
	}
	jobID := plan.PlanID
	if e.freshRender {
		jobID = fmt.Sprintf("%s:render:%d:%d", plan.PlanID, time.Now().UTC().UnixNano(), e.freshSeq.Add(1))
	}
	// Keep the queue dispatch explicit. RenderingGen uses this discriminator to
	// route the job through the overlay renderer (and to apply the overlay media
	// contract/ffprobe checks); omitting it silently falls back to the legacy
	// render_segment path.
	job := RenderQueueJob{
		ID:          jobID,
		JobType:     capoverlay.JobTypeRender,
		OverlaySpec: spec,
		Assets:      assets,
	}

	if err := e.client.Submit(ctx, job); err != nil {
		if e.freshRender {
			return RenderReference{}, fmt.Errorf("chronon queue fresh render submit failed: %w", err)
		}
		if errors.Is(err, ErrJobExists) {
			if existing, getErr := e.client.Get(ctx, plan.PlanID); getErr == nil && existing.State == "failed" {
				if retrier, ok := e.client.(interface {
					Retry(context.Context, string) error
				}); ok {
					_ = retrier.Retry(ctx, plan.PlanID)
				}
			}
		} else {
			return RenderReference{}, fmt.Errorf("chronon queue render submit failed: %w", err)
		}
	}

	done, wait, err := e.waitForCompletion(ctx, jobID)
	if err != nil {
		return RenderReference{}, err
	}
	if e.publisher != nil {
		if done.Artifact == nil || done.Artifact.SHA256 == "" || done.Artifact.SizeBytes <= 0 || done.Artifact.URL == "" {
			return RenderReference{}, fmt.Errorf("render job %s completed without certified artifact", jobID)
		}
		if err := e.publisher.PublishOverlay(ctx, OverlayPublicationSpec{
			ScriptName: plan.ScriptName,
			Language:   plan.Language,
			ProjectID:  plan.ProjectID,
			PlanID:     plan.PlanID,
		}, done.Artifact); err != nil {
			return RenderReference{}, fmt.Errorf("publish overlay artifact to Drive: %w", err)
		}
	}
	if e.recorder != nil {
		// attempt_id is the analytics idempotency key: it must be the real
		// queue job id. In fresh mode that is the unique per-attempt identity,
		// so two renders of the same plan record two rows instead of upsert-
		// colliding on the plan id.
		attempt := BuildRenderAttemptAnalyticsWithWait(jobID, plan, done.Artifact, wait)
		if err := e.recorder.RecordAttempt(ctx, attempt); err != nil {
			return RenderReference{}, fmt.Errorf("record render attempt analytics: %w", err)
		}
	}
	// Map the RenderingGen worker's own phase timings into the canonical
	// run model instead of a new timing family: each reported phase becomes
	// one owner-measured operation on the run bound to ctx (the kernel
	// never re-times a phase the worker already measured).
	recordRenderingGenPhases(ctx, done.Artifact)
	return RenderReference{JobID: jobID, Status: "COMPLETED", Artifact: done.Artifact}, nil
}

// terminalRenderState reports whether state is a terminal render state. The
// canonical terminal set is completed | failed | cancelled and MUST stay
// aligned with the RenderQueueWaiter contract above; a terminal state that is
// not recognised here would either be treated as success or poll forever.
func terminalRenderState(state string) bool {
	switch state {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

// terminalRenderResult maps a terminal job to the enqueuer's return value.
// Only "completed" is a success: a failed job surfaces its reason, and a
// cancelled job fails closed with its own reason rather than being reported as
// a completed render (which would surface downstream as the misleading
// "completed without certified artifact" error).
func terminalRenderResult(job RenderQueueJob, id string, metrics RenderCompletionMetrics) (RenderQueueJob, RenderCompletionMetrics, error) {
	switch job.State {
	case "failed":
		reason := job.FailReason
		if reason == "" {
			reason = "unknown failure"
		}
		return job, metrics, fmt.Errorf("render job %s failed: %s", id, reason)
	case "cancelled":
		reason := job.FailReason
		if reason == "" {
			reason = "unknown reason"
		}
		return job, metrics, fmt.Errorf("render job %s cancelled: %s", id, reason)
	default:
		return job, metrics, nil
	}
}

var semanticAssetIDSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func semanticAssetLogicalPath(ref capoverlay.OverlayAssetRef) string {
	if strings.HasPrefix(strings.TrimSpace(ref.URL), "assets/") {
		return filepath.ToSlash(strings.TrimSpace(ref.URL))
	}
	id := semanticAssetIDSanitizer.ReplaceAllString(strings.TrimSpace(ref.AssetID), "_")
	if id == "" {
		id = "asset"
	}
	ext := filepath.Ext(ref.URL)
	if parsed, err := url.Parse(ref.URL); err == nil && parsed.Path != "" {
		ext = filepath.Ext(parsed.Path)
	}
	if ext == "" {
		switch strings.ToLower(strings.TrimSpace(strings.SplitN(ref.MediaType, ";", 2)[0])) {
		case "image/png", "image":
			ext = ".png"
		case "image/jpeg", "image/jpg":
			ext = ".jpg"
		case "video/mp4", "video/quicktime", "video":
			ext = ".mp4"
		case "font/ttf", "font":
			ext = ".ttf"
		}
	}
	return "assets/semantic/" + id + ext
}

// recordRenderingGenPhases projects the worker-reported RenderingGen phase
// durations (materialize/plan/render/probe/hash/objectstore_upload/
// drive_publish) into canonical run operations bound to ctx. Phases the
// worker did not report (zero) are skipped — a missing measurement is never
// recorded as zero. The queue wait is already a canonical WaitCompletion
// observation (waitForCompletion) and the job wall time is the run's own
// WallTimeMs, so neither is duplicated here.
//
// The operations are bound to StageOverlayRender, the stage the render phase
// is measured under. Binding them to StageProcess instead left the render's
// work attached to a stage that no phase produced: the operations could never
// be joined to a stage wall time, so the breakdown reported the render's cost
// under the enclosing audio stage and gave that stage a dominant operation
// from another subsystem.
func recordRenderingGenPhases(ctx context.Context, artifact *RenderArtifact) {
	if artifact == nil {
		return
	}
	phases := []struct {
		operation  kernobs.OperationName
		durationMS int64
	}{
		{kernobs.OperationMaterialize, artifact.MaterializeMS},
		{kernobs.OperationPlan, artifact.PlanMS},
		{kernobs.OperationRender, artifact.RenderMS},
		{kernobs.OperationProbe, artifact.ProbeMS},
		{kernobs.OperationHash, artifact.HashMS},
		{kernobs.OperationObjectStoreUpload, artifact.UploadMS},
		{kernobs.OperationDrivePublish, artifact.DrivePublishMS},
	}
	for _, phase := range phases {
		if phase.durationMS <= 0 {
			continue
		}
		kernobs.RecordOperation(ctx, kernobs.OperationInfo{
			Stage:     StageOverlayRender,
			Component: kernobs.ComponentRenderingGen,
			Operation: phase.operation,
		}, phase.durationMS)
	}
}

// ── overlay.prepare ───────────────────────────────────────────────────

// QueuePrepareEnqueuer submits the overlay.prepare job for the run's
// pre-timing OverlayIntents to the central RenderingGen queue. Unlike the
// render enqueuer it is fire-and-forget: prepare resolves templates and
// prefetches entity assets independently of the timing-frozen render path
// and must never block the pipeline. The job id is deterministic
// ("prepare-"+planID) so replays are idempotent.
type QueuePrepareEnqueuer struct {
	client RenderQueueClient
}

// NewQueuePrepareEnqueuer creates a queue-backed prepare enqueuer.
func NewQueuePrepareEnqueuer(client RenderQueueClient) (*QueuePrepareEnqueuer, error) {
	if client == nil {
		return nil, fmt.Errorf("queue prepare enqueuer requires a queue client")
	}
	return &QueuePrepareEnqueuer{client: client}, nil
}

// EnqueuePrepare submits the prepare job and returns immediately. A job that
// already exists (ErrJobExists) is treated as idempotent success so a retry
// never double-prepares.
func (e *QueuePrepareEnqueuer) EnqueuePrepare(ctx context.Context, req capoverlay.PrepareRequest) error {
	if e == nil || e.client == nil {
		return fmt.Errorf("queue prepare enqueuer is not configured")
	}
	if err := req.Validate(); err != nil {
		return err
	}
	// Prepare has its own deliberately small wire contract. The capability
	// intent is rich (entity identity, payload and asset refs), but the worker
	// only needs template_id + PENDING timing to warm its registry; the asset
	// refs travel in the queue manifest. Projecting here prevents the rich
	// OverlayIntent fields (for example kind/entity) from crossing a strict
	// renderinggen.overlay-prepare.v1 boundary.
	spec, err := json.Marshal(newOverlayPrepareWire(req))
	if err != nil {
		return fmt.Errorf("chronon queue prepare marshal: %w", err)
	}
	job := RenderQueueJob{
		ID:          "prepare-" + req.PlanID,
		JobType:     capoverlay.JobTypePrepare,
		OverlaySpec: spec,
		Assets:      prepareAssets(req.Intents),
	}
	if err := e.client.Submit(ctx, job); err != nil {
		if errors.Is(err, ErrJobExists) {
			return nil // idempotent replay
		}
		return fmt.Errorf("chronon queue prepare submit failed: %w", err)
	}
	return nil
}

type overlayPrepareWire struct {
	SchemaVersion string                     `json:"schema_version"`
	PlanID        string                     `json:"plan_id"`
	VideoID       string                     `json:"video_id"`
	Width         int                        `json:"width"`
	Height        int                        `json:"height"`
	FPSNum        int                        `json:"fps_num"`
	FPSDen        int                        `json:"fps_den"`
	Intents       []overlayPrepareIntentWire `json:"intents"`
}

type overlayPrepareIntentWire struct {
	TemplateID  string `json:"template_id"`
	TimingState string `json:"timing_state"`
}

func newOverlayPrepareWire(req capoverlay.PrepareRequest) overlayPrepareWire {
	wire := overlayPrepareWire{
		SchemaVersion: req.SchemaVersion,
		PlanID:        req.PlanID,
		VideoID:       req.VideoID,
		Width:         req.Width,
		Height:        req.Height,
		FPSNum:        req.FPSNum,
		FPSDen:        req.FPSDen,
		Intents:       make([]overlayPrepareIntentWire, len(req.Intents)),
	}
	for i, intent := range req.Intents {
		wire.Intents[i] = overlayPrepareIntentWire{TemplateID: intent.TemplateID, TimingState: string(intent.TimingState)}
	}
	return wire
}

// prepareAssets collects the entity-image assets referenced by the intents,
// deduplicated by content hash, so the queue worker can prefetch them during
// the prepare phase.
func prepareAssets(intents []capoverlay.OverlayIntent) []RenderQueueAsset {
	var assets []RenderQueueAsset
	seen := make(map[string]bool)
	for _, intent := range intents {
		for _, ref := range intent.Payload.AssetRefs {
			hash := strings.ToLower(strings.TrimSpace(ref.SHA256))
			if hash == "" || seen[hash] {
				continue
			}
			seen[hash] = true
			asset := RenderQueueAsset{Hash: hash, URL: ref.URL, LocalPath: ref.LocalPath}
			if strings.HasPrefix(ref.URL, "http") {
				asset.SourceURL = ref.URL
			}
			assets = append(assets, asset)
		}
	}
	return assets
}

// waitForCompletion polls the queue until the job reaches a terminal state.
// The whole blocked interval is recorded as a completion wait on the bound
// run (RunReport.Waits), never as a stage: it is time spent waiting on the
// render queue, not pipeline CPU work.
func (e *QueueRenderEnqueuer) waitForCompletion(ctx context.Context, id string) (RenderQueueJob, RenderCompletionMetrics, error) {
	waitStarted := time.Now()
	interval := e.pollInterval
	if interval <= 0 {
		interval = defaultQueuePollInterval
	}
	metrics := RenderCompletionMetrics{PollInterval: interval}
	defer func() {
		kernobs.RecordWait(ctx, kernobs.WaitInfo{
			Kind:       kernobs.WaitCompletion,
			Component:  kernobs.ComponentRenderQueue,
			StartedAt:  waitStarted,
			FinishedAt: time.Now(),
		})
	}()

	// Event-driven completion: the wait parks server-side on the terminal
	// state transition, so the observed completion latency is the transition
	// itself rather than up to one poll interval. No client-side polling sleep
	// is recorded because none is spent.
	if waiter, ok := e.client.(RenderQueueWaiter); ok {
		job, err := waiter.WaitTerminal(ctx, id)
		metrics.CompletionWait = time.Since(waitStarted)
		if err != nil {
			return RenderQueueJob{}, metrics, err
		}
		return terminalRenderResult(job, id, metrics)
	}

	// Polling fallback for queue clients without the wait capability.
	for {
		job, err := e.client.Get(ctx, id)
		metrics.PollCount++
		if err != nil {
			return RenderQueueJob{}, metrics, err
		}
		switch {
		case terminalRenderState(job.State):
			metrics.CompletionWait = time.Since(waitStarted)
			return terminalRenderResult(job, id, metrics)
		}

		sleepStarted := time.Now()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			metrics.PollingSleep += time.Since(sleepStarted)
			metrics.CompletionWait = time.Since(waitStarted)
			return RenderQueueJob{}, metrics, ctx.Err()
		case <-timer.C:
			metrics.PollingSleep += time.Since(sleepStarted)
		}
	}
}
