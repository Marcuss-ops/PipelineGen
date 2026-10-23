package wiring

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// overlayDriveRecordingPublisher records the real publication calls of the
// handler under test.
type overlayDriveRecordingPublisher struct {
	mu    sync.Mutex
	calls []scriptgen.OverlayPublicationSpec
	err   error
}

func (p *overlayDriveRecordingPublisher) PublishOverlay(_ context.Context, spec scriptgen.OverlayPublicationSpec, _ *scriptgen.RenderArtifact) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, spec)
	return p.err
}

func (p *overlayDriveRecordingPublisher) all() []scriptgen.OverlayPublicationSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]scriptgen.OverlayPublicationSpec(nil), p.calls...)
}

// overlayDriveCaptureRecorder implements the kernel recorder seam so a test
// can observe what the delivery run persists.
type overlayDriveCaptureRecorder struct {
	mu       sync.Mutex
	starts   []*kernobs.RunReport
	ops      []kernobs.OperationReport
	opsRunID []string
	finished int
}

func (r *overlayDriveCaptureRecorder) StartReport(_ context.Context, p *kernobs.RunReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, p)
	return nil
}
func (r *overlayDriveCaptureRecorder) AppendStage(context.Context, string, kernobs.StageReport) error {
	return nil
}
func (r *overlayDriveCaptureRecorder) AppendOperation(_ context.Context, runID string, op kernobs.OperationReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opsRunID = append(r.opsRunID, runID)
	r.ops = append(r.ops, op)
	return nil
}
func (r *overlayDriveCaptureRecorder) RecordChild(context.Context, *kernobs.RunReport) error {
	return nil
}
func (r *overlayDriveCaptureRecorder) SaveReport(context.Context, *kernobs.RunReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished++
	return nil
}
func (r *overlayDriveCaptureRecorder) operations() []kernobs.OperationReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]kernobs.OperationReport(nil), r.ops...)
}

func overlayDriveOutboxFixture(t *testing.T, jobStatus string) (*sqljobs.SQLiteStore, outboxevents.Event) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE jobs (id TEXT PRIMARY KEY, status TEXT NOT NULL);
		CREATE TABLE job_results (id INTEGER PRIMARY KEY, job_id TEXT, attempt INTEGER, result_hash TEXT, codec_id TEXT, result_payload TEXT, created_at TEXT);`); err != nil {
		t.Fatal(err)
	}
	const jobID = "job-overlay-outbox"
	if _, err := db.Exec(`INSERT INTO jobs(id,status) VALUES(?,?)`, jobID, jobStatus); err != nil {
		t.Fatal(err)
	}
	result := `{"run_id":"run-overlay","result":{"overlay_plan":{"plan_id":"plan-en","language":"en"}}}`
	if _, err := db.Exec(`INSERT INTO job_results(job_id,attempt,result_hash,codec_id,result_payload,created_at) VALUES(?,0,'h','json',?,'now')`, jobID, result); err != nil {
		t.Fatal(err)
	}
	req := renderinggen.OverlayDrivePublicationRequest{
		Spec: scriptgen.OverlayPublicationSpec{
			ScriptName: "golden", Language: "en", JobID: jobID, ResultJobID: jobID,
			PlanID: "plan-en:item:003", OverlayItemID: "phrase-003",
		},
		Artifact: scriptgen.RenderArtifact{SHA256: "sha-item-003", SizeBytes: 42, URL: "objectstore://sha-item-003", DriveLink: "https://drive.example/f"},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return sqljobs.NewSQLiteStore(db, zap.NewNop()), outboxevents.Event{
		ID: 7, EventType: renderinggen.EventOverlayDrivePublicationRequested,
		AggregateID: jobID, AggregateType: "overlay_artifact", PayloadJSON: string(payload),
	}
}

// TestOverlayDrivePublicationHandlerMeasuresRealUploadOnBoundRun pins the
// measurement of the REAL Drive upload: with a run bound on ctx the handler
// records exactly one drive_publish operation on that run (stage
// overlay_render, component drive) and the metadata carries the outbox path
// plus the job/item identity needed to join the upload to its overlay item.
func TestOverlayDrivePublicationHandlerMeasuresRealUploadOnBoundRun(t *testing.T) {
	store, evt := overlayDriveOutboxFixture(t, "SUCCEEDED")
	pub := &overlayDriveRecordingPublisher{}
	h := &overlayDrivePublicationHandler{direct: pub, results: store, observer: kernobs.NewRunObserver(nil)}

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: "job-render", AttemptID: "attempt-render"})
	ctx := kernobs.WithRun(context.Background(), run)
	if err := h.Handle(ctx, evt); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got := pub.all(); len(got) != 1 {
		t.Fatalf("PublishOverlay calls = %d, want 1", len(got))
	}
	ops := run.Report().Operations
	if len(ops) != 1 {
		t.Fatalf("recorded operations = %d, want 1: %+v", len(ops), ops)
	}
	op := ops[0]
	if op.Stage != string(kernobs.StageOverlayRender) || op.Component != string(kernobs.ComponentDrive) || op.Operation != string(kernobs.OperationDrivePublish) {
		t.Fatalf("operation identity = %s/%s/%s, want overlay_render/drive/drive_publish", op.Stage, op.Component, op.Operation)
	}
	for _, want := range []string{`"path":"outbox"`, `"item_id":"phrase-003"`, `"plan_id":"plan-en:item:003"`, `"artifact_sha":"sha-item-003"`} {
		if !strings.Contains(op.MetadataJSON, want) {
			t.Errorf("operation metadata %s missing %s", op.MetadataJSON, want)
		}
	}
}

// TestOverlayDrivePublicationHandlerBindsDeliveryRunWithoutOne pins the
// outbox worker case: no run is bound on the dispatch context, so the handler
// starts a delivery run, records the real upload on it, and finishes it. The
// measurement must survive the detached dispatch — that upload was the ONLY
// publication cost left invisible.
func TestOverlayDrivePublicationHandlerBindsDeliveryRunWithoutOne(t *testing.T) {
	store, evt := overlayDriveOutboxFixture(t, "SUCCEEDED")
	pub := &overlayDriveRecordingPublisher{}
	rec := &overlayDriveCaptureRecorder{}
	h := &overlayDrivePublicationHandler{
		direct: pub, results: store,
		observer: kernobs.NewRunObserverWithCollector(rec, nil),
	}
	if err := h.Handle(context.Background(), evt); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if got := pub.all(); len(got) != 1 {
		t.Fatalf("PublishOverlay calls = %d, want 1", len(got))
	}
	rec.mu.Lock()
	starts := append([]*kernobs.RunReport(nil), rec.starts...)
	finished := rec.finished
	rec.mu.Unlock()
	if len(starts) != 1 {
		t.Fatalf("delivery runs started = %d, want 1", len(starts))
	}
	if starts[0].JobID != "job-overlay-outbox" || starts[0].JobType != renderinggen.EventOverlayDrivePublicationRequested {
		t.Fatalf("delivery run identity = job %q type %q", starts[0].JobID, starts[0].JobType)
	}
	if finished != 1 {
		t.Fatalf("delivery runs finished = %d, want 1", finished)
	}
	ops := rec.operations()
	if len(ops) != 1 {
		t.Fatalf("recorded operations = %d, want 1: %+v", len(ops), ops)
	}
	if ops[0].Operation != string(kernobs.OperationDrivePublish) || ops[0].Component != string(kernobs.ComponentDrive) {
		t.Fatalf("operation identity = %s/%s, want drive/drive_publish", ops[0].Component, ops[0].Operation)
	}
}

// TestOverlayDrivePublicationHandlerSkipsMeasurementOnStaleEvent pins the
// stale-event path: no upload happens, so no upload may be measured. A
// measurement without the boundary call would be a fabricated fact.
func TestOverlayDrivePublicationHandlerSkipsMeasurementOnStaleEvent(t *testing.T) {
	// A RUNNING parent defers the publication (scheduling precondition):
	// the handler must neither publish nor record a drive_publish fact.
	store, evt := overlayDriveOutboxFixture(t, "RUNNING")
	pub := &overlayDriveRecordingPublisher{}
	rec := &overlayDriveCaptureRecorder{}
	h := &overlayDrivePublicationHandler{
		direct: pub, results: store,
		observer: kernobs.NewRunObserverWithCollector(rec, nil),
	}
	if err := h.Handle(context.Background(), evt); err == nil {
		t.Fatal("expected deferred error for non-terminal parent")
	}
	if got := pub.all(); len(got) != 0 {
		t.Fatalf("PublishOverlay calls = %d, want 0", len(got))
	}
	if ops := rec.operations(); len(ops) != 0 {
		t.Fatalf("recorded operations = %+v, want none", ops)
	}
}
