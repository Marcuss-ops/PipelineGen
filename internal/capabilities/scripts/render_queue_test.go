package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// fakeRenderQueueClient implements RenderQueueClient in-memory for the
// enqueuer tests. It lets a test pre-seed a job (to exercise the idempotent
// ErrJobExists path) or advance the job state between polls.
type fakeRenderQueueClient struct {
	jobs  map[string]RenderQueueJob
	calls int
}

type captureOverlayPublication struct {
	spec     OverlayPublicationSpec
	artifact *RenderArtifact
}

type blockingOverlayPublication struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingOverlayPublication) PublishOverlay(_ context.Context, _ OverlayPublicationSpec, artifact *RenderArtifact) error {
	close(p.started)
	<-p.release
	artifact.DriveFileID = "drive-async"
	return nil
}

func (p *captureOverlayPublication) PublishOverlay(_ context.Context, spec OverlayPublicationSpec, artifact *RenderArtifact) error {
	p.spec = spec
	p.artifact = artifact
	return nil
}

// runScopedOverlayPublication is a Drive publication fake that behaves per RUN:
// it can hold one run's upload open and fail it while every other run's upload
// succeeds. That is the shape the live misattribution had — one run's overlay
// publication failing while another run's publications were in flight on the
// same process-wide pool — so a test built on it can prove the join is scoped
// to the run that queued the work.
type runScopedOverlayPublication struct {
	mu      sync.Mutex
	called  map[string]int
	hold    map[string]chan struct{}
	started map[string]chan struct{}
	fail    map[string]error
}

func newRunScopedOverlayPublication() *runScopedOverlayPublication {
	return &runScopedOverlayPublication{
		called:  make(map[string]int),
		hold:    make(map[string]chan struct{}),
		started: make(map[string]chan struct{}),
		fail:    make(map[string]error),
	}
}

// holdRun makes the named run's publication block until releaseRun is called.
func (p *runScopedOverlayPublication) holdRun(runID string) *runScopedOverlayPublication {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hold[runID] = make(chan struct{})
	p.started[runID] = make(chan struct{})
	return p
}

// failRun makes the named run's publication fail with err.
func (p *runScopedOverlayPublication) failRun(runID string, err error) *runScopedOverlayPublication {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail[runID] = err
	return p
}

// waitStarted blocks until the named run's publication has begun.
func (p *runScopedOverlayPublication) waitStarted(t *testing.T, runID string) {
	t.Helper()
	p.mu.Lock()
	started := p.started[runID]
	p.mu.Unlock()
	if started == nil {
		t.Fatalf("no publication was registered for run %q", runID)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("publication for run %q never started", runID)
	}
}

func (p *runScopedOverlayPublication) releaseRun(runID string) {
	p.mu.Lock()
	hold := p.hold[runID]
	p.mu.Unlock()
	if hold != nil {
		close(hold)
	}
}

func (p *runScopedOverlayPublication) calls(runID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.called[runID]
}

func (p *runScopedOverlayPublication) PublishOverlay(ctx context.Context, _ OverlayPublicationSpec, artifact *RenderArtifact) error {
	runID := runIDFromContext(ctx)
	p.mu.Lock()
	p.called[runID]++
	first := p.called[runID] == 1
	hold, started, fail := p.hold[runID], p.started[runID], p.fail[runID]
	p.mu.Unlock()
	if first && started != nil {
		close(started)
	}
	if hold != nil {
		<-hold
	}
	if fail != nil {
		return fail
	}
	artifact.DriveFileID = "drive-" + runID
	return nil
}

// runIDFromContext is the publication batch key the enqueuer uses: the kernel
// run bound to ctx, or "" for a composition that binds none.
func runIDFromContext(ctx context.Context) string {
	if run := kernobs.FromContext(ctx); run != nil {
		return run.Report().RunID
	}
	return ""
}

func newFakeRenderQueueClient() *fakeRenderQueueClient {
	return &fakeRenderQueueClient{jobs: make(map[string]RenderQueueJob)}
}

func (f *fakeRenderQueueClient) Submit(_ context.Context, job RenderQueueJob) error {
	f.calls++
	if _, ok := f.jobs[job.ID]; ok {
		return ErrJobExists
	}
	f.jobs[job.ID] = job
	return nil
}

func (f *fakeRenderQueueClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	job, ok := f.jobs[id]
	if !ok {
		return RenderQueueJob{}, errors.New("job not found")
	}
	return job, nil
}

// transitioningRenderClient reports "queued" on the first poll and
// "completed" afterwards, so the enqueuer performs at least one real poll
// interval and the recorded completion wait has a measurable duration.
type transitioningRenderClient struct{ polls int }

func (c *transitioningRenderClient) Submit(_ context.Context, job RenderQueueJob) error { return nil }
func (c *transitioningRenderClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	c.polls++
	if c.polls < 2 {
		return RenderQueueJob{ID: id, State: "queued"}, nil
	}
	return RenderQueueJob{ID: id, State: "completed", Artifact: &RenderArtifact{RenderMS: 800, EncodeMS: 200}}, nil
}

// TestQueueRenderEnqueuerSeparatesChrononAndPollingWait measures the production
// cadence directly: a render that is ready on the second status response has
// 800ms of worker-reported Chronon work but approximately one 2s polling sleep.
// The two durations must never be added together and reported as render_ms.
func TestQueueRenderEnqueuerSeparatesChrononAndPollingWait(t *testing.T) {
	client := &transitioningRenderClient{}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &fakeAttemptRecorder{}
	enqueuer.SetRecorder(recorder)
	enqueuer.pollInterval = 2 * time.Second

	started := time.Now()
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if len(recorder.recorded) != 1 {
		t.Fatalf("recorded attempts = %d, want 1", len(recorder.recorded))
	}
	got := recorder.recorded[0]
	if got.RenderMS != 800 || got.EncodeMS != 200 {
		t.Fatalf("worker durations = render=%d encode=%d, want 800/200", got.RenderMS, got.EncodeMS)
	}
	if got.PollCount != 2 || got.PollingIntervalMS != 2000 {
		t.Fatalf("poll metrics = polls=%d interval=%d, want 2/2000", got.PollCount, got.PollingIntervalMS)
	}
	if got.PollingSleepMS < 1900 || got.PollingSleepMS > 2600 {
		t.Fatalf("polling sleep = %dms, want approximately one 2s interval", got.PollingSleepMS)
	}
	if got.CompletionWaitMS < got.PollingSleepMS || got.CompletionWaitMS > 3000 {
		t.Fatalf("completion wait = %dms, polling sleep=%dms, want ~2s", got.CompletionWaitMS, got.PollingSleepMS)
	}
	if elapsed < 1900*time.Millisecond || elapsed > 3200*time.Millisecond {
		t.Fatalf("wall elapsed = %v, want approximately one 2s polling interval", elapsed)
	}
	t.Logf("separated metrics: render_ms=%d encode_ms=%d completion_wait_ms=%d polling_sleep_ms=%d polling_interval_ms=%d poll_count=%d wall_elapsed_ms=%d", got.RenderMS, got.EncodeMS, got.CompletionWaitMS, got.PollingSleepMS, got.PollingIntervalMS, got.PollCount, elapsed.Milliseconds())
}

// eventDrivenRenderClient implements RenderQueueWaiter: it reports a terminal
// job immediately, so the enqueuer must complete without any polling sleep and
// WITHOUT touching Get.
type eventDrivenRenderClient struct {
	waits int
	gets  int
	job   RenderQueueJob
}

func (c *eventDrivenRenderClient) Submit(_ context.Context, job RenderQueueJob) error { return nil }
func (c *eventDrivenRenderClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	c.gets++
	return RenderQueueJob{ID: id, State: "queued"}, nil
}
func (c *eventDrivenRenderClient) WaitTerminal(_ context.Context, _ string) (RenderQueueJob, error) {
	c.waits++
	return c.job, nil
}

// TestQueueRenderEnqueuerUsesEventDrivenWait pins the production path: when the
// queue client supports the job-status long poll, completion is observed at
// the transition with zero polling sleep, regardless of the configured polling
// cadence. An hour-long interval proves the polling loop is never entered.
func TestQueueRenderEnqueuerUsesEventDrivenWait(t *testing.T) {
	client := &eventDrivenRenderClient{job: RenderQueueJob{
		ID: "golden-overlay-v1", State: "completed",
		Artifact: &RenderArtifact{RenderMS: 800, EncodeMS: 200, SHA256: "ab", URL: "https://store/x.mp4", SizeBytes: 1},
	}}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Hour
	recorder := &fakeAttemptRecorder{}
	enqueuer.SetRecorder(recorder)

	start := time.Now()
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatal(err)
	}
	if client.waits != 1 {
		t.Fatalf("WaitTerminal calls = %d, want 1", client.waits)
	}
	if client.gets != 0 {
		t.Fatalf("event-driven wait must not probe the job with Get: gets=%d", client.gets)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("event-driven enqueue took %v; polling cadence leaked into the path", elapsed)
	}
	if len(recorder.recorded) != 1 {
		t.Fatalf("recorded attempts = %d, want 1", len(recorder.recorded))
	}
	got := recorder.recorded[0]
	if got.PollingSleepMS != 0 || got.PollCount != 0 {
		t.Fatalf("event-driven path must not record polling: sleep=%dms polls=%d", got.PollingSleepMS, got.PollCount)
	}
	if got.RenderMS != 800 || got.EncodeMS != 200 {
		t.Fatalf("worker durations lost: render=%d encode=%d", got.RenderMS, got.EncodeMS)
	}
}

// TestQueueRenderEnqueuerEventDrivenPropagatesFailure pins that a terminal
// failed job still surfaces its reason on the event-driven path.
func TestQueueRenderEnqueuerEventDrivenPropagatesFailure(t *testing.T) {
	client := &eventDrivenRenderClient{job: RenderQueueJob{ID: "x", State: "failed", FailReason: "chronon exploded"}}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1())
	if err == nil {
		t.Fatal("expected the terminal failure to propagate")
	}
	if !strings.Contains(err.Error(), "chronon exploded") {
		t.Fatalf("failure reason lost: %v", err)
	}
}

// TestRenderQueueStateVocabulary pins both halves of the queue's state
// vocabulary: the literals the RenderingGen queue actually sends, and the
// classifier that reads them. The literals are asserted directly because a
// constant holding a value the queue never sends is exactly the drift the raw
// literals used to hide — every decision in render_queue.go would stop matching
// with no error and no log.
func TestRenderQueueStateVocabulary(t *testing.T) {
	if RenderQueueStateCompleted != "completed" || RenderQueueStateFailed != "failed" || RenderQueueStateCancelled != "cancelled" {
		t.Fatalf("wire vocabulary drifted: completed=%q failed=%q cancelled=%q",
			RenderQueueStateCompleted, RenderQueueStateFailed, RenderQueueStateCancelled)
	}
	for _, state := range []string{RenderQueueStateCompleted, RenderQueueStateFailed, RenderQueueStateCancelled} {
		if !terminalRenderState(state) {
			t.Fatalf("terminalRenderState(%q) = false, want true", state)
		}
	}
	for _, state := range []string{"", "queued", "running", "rendered"} {
		if terminalRenderState(state) {
			t.Fatalf("terminalRenderState(%q) = true, want false", state)
		}
	}
}

// TestTerminalRenderResultFailsClosedOnUnknownState pins the classifier that
// decides whether a terminal job is the caller's success. An unrecognised
// terminal state must NEVER come back as a completed render: the caller's next
// step is to fetch the certified artifact, so a state this build does not know
// has to stop it rather than send it looking for bytes no one produced.
func TestTerminalRenderResultFailsClosedOnUnknownState(t *testing.T) {
	metrics := RenderCompletionMetrics{}
	if _, _, err := terminalRenderResult(RenderQueueJob{ID: "job-1", State: RenderQueueStateCompleted}, "job-1", metrics); err != nil {
		t.Fatalf("completed must be a success, got %v", err)
	}
	if _, _, err := terminalRenderResult(RenderQueueJob{ID: "job-1", State: "failed", FailReason: "chronon exploded"}, "job-1", metrics); err == nil {
		t.Fatal("failed must surface an error")
	}
	if _, _, err := terminalRenderResult(RenderQueueJob{ID: "job-1", State: "canceled"}, "job-1", metrics); err == nil {
		t.Fatal("an unrecognised terminal state must fail closed, not report a completed render")
	}
}

// retryingRenderQueueClient is fakeRenderQueueClient PLUS the optional
// RenderQueueRetrier capability, so a test can drive the "the job I collided
// with is already FAILED" recovery path that a plain replay skips.
type retryingRenderQueueClient struct {
	fakeRenderQueueClient
	retries    int
	retryErr   error
	retriedJob *RenderQueueJob
}

func newRetryingRenderQueueClient(job RenderQueueJob) *retryingRenderQueueClient {
	return &retryingRenderQueueClient{
		fakeRenderQueueClient: fakeRenderQueueClient{jobs: map[string]RenderQueueJob{job.ID: job}},
	}
}

func (c *retryingRenderQueueClient) Retry(_ context.Context, id string) error {
	c.retries++
	if c.retryErr != nil {
		return c.retryErr
	}
	if c.retriedJob != nil {
		job := *c.retriedJob
		job.ID = id
		c.jobs[id] = job
	}
	return nil
}

// TestQueueRenderEnqueuerFailedJobRecovery pins the ONE decision this recovery
// path exists for: a submission that collides with an existing job in FAILED
// state must RE-ARM it, and a re-arm that cannot happen must fail closed.
//
// Swallowing the retry error (the historical `_ = retrier.Retry(...)`, whose
// capability was an anonymous interface at the call site) let the enqueuer walk
// into waitForCompletion on a job that was already terminally failed: the caller
// paid the whole wait and then read an unexplained render failure instead of
// "the queue could not be re-armed".
func TestQueueRenderEnqueuerFailedJobRecovery(t *testing.T) {
	plan := capoverlay.GoldenOverlayPlanV1()
	failedJob := RenderQueueJob{ID: plan.PlanID, State: "failed", FailReason: "chronon exploded"}

	t.Run("a re-arm that fails is surfaced", func(t *testing.T) {
		client := newRetryingRenderQueueClient(failedJob)
		client.retryErr = errors.New("queue refused the reset")
		enqueuer, err := NewQueueRenderEnqueuer(client)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err == nil {
			t.Fatal("expected the failed re-arm to propagate")
		} else if !strings.Contains(err.Error(), "retry failed") {
			t.Fatalf("error must name the failed re-arm, got %v", err)
		}
		if client.retries != 1 {
			t.Fatalf("retries = %d, want 1", client.retries)
		}
	})

	t.Run("a client that cannot re-arm fails closed", func(t *testing.T) {
		client := newFakeRenderQueueClient()
		client.jobs[failedJob.ID] = failedJob
		enqueuer, err := NewQueueRenderEnqueuer(client)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err == nil {
			t.Fatal("expected a client without the retrier capability to fail closed")
		} else if !strings.Contains(err.Error(), "cannot retry it") {
			t.Fatalf("error must name the missing retrier capability, got %v", err)
		}
	})

	t.Run("a re-armed job is awaited and returned", func(t *testing.T) {
		completed := RenderQueueJob{State: "completed", Artifact: &RenderArtifact{RenderMS: 800, EncodeMS: 200}}
		client := newRetryingRenderQueueClient(failedJob)
		client.retriedJob = &completed
		enqueuer, err := NewQueueRenderEnqueuer(client)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := enqueuer.EnqueueChrononPlan(context.Background(), plan)
		if err != nil {
			t.Fatalf("a re-armed job must be awaited, got %v", err)
		}
		if client.retries != 1 {
			t.Fatalf("retries = %d, want 1", client.retries)
		}
		if ref.JobID != plan.PlanID {
			t.Fatalf("job id = %q, want %q", ref.JobID, plan.PlanID)
		}
	})
}

func TestQueueRenderEnqueuerSetPollInterval(t *testing.T) {
	enqueuer, err := NewQueueRenderEnqueuer(newFakeRenderQueueClient())
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetPollInterval(250 * time.Millisecond)
	if enqueuer.pollInterval != 250*time.Millisecond {
		t.Fatalf("poll interval = %s, want 250ms", enqueuer.pollInterval)
	}
	// Non-positive values must not disable polling accidentally.
	enqueuer.SetPollInterval(0)
	if enqueuer.pollInterval != 250*time.Millisecond {
		t.Fatalf("non-positive poll interval changed configured value to %s", enqueuer.pollInterval)
	}
}

func TestSeparateOverlayItemPlanUsesTTSWindowPlusPaddingAndFiveSecondCap(t *testing.T) {
	parent := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan, PlanID: "dolly:overlay", VideoID: "dolly",
		ScriptName: "Dolly", Language: "en", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Items: []capoverlay.OverlayItem{{
			ID: "entity-dolly", EntityID: "person:dolly-parton", Kind: "entity_card", TemplateID: "person_default",
			StartUS: 12_000_000, DurationUS: 1_500_000, StartMs: 12000, EndMs: 13500,
			Text: "Dolly Parton", AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "dolly", SHA256: "abc"}},
		}},
	}
	child, meta, err := separateOverlayItemPlan(parent, parent.Items[0], 0)
	if err != nil {
		t.Fatal(err)
	}
	if child.DurationMS != 3500 || child.Items[0].StartUS != 0 || child.Items[0].DurationUS != 3_500_000 {
		t.Fatalf("child timing = duration=%d item=%+v, want 3500ms local / 3500000us", child.DurationMS, child.Items[0])
	}
	if meta.SourceStartUS != 12_000_000 || meta.SourceEndUS != 13_500_000 || meta.TargetDurationUS != 3_500_000 {
		t.Fatalf("source metadata = %+v", meta)
	}

	long := parent.Items[0]
	long.ID = "phrase-long"
	long.EntityID = ""
	long.Kind = "text_phrase"
	long.TemplateID = "IMPORTANT_PHRASE"
	long.StartUS, long.DurationUS = 0, 4_500_000
	long.StartMs, long.EndMs = 0, 4500
	child, meta, err = separateOverlayItemPlan(parent, long, 1)
	if err != nil {
		t.Fatal(err)
	}
	if child.DurationMS != 5000 || child.Items[0].DurationUS != 5_000_000 || meta.TargetDurationUS != 5_000_000 {
		t.Fatalf("five-second cap not applied: child=%+v meta=%+v", child, meta)
	}
}

// TestQueueRenderEnqueuerChrononPlan pins the production path that makes
// PipelineGen submit semantic visual instructions to RenderingGen. RenderingGen
// owns the final semantic→Chronon v2 compilation and submits the certified
// artifact only after that concrete plan has actually rendered.
func TestQueueRenderEnqueuerChrononPlan(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond

	// Pre-seed the semantic document so the idempotent replay path observes
	// the same payload shape production submits.
	spec, err := json.Marshal(capoverlay.GoldenOverlayPlanV1())
	if err != nil {
		t.Fatal(err)
	}
	client.jobs["golden-overlay-v1"] = RenderQueueJob{
		ID:          "golden-overlay-v1",
		JobType:     capoverlay.JobTypeRender,
		OverlaySpec: spec,
		Assets: []RenderQueueAsset{
			{SHA256: capoverlay.GoldenBackgroundHash, URL: "assets/background.jpg"},
			{SHA256: capoverlay.GoldenAppleHash, URL: "assets/apple.png"},
			{SHA256: capoverlay.GoldenPresetFontHash, URL: capoverlay.CanonicalPresetFontPath},
			{SHA256: capoverlay.GoldenFontHash, URL: capoverlay.CanonicalTextFontPath},
		},
		State: "completed",
		Artifact: &RenderArtifact{
			ID:           "art-golden",
			URL:          "https://store/result.mp4",
			SHA256:       "ab",
			MimeType:     "video/mp4",
			ProfileID:    "chronon-copy-v1",
			CopyEligible: true,
			Width:        1280,
			Height:       720,
			FPSNum:       30,
			FrameCount:   150,
			DurationUS:   5000000,
			Codec:        "h264",
		},
	}

	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1())
	if err != nil {
		t.Fatal(err)
	}
	if ref.JobID != "golden-overlay-v1" || ref.Status != "COMPLETED" {
		t.Fatalf("unexpected reference: %+v", ref)
	}
	if ref.Artifact == nil || ref.Artifact.ProfileID != "chronon-copy-v1" || !ref.Artifact.CopyEligible || ref.Artifact.FrameCount != 150 {
		t.Fatalf("artifact not propagated: %+v", ref.Artifact)
	}

	// The submitted job must carry the semantic overlay-plan.v1 document (not
	// an old PipelineGen-owned concrete Chronon v1 plan). RenderingGen then
	// compiles the v2 document immediately before invoking Chronon.
	submitted, ok := client.jobs["golden-overlay-v1"]
	if !ok {
		t.Fatal("job was not submitted to the queue")
	}
	if submitted.JobType != capoverlay.JobTypeRender {
		t.Fatalf("submitted job type = %q, want %q", submitted.JobType, capoverlay.JobTypeRender)
	}
	var doc struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(submitted.OverlaySpec, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != capoverlay.SchemaVersionPlan {
		t.Fatalf("submitted plan schema = %q, want %q", doc.SchemaVersion, capoverlay.SchemaVersionPlan)
	}
	if len(submitted.Assets) != 4 {
		t.Fatalf("submitted assets = %d, want 4", len(submitted.Assets))
	}
	if submitted.Assets[0].SHA256 != capoverlay.GoldenBackgroundHash || submitted.Assets[0].URL != "assets/background.jpg" {
		t.Fatalf("asset 0 not projected: %+v", submitted.Assets[0])
	}
	if submitted.Assets[1].SHA256 != capoverlay.GoldenAppleHash || submitted.Assets[1].URL != "assets/apple.png" {
		t.Fatalf("asset 1 not projected: %+v", submitted.Assets[1])
	}
	if submitted.Assets[3].SHA256 != capoverlay.GoldenFontHash || submitted.Assets[3].URL != capoverlay.CanonicalTextFontPath {
		t.Fatalf("Cyrillic semantic font not projected: %+v", submitted.Assets[3])
	}
}

type freshRenderClient struct {
	job RenderQueueJob
}

func (c *freshRenderClient) Submit(_ context.Context, job RenderQueueJob) error {
	job.State = "completed"
	job.Artifact = &RenderArtifact{SHA256: "fresh", SizeBytes: 1, URL: "https://store.invalid/fresh.mp4"}
	c.job = job
	return nil
}

func (c *freshRenderClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	if c.job.ID != id {
		return RenderQueueJob{}, errors.New("unexpected fresh render job id")
	}
	return c.job, nil
}

func TestQueueRenderEnqueuerFreshRenderUsesNewQueueIdentity(t *testing.T) {
	client := &freshRenderClient{}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetFreshRender(true)
	enqueuer.pollInterval = time.Millisecond

	plan := capoverlay.GoldenOverlayPlanV1()
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if client.job.ID == plan.PlanID {
		t.Fatalf("fresh render reused logical plan id %q", client.job.ID)
	}
	if !strings.HasPrefix(client.job.ID, plan.PlanID+":render:") {
		t.Fatalf("fresh render job id=%q does not carry plan identity", client.job.ID)
	}
}

func TestQueueRenderEnqueuerCarriesJobDriveFolderToPublisherAndOmitsItFromWire(t *testing.T) {
	client := &freshRenderClient{}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond
	capture := &captureOverlayPublication{}
	enqueuer.SetArtifactPublisher(capture)

	plan := capoverlay.GoldenOverlayPlanV1()
	plan.DriveFolderID = "job-selected-root"
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if capture.spec.DriveFolderID != "job-selected-root" {
		t.Fatalf("publisher drive folder = %q, want job-selected-root", capture.spec.DriveFolderID)
	}
	var wire struct {
		DriveFolderID string `json:"drive_folder_id,omitempty"`
	}
	if err := json.Unmarshal(client.job.OverlaySpec, &wire); err != nil {
		t.Fatalf("decode submitted overlay spec: %v", err)
	}
	if wire.DriveFolderID != "" {
		t.Fatalf("application-only drive folder leaked onto RenderingGen wire: %q", wire.DriveFolderID)
	}
}

func TestQueueRenderEnqueuerAsyncPublicationJoinsBeforeWait(t *testing.T) {
	client := &freshRenderClient{}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	publisher := &blockingOverlayPublication{started: make(chan struct{}), release: make(chan struct{})}
	enqueuer.SetArtifactPublisher(publisher)
	enqueuer.SetAsyncPublication(true)

	ref, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-publisher.started:
	case <-time.After(time.Second):
		t.Fatal("async publication did not start")
	}
	if ref.Artifact == nil || ref.Artifact.DriveFileID != "" {
		t.Fatalf("render caller must return before Drive publication completes: %+v", ref.Artifact)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- enqueuer.Wait(context.Background()) }()
	select {
	case err := <-waitDone:
		t.Fatalf("Wait returned before publication completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(publisher.release)
	if err := <-waitDone; err != nil {
		t.Fatal(err)
	}
	if ref.Artifact.DriveFileID != "drive-async" {
		t.Fatalf("joined publication did not update certified artifact: %+v", ref.Artifact)
	}
}

// TestQueueRenderEnqueuerRecordsAttemptAnalytics pins the analytics wiring:
// when a recorder is attached, one completed render attempt produces exactly
// one analytics record derived from the plan census + the certified artifact.
func TestQueueRenderEnqueuerRecordsAttemptAnalytics(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond
	recorder := &fakeAttemptRecorder{}
	enqueuer.SetRecorder(recorder)

	client.jobs["golden-overlay-v1"] = RenderQueueJob{
		ID:    "golden-overlay-v1",
		State: "completed",
		Artifact: &RenderArtifact{
			SHA256: "ab", RenderMS: 500, EncodeMS: 100, Width: 1280, Height: 720,
			DriveFileID: "drive-1", DriveLink: "https://drive.google.com/file/d/drive-1/view",
		},
	}

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatal(err)
	}
	if len(recorder.recorded) != 1 {
		t.Fatalf("recorded attempts = %d, want 1", len(recorder.recorded))
	}
	got := recorder.recorded[0]
	if got.AttemptID != "golden-overlay-v1" || got.SHA256 != "ab" || got.RenderMS != 500 || got.EncodeMS != 100 ||
		got.DriveFileID != "drive-1" || got.DriveLink != "https://drive.google.com/file/d/drive-1/view" {
		t.Fatalf("recorded attempt = %+v", got)
	}
	if got.Content.Images == 0 {
		t.Fatalf("content census empty: %+v", got.Content)
	}
}

// TestQueueRenderEnqueuerRecorderFailureFailsClosed pins the fail-closed
// analytics contract: a recorder error fails the enqueue rather than being
// silently swallowed (never represent an unavailable backend as success).
func TestQueueRenderEnqueuerRecorderFailureFailsClosed(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond
	enqueuer.SetRecorder(&fakeAttemptRecorder{err: errors.New("analytics db down")})

	client.jobs["golden-overlay-v1"] = RenderQueueJob{ID: "golden-overlay-v1", State: "completed", Artifact: &RenderArtifact{SHA256: "ab"}}

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1()); err == nil {
		t.Fatal("recorder failure must fail the enqueue")
	}
}

// ── overlay.prepare enqueuer ──────────────────────────────────────────

func prepareTestRequest(planID string) capoverlay.PrepareRequest {
	return capoverlay.PrepareRequest{
		SchemaVersion: capoverlay.SchemaVersionPrepare,
		PlanID:        planID,
		VideoID:       planID,
		Width:         1280,
		Height:        720,
		FPSNum:        30, FPSDen: 1,
		Intents: []capoverlay.OverlayIntent{
			{
				Version: capoverlay.OverlayIntentVersion, IntentID: "intent-scene-0-tom-hanks",
				SceneID: "scene-0", SceneIndex: 0, Source: capoverlay.IntentSourceEntity,
				Entity: capoverlay.EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
				Kind:   string(capoverlay.KindEntityCard), TemplateID: "person_default",
				Payload: capoverlay.IntentPayload{Name: "Tom Hanks"}, TimingState: capoverlay.TimingStatePending,
			},
			{
				Version: capoverlay.OverlayIntentVersion, IntentID: "intent-scene-0-apple",
				SceneID: "scene-0", SceneIndex: 0, Source: capoverlay.IntentSourceEntity,
				Entity: capoverlay.EntityBinding{Type: "LOGO", CanonicalName: "Apple"},
				Kind:   string(capoverlay.KindLogo), TemplateID: "LOGO",
				Payload: capoverlay.IntentPayload{
					Name: "Apple",
					AssetRefs: []capoverlay.OverlayAssetRef{
						{AssetID: "apple-logo", URL: "https://cdn.example.com/apple.png", SHA256: "abc123"},
						{AssetID: "apple-logo", URL: "https://cdn.example.com/apple.png", SHA256: "ABC123"}, // dedup by hash
					},
				},
				TimingState: capoverlay.TimingStatePending,
			},
		},
	}
}

// TestQueuePrepareEnqueuer_SubmitsPrepareJob pins the overlay.prepare
// path: the pre-timing PrepareRequest is submitted as an overlay.prepare
// job whose id is "prepare-"+planID (idempotency key), whose spec contains
// the strict worker projection of those intents, and whose assets are the deduplicated
// entity-image refs carried on the intents.
func TestQueuePrepareEnqueuer_SubmitsPrepareJob(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueuePrepareEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	req := prepareTestRequest("run-prepare-001")
	if err := enqueuer.EnqueuePrepare(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	job, ok := client.jobs["prepare-run-prepare-001"]
	if !ok {
		t.Fatal("prepare job was not submitted")
	}
	if job.JobType != capoverlay.JobTypePrepare {
		t.Fatalf("job type = %q, want %q", job.JobType, capoverlay.JobTypePrepare)
	}
	var got overlayPrepareWire
	if err := json.Unmarshal(job.OverlaySpec, &got); err != nil {
		t.Fatal(err)
	}
	if got.PlanID != req.PlanID || got.SchemaVersion != capoverlay.SchemaVersionPrepare {
		t.Fatalf("spec did not round-trip: %+v", got)
	}
	if len(got.Intents) != 2 || got.Intents[0].TemplateID != "person_default" || got.Intents[1].TimingState != string(capoverlay.TimingStatePending) {
		t.Fatalf("intents not projected: %+v", got.Intents)
	}
	// Assets are deduplicated by content hash (case-insensitive).
	if len(job.Assets) != 1 || job.Assets[0].SHA256 != "abc123" || job.Assets[0].URL != "https://cdn.example.com/apple.png" {
		t.Fatalf("prepare assets = %+v", job.Assets)
	}
}

// TestQueuePrepareEnqueuer_IdempotentOnReplay pins that a retry never
// double-prepares: an existing job (ErrJobExists) is treated as success.
func TestQueuePrepareEnqueuer_IdempotentOnReplay(t *testing.T) {
	client := newFakeRenderQueueClient()
	client.jobs["prepare-run-prepare-001"] = RenderQueueJob{ID: "prepare-run-prepare-001"}
	enqueuer, err := NewQueuePrepareEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := enqueuer.EnqueuePrepare(context.Background(), prepareTestRequest("run-prepare-001")); err != nil {
		t.Fatalf("replay must be idempotent: %v", err)
	}
}

// TestQueuePrepareEnqueuer_RejectsInvalidRequest pins fail-closed: an
// invalid PrepareRequest is rejected before any submit.
func TestQueuePrepareEnqueuer_RejectsInvalidRequest(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueuePrepareEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	req := prepareTestRequest("run-prepare-001")
	req.Intents[0].TimingState = capoverlay.TimingStateFrozen
	if err := enqueuer.EnqueuePrepare(context.Background(), req); err == nil {
		t.Fatal("a FROZEN intent must fail before submit")
	}
	if client.calls != 0 {
		t.Fatalf("submit calls = %d, want 0", client.calls)
	}
}

// TestQueuePrepareEnqueuer_NilClientFailsClosed pins that an unconfigured
// enqueuer never silently succeeds.
func TestQueuePrepareEnqueuer_NilClientFailsClosed(t *testing.T) {
	if _, err := NewQueuePrepareEnqueuer(nil); err == nil {
		t.Fatal("nil client must fail construction")
	}
}

// TestQueueRenderEnqueuerChrononPlanPropagatesFailure pins failure
// propagation on the Chronon plan path (mirror of the media-plan failure
// test).
func TestQueueRenderEnqueuerChrononPlanPropagatesFailure(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond

	client.jobs["golden-overlay-v1"] = RenderQueueJob{ID: "golden-overlay-v1", State: "failed", FailReason: "chronon exploded"}

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1()); err == nil {
		t.Fatal("expected failure to propagate")
	}
}

// TestRecordRenderingGenPhasesMapsWorkerTimingsToCanonicalRun pins the
// RenderingGen → canonical mapping: every worker-reported phase becomes one
// owner-measured operation on the run (component renderinggen, stage
// overlay_render), and phases the worker did not report are skipped — never a fake
// zero. The kernel never re-times a phase the worker already measured.
func TestRecordRenderingGenPhasesMapsWorkerTimingsToCanonicalRun(t *testing.T) {
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-render", JobType: "overlay.render", AttemptID: "attempt-render"})
	ctx := kernobs.WithRun(context.Background(), run)

	artifact := &RenderArtifact{
		MaterializeMS: 420, PlanMS: 15, RenderMS: 900, ProbeMS: 25, HashMS: 8,
		UploadMS: 31, DrivePublishMS: 240,
	}
	recordRenderingGenPhases(ctx, artifact)

	report := run.Finish()
	want := map[string]int64{
		"materialize": 420, "plan": 15, "render": 900, "probe": 25,
		"hash": 8, "objectstore_upload": 31, "drive_publish": 240,
	}
	got := map[string]int64{}
	for _, op := range report.Operations {
		if op.Component != string(kernobs.ComponentRenderingGen) {
			t.Fatalf("operation %s component=%q, want renderinggen", op.Operation, op.Component)
		}
		if op.Stage != string(StageOverlayRender) {
			t.Fatalf("operation %s stage=%q, want overlay_render", op.Operation, op.Stage)
		}
		if _, dup := got[op.Operation]; dup {
			t.Fatalf("operation %s recorded twice", op.Operation)
		}
		got[op.Operation] = op.DurationMs
	}
	if len(got) != len(want) {
		t.Fatalf("operations=%v, want %v", got, want)
	}
	for op, ms := range want {
		if got[op] != ms {
			t.Fatalf("operation %s duration=%d, want %d", op, got[op], ms)
		}
	}
}

// TestRecordRenderingGenPhasesSkipsUnreportedPhases pins the no-fake-zero
// rule: a nil artifact and zero durations record nothing.
func TestRecordRenderingGenPhasesSkipsUnreportedPhases(t *testing.T) {
	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-render", AttemptID: "attempt-render"})
	ctx := kernobs.WithRun(context.Background(), run)

	recordRenderingGenPhases(ctx, nil)
	recordRenderingGenPhases(ctx, &RenderArtifact{RenderMS: 0})

	if report := run.Finish(); len(report.Operations) != 0 {
		t.Fatalf("unreported phases must not be recorded: %+v", report.Operations)
	}
}

// ─── Phase 2: the canonical media identity (kernel/asset.Ref) ────────────
//
// RenderQueueAsset is the WIRE PROJECTION of kernel/asset.Ref. These tests pin
// the two properties that make that projection worth having: the deployed wire
// key did not change when the Go field was renamed to the canonical vocabulary,
// and the identity a producer builds and the identity the queue reads back are
// the SAME identity even when the digest arrives in a different case.

// TestRenderQueueAssetWireContractStaysHashKeyed is a cross-repo contract test.
// The Go field is SHA256 (the canonical content-address vocabulary) but the JSON
// key must remain `hash`, because it is the deployed RenderingGen queue contract
// (queueclient.AssetRef). Renaming the field is a local cleanup; renaming the key
// is a coordinated cutover, and this test refuses to let one masquerade as the
// other.
func TestRenderQueueAssetWireContractStaysHashKeyed(t *testing.T) {
	raw, err := json.Marshal(RenderQueueAsset{
		SHA256:    "abc123",
		URL:       "assets/semantic/person_matt.jpg",
		SourceURL: "https://cdn.example/matt.jpg",
		LocalPath: "/tmp/producer-only/matt.jpg",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if _, ok := decoded["hash"]; !ok {
		t.Errorf("wire asset lost its `hash` key: %s", raw)
	}
	if _, ok := decoded["sha256"]; ok {
		t.Errorf("the Go field rename leaked onto the wire as `sha256`: %s", raw)
	}
	if len(decoded) != 3 {
		t.Errorf("wire asset keys = %v, want exactly hash/url/source_url: %s", decoded, raw)
	}
	// LocalPath is a producer-process runtime value and must never be published.
	if strings.Contains(string(raw), "local_path") || strings.Contains(string(raw), "/tmp/producer-only") {
		t.Errorf("producer-local path leaked onto the queue wire: %s", raw)
	}
}

// TestRenderQueueAssetRefCanonicalisesDigestSpelling pins the reason identity
// moved onto kernel/asset.Ref: the digest is a COMPARISON KEY. The enqueue path
// deduplicates assets by it, so "ABC123" and "abc123" must be one asset — not
// two payloads published under two addresses for the same bytes.
func TestRenderQueueAssetRefCanonicalisesDigestSpelling(t *testing.T) {
	upper := RenderQueueAsset{SHA256: "ABC123", URL: "assets/a.jpg"}
	lower := RenderQueueAsset{SHA256: "abc123", URL: "assets/a.jpg"}

	if !upper.Ref().Equal(lower.Ref()) {
		t.Errorf("one digest in two cases produced two identities: %v vs %v", upper.Ref(), lower.Ref())
	}
	if upper.Ref().DedupKey() != lower.Ref().DedupKey() {
		t.Errorf("dedup keys disagree for one digest: %q vs %q", upper.Ref().DedupKey(), lower.Ref().DedupKey())
	}
	if upper.Ref().SHA256 != "abc123" {
		t.Errorf("Ref().SHA256 = %q, want the canonical lower-case address", upper.Ref().SHA256)
	}
}

// TestRenderQueueAssetRefCarriesNoLocation asserts the projection's whole point:
// a location-bearing wire asset projects onto a location-FREE identity. The
// queue DTO may hold a producer path for staging, but the identity it hands to
// the canonical type must be unable to describe where bytes live.
func TestRenderQueueAssetRefCarriesNoLocation(t *testing.T) {
	identity := RenderQueueAsset{
		SHA256:    "abc123",
		URL:       "assets/a.jpg",
		SourceURL: "https://cdn.example/a.jpg",
		LocalPath: "/tmp/a.jpg",
	}.Ref()

	raw, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	for _, forbidden := range []string{"local_path", "url", "source_url", "drive_link", "drive_file_id", "download_link", "legacy_file_md5"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Errorf("canonical identity carries location key %q: %s", forbidden, raw)
		}
	}
	if err := identity.Validate(); err != nil {
		t.Errorf("a digest-bearing wire asset must project onto a valid identity: %v", err)
	}
}

// TestNewRenderQueueAssetRoundTripsThroughTheIdentity proves the builder and the
// projection agree, so a producer that builds from the canonical identity cannot
// have it silently changed on the way to the queue.
//
// The projection is deliberately NARROWER than the identity: the deployed
// RenderingGen wire asset carries only hash/logical_path/source_url, so
// MediaType and SizeBytes have no field to travel in and are dropped. That is a
// documented narrowing of the queue contract, not a lossy conversion — the test
// asserts the half that the contract claims to carry, and names the half it does
// not, so a future widening is a deliberate change rather than a surprise.
func TestNewRenderQueueAssetRoundTripsThroughTheIdentity(t *testing.T) {
	identity := kernelasset.New("assets/a.jpg", "ABCDEF12", "image/jpeg", 4096)
	asset := NewRenderQueueAsset(identity, "assets/a.jpg", "https://cdn.example/a.jpg")

	if asset.SHA256 != "abcdef12" {
		t.Errorf("builder did not canonicalise the digest: %q", asset.SHA256)
	}
	if asset.LocalPath != "" {
		t.Errorf("builder leaked a location into the projection: %q", asset.LocalPath)
	}

	back := asset.Ref()
	want := kernelasset.Ref{AssetID: "assets/a.jpg", SHA256: "abcdef12"}
	if back != want {
		t.Errorf("build→project is lossy: got %+v, want %+v", back, want)
	}
	if back.MediaType != "" || back.SizeBytes != 0 {
		t.Errorf("the wire projection must not invent fields the queue does not carry: %+v", back)
	}
}

// publicationRunCtx builds a run-bound context the way the job worker does
// (kernobs.WithRun around the ctx it hands to the script runner), so the
// enqueuer sees two DISTINCT run identities sharing one pool.
func publicationRunCtx(t *testing.T, observer *kernobs.RunObserver, runID string) context.Context {
	t.Helper()
	run := observer.StartRun(context.Background(), kernobs.RunInfo{RunID: runID, AttemptID: "attempt-1"})
	if run == nil {
		t.Fatalf("StartRun returned no run for %q", runID)
	}
	return kernobs.WithRun(context.Background(), run)
}

// TestQueueRenderEnqueuerPublicationJoinIsRunScoped pins the cross-run
// isolation of the publication pool.
//
// The pool is process-wide (the composition root wires ONE enqueuer for the
// whole process), but each run's completion only joins its OWN publications.
// The contract, each half of which was violated by the previous shared
// WaitGroup + single error slot:
//
//  1. run A never blocks on run B's in-flight publication;
//  2. run A is never failed by run B's publication error;
//  3. a run that already completed (batch retired) does not poison a later run.
//
// Point 2 is the live failure this exists for: a run was failed with another
// run's overlay error (`context canceled` from a different video), so the
// operator's retry could not fix its own run.
func TestQueueRenderEnqueuerPublicationJoinIsRunScoped(t *testing.T) {
	t.Parallel()

	observer := kernobs.NewRunObserver(nil)
	ctxSlow := publicationRunCtx(t, observer, "run-slow")
	ctxFast := publicationRunCtx(t, observer, "run-fast")

	imposed := errors.New("drive upload failed for this run only")
	publisher := newRunScopedOverlayPublication().holdRun("run-slow").failRun("run-slow", imposed)

	enqueuer, err := NewQueueRenderEnqueuer(&freshRenderClient{})
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetArtifactPublisher(publisher)
	enqueuer.SetAsyncPublication(true)

	// run-slow publishes first and is held open; run-fast publishes after it.
	if _, err := enqueuer.EnqueueChrononPlan(ctxSlow, capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatalf("enqueue for run-slow: %v", err)
	}
	if _, err := enqueuer.EnqueueChrononPlan(ctxFast, capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatalf("enqueue for run-fast: %v", err)
	}
	publisher.waitStarted(t, "run-slow")

	// (1) run-fast joins its own batch only, while run-slow's upload is still
	// open on the shared pool.
	doneFast := make(chan error, 1)
	go func() { doneFast <- enqueuer.Wait(ctxFast) }()
	select {
	case err := <-doneFast:
		if err != nil {
			t.Fatalf("run-fast inherited another run's publication failure: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run-fast's join blocked on run-slow's in-flight publication")
	}
	if got := publisher.calls("run-fast"); got != 1 {
		t.Fatalf("run-fast publications = %d, want 1 (its own publication must have run)", got)
	}

	// (2) run-slow is failed by its OWN failure — and only now, after the join.
	publisher.releaseRun("run-slow")
	if err := enqueuer.Wait(ctxSlow); !errors.Is(err, imposed) {
		t.Fatalf("run-slow join = %v, want its own publication failure %v", err, imposed)
	}

	// (3) a later run starts clean: the retired batch left nothing behind.
	ctxLater := publicationRunCtx(t, observer, "run-later")
	if _, err := enqueuer.EnqueueChrononPlan(ctxLater, capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatalf("enqueue for run-later: %v", err)
	}
	if err := enqueuer.Wait(ctxLater); err != nil {
		t.Fatalf("a later run inherited a retired publication failure: %v", err)
	}
}

// TestQueueRenderEnqueuerUnboundPublicationJoinsAndClears pins the fallback
// path: a composition that binds no kernel run (unit-style callers, one-shot
// CLI calls) keeps the historical process-wide join semantics — the join
// returns that work's first failure, and TAKES it, so the next join of the same
// unbound pool is clean instead of re-reporting a failure that is over.
func TestQueueRenderEnqueuerUnboundPublicationJoinsAndClears(t *testing.T) {
	t.Parallel()

	imposed := errors.New("drive upload failed")
	publisher := newRunScopedOverlayPublication().failRun("", imposed)

	enqueuer, err := NewQueueRenderEnqueuer(&freshRenderClient{})
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetArtifactPublisher(publisher)
	enqueuer.SetAsyncPublication(true)

	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := enqueuer.Wait(context.Background()); !errors.Is(err, imposed) {
		t.Fatalf("unbound join = %v, want the publication failure %v", err, imposed)
	}
	if err := enqueuer.Wait(context.Background()); err != nil {
		t.Fatalf("a joined failure must be taken, not re-reported: %v", err)
	}
}
