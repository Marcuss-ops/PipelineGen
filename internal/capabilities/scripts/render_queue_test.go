package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// fakeRenderQueueClient implements RenderQueueClient in-memory for the
// enqueuer tests. It lets a test pre-seed a job (to exercise the idempotent
// ErrJobExists path) or advance the job state between polls.
type fakeRenderQueueClient struct {
	jobs  map[string]RenderQueueJob
	calls int
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

// TestQueueRenderEnqueuerChrononPlan pins the production path that makes
// PipelineGen produce visual instructions for Chronon: the semantic
// GoldenOverlayPlanV1 is compiled to the chronon.render-plan.v1 document and
// submitted through the queue exactly as the RenderingGen worker expects it
// (render_plan + content-addressed assets), then the enqueuer waits for the
// certified artifact.
func TestQueueRenderEnqueuerChrononPlan(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond

	// Compile the golden semantic plan up front so the pre-seeded completion
	// can carry the right job id and the already-recorded spec/assets.
	compiled, err := capoverlay.CompileChrononPlan(capoverlay.GoldenOverlayPlanV1())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := compiled.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	client.jobs[compiled.Plan.JobID] = RenderQueueJob{
		ID:          compiled.Plan.JobID,
		JobType:     capoverlay.JobTypeRender,
		OverlaySpec: spec,
		Assets: []RenderQueueAsset{
			{Hash: capoverlay.GoldenBackgroundHash, URL: "assets/background.jpg"},
			{Hash: capoverlay.GoldenAppleHash, URL: "assets/apple.png"},
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

	// The submitted job must carry the chronon.render-plan.v1 document (not
	// the media render-plan.v2) and the content-addressed golden assets, so
	// the RenderingGen worker writes the plan Chronon actually executes.
	submitted, ok := client.jobs["golden-overlay-v1"]
	if !ok {
		t.Fatal("job was not submitted to the queue")
	}
	if submitted.JobType != capoverlay.JobTypeRender {
		t.Fatalf("submitted job type = %q, want %q", submitted.JobType, capoverlay.JobTypeRender)
	}
	var doc struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(submitted.OverlaySpec, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != capoverlay.ChrononSchema {
		t.Fatalf("submitted plan schema = %q, want %q", doc.Schema, capoverlay.ChrononSchema)
	}
	if len(submitted.Assets) != 2 {
		t.Fatalf("submitted assets = %d, want 2", len(submitted.Assets))
	}
	if submitted.Assets[0].Hash != capoverlay.GoldenBackgroundHash || submitted.Assets[0].URL != "assets/background.jpg" {
		t.Fatalf("asset 0 not projected: %+v", submitted.Assets[0])
	}
	if submitted.Assets[1].Hash != capoverlay.GoldenAppleHash || submitted.Assets[1].URL != "assets/apple.png" {
		t.Fatalf("asset 1 not projected: %+v", submitted.Assets[1])
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
// job whose id is "prepare-"+planID (idempotency key), whose spec round-trips
// back to the same intents, and whose assets are the deduplicated
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
	var got capoverlay.PrepareRequest
	if err := json.Unmarshal(job.OverlaySpec, &got); err != nil {
		t.Fatal(err)
	}
	if got.PlanID != req.PlanID || got.SchemaVersion != capoverlay.SchemaVersionPrepare {
		t.Fatalf("spec did not round-trip: %+v", got)
	}
	if len(got.Intents) != 2 || got.Intents[0].TemplateID != "person_default" || got.Intents[1].TimingState != capoverlay.TimingStatePending {
		t.Fatalf("intents not projected: %+v", got.Intents)
	}
	// Assets are deduplicated by content hash (case-insensitive).
	if len(job.Assets) != 1 || job.Assets[0].Hash != "abc123" || job.Assets[0].URL != "https://cdn.example.com/apple.png" {
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
