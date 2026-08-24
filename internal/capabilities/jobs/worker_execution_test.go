package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func TestExtractStagedArtifacts_HappyPath(t *testing.T) {
	// FASE 1 close-out: the happy-path fixture now sets Path on
	// every Required artefact so manifest.Validate() (gated inline
	// in extractStagedArtifacts per FASE 1 spec "the Required-empty-
	// path case → bloccare SUCCEEDED") accepts the manifest and
	// proceeds to the published-artifact projection. Pre-FASE-1
	// the short-circuit `manifest.Artifacts == 0 → []` reached
	// the projection unconditionally; the FASE 1 typed-error
	// gate short-circuits BEFORE Validate, so the projection
	// only runs against a Validate-passed manifest.
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-001",
		JobID:         "job_test_123:script_json",
		Artifacts: []job.Artifact{
			{
				ID:        "job_test_123:script_json",
				Kind:      job.ArtifactKindScriptJSON,
				Path:      "/tmp/pipelinegen/jobs/job_test_123/script.json",
				Filename:  "script.json",
				MIMEType:  "application/json",
				SizeBytes: 1024,
				SHA256:    "abc123",
				Required:  true,
			},
			{
				ID:        "job_test_123:script_text",
				Kind:      job.ArtifactKindScriptText,
				Path:      "/tmp/pipelinegen/jobs/job_test_123/script.txt",
				Filename:  "script.txt",
				MIMEType:  "text/plain",
				SizeBytes: 512,
				SHA256:    "def456",
				Required:  false,
			},
		},
	}

	result := map[string]any{
		job.ManifestKey: manifestToRawJSON(t, manifest),
	}

	raw, extractErr := extractStagedArtifacts(result, "script.generate")
	if extractErr != nil {
		t.Fatalf("expected nil err for valid manifest, got %v", extractErr)
	}

	if string(raw) == "[]" {
		t.Fatal("expected non-empty staged artifacts for a valid manifest")
	}

	var artifacts remote.StagedArtifacts
	if err := json.Unmarshal(raw, &artifacts); err != nil {
		t.Fatalf("failed to unmarshal staged artifacts: %v", err)
	}

	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}

	// First artifact: required
	a := artifacts[0]
	if a.ArtifactID != "job_test_123:script_json" {
		t.Errorf("ArtifactID: got %q, want %q", a.ArtifactID, "job_test_123:script_json")
	}
	if a.Destination != "script" {
		t.Errorf("Destination: got %q, want script", a.Destination)
	}
	if a.Filename != "script.json" {
		t.Errorf("Filename: got %q, want %q", a.Filename, "script.json")
	}
	if a.MIMEType != "application/json" {
		t.Errorf("MIMEType: got %q, want %q", a.MIMEType, "application/json")
	}
	if a.SizeBytes != 1024 {
		t.Errorf("SizeBytes: got %d, want %d", a.SizeBytes, 1024)
	}
	if a.SHA256 != "abc123" {
		t.Errorf("SHA256: got %q, want %q", a.SHA256, "abc123")
	}
	if a.Path == "" || a.Filename != "script.json" || a.SizeBytes != 1024 {
		t.Errorf("local artifact projection incomplete: path=%q filename=%q size=%d", a.Path, a.Filename, a.SizeBytes)
	}

	// Source derived from job type prefix
	// Second artifact: optional
	b := artifacts[1]
	if b.Destination != "script" || b.Path == "" {
		t.Errorf("optional artifact projection incomplete: destination=%q path=%q", b.Destination, b.Path)
	}
}

// TestExtractStagedArtifacts_OverlayManifestPreservesDriveRouting pins the
// manifest→bridge step of the probe→SHA256→manifest→publisher flow: an
// overlay.render manifest must project to the youtube_clip destination AND
// carry source=chronon + drive_subpath=[overlay] + probe sha256/size_bytes
// through to the staged reference the Sender-side publisher consumes.
func TestExtractStagedArtifacts_OverlayManifestPreservesDriveRouting(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-overlay",
		JobID:         "job_overlay:overlay:001",
		Artifacts: []job.Artifact{
			{
				ID:        "job_overlay:overlay:001",
				Kind:      job.ArtifactKindOverlay,
				Path:      "/tmp/pipelinegen/jobs/job_overlay/overlay_001.mov",
				Filename:  "overlay_001.mov",
				MIMEType:  "video/quicktime",
				SizeBytes: 1234567,
				SHA256:    "deadbeef",
				Required:  true,
				ArtifactMetadata: map[string]any{
					"source":        "chronon",
					"drive_subpath": []string{"overlay"},
				},
			},
		},
	}

	raw, err := extractStagedArtifacts(map[string]any{job.ManifestKey: manifestToRawJSON(t, manifest)}, "overlay.render")
	if err != nil {
		t.Fatalf("extractStagedArtifacts: %v", err)
	}

	var artifacts remote.StagedArtifacts
	if err := json.Unmarshal(raw, &artifacts); err != nil {
		t.Fatalf("unmarshal staged artifacts: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	a := artifacts[0]
	if a.Destination != "youtube_clip" {
		t.Fatalf("Destination = %q, want youtube_clip", a.Destination)
	}
	if a.SHA256 != "deadbeef" || a.SizeBytes != 1234567 {
		t.Fatalf("probe fields lost: sha256=%q size=%d", a.SHA256, a.SizeBytes)
	}
	if a.ArtifactMetadata["source"] != "chronon" {
		t.Fatalf("source = %v, want chronon", a.ArtifactMetadata["source"])
	}
	sub, ok := a.ArtifactMetadata["drive_subpath"].([]any)
	if !ok || len(sub) != 1 || sub[0] != "overlay" {
		t.Fatalf("drive_subpath = %#v, want [overlay]", a.ArtifactMetadata["drive_subpath"])
	}
}

func TestExtractStagedArtifacts_NilResult(t *testing.T) {
	// FASE 1 close-out typed-error contract: a nil handler result
	// surfaces job.ErrArtifactManifestMissing — the spec mandates
	// "il manifest è assente ... bloccare ... SUCCEEDED". The
	// pre-FASE-1 empty-pass behaviour is retired.
	raw, extractErr := extractStagedArtifacts(nil, "script.generate")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestMissing for nil result, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestMissing) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestMissing), got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
	}
}

func TestExtractStagedArtifacts_NoManifestKey(t *testing.T) {
	// FASE 1 close-out typed-error contract: handler result without
	// __artifact_manifest key surfaces job.ErrArtifactManifestMissing.
	result := map[string]any{
		"some_other_key": "value",
		"data":           map[string]any{"score": 0.95},
	}

	raw, extractErr := extractStagedArtifacts(result, "image.generate.google")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestMissing for absent __artifact_manifest key, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestMissing) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestMissing), got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
	}
}

func TestExtractStagedArtifacts_EmptyArtifactsList(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-001",
		JobID:         "job_test_empty",
		Artifacts:     []job.Artifact{},
	}

	result := map[string]any{
		job.ManifestKey: manifestToRawJSON(t, manifest),
	}

	raw, extractErr := extractStagedArtifacts(result, "script.generate")
	if extractErr != nil {
		t.Fatalf("expected nil err for empty manifest (back-compat: empty result OK), got %v", extractErr)
	}

	if string(raw) != "[]" {
		t.Fatalf("expected empty array for manifest with zero artifacts, got %s", string(raw))
	}
}

// TestExtractStagedArtifacts_DecodeFailure_TypedSentinel pins the
// FASE 1 close-out JSON-decode failure path. Renamed from
// TestExtractStagedArtifacts_MalformedManifest for clearer failure-
// attribution vs. the ValidateFailure_TypedSentinel below. The
// Decode-failure channel surfaces typed job.ErrArtifactManifestInvalid
// via the dual-%w form (godlike/06 SSOT: typed-sentinel wrap chained
// alongside the inner json error so errors.Is probes don't get a
// silent string-match devolution).
func TestExtractStagedArtifacts_DecodeFailure_TypedSentinel(t *testing.T) {
	result := map[string]any{
		job.ManifestKey: "not-valid-json",
	}

	raw, extractErr := extractStagedArtifacts(result, "books.process")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestInvalid for malformed manifest, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestInvalid) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestInvalid), got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
	}
}

// TestExtractStagedArtifacts_ValidateFailure_TypedSentinel pins
// the FASE 1 close-out Validate-failure path. A manifest that
// DECODES cleanly (valid JSON, shape matches) but fails Validate
// (e.g. empty schema_version, empty id/kind) surfaces typed
// job.ErrArtifactManifestInvalid via the dual-%w form. The inner
// Validate-returned sentinel propagates via errors.Is chain
// traversal, so callers probing sub-mode sentinels (e.g. for
// required-empty-path) resolve identically.
//
// Distinct from TestExtractStagedArtifacts_DecodeFailure_TypedSentinel
// (which exercises JSON-decode failures) — both pin DIFFERENT
// failure channels of the FASE 1 typed-error contract.
func TestExtractStagedArtifacts_ValidateFailure_TypedSentinel(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: "", // empty schema_version violates Validate
		Artifacts: []job.Artifact{
			{
				ID: "x", Kind: job.ArtifactKindScriptJSON,
				Path: "/tmp/x", Filename: "x", Required: true,
			},
		},
	}

	result := map[string]any{
		job.ManifestKey: manifestToRawJSON(t, manifest),
	}

	raw, extractErr := extractStagedArtifacts(result, "script.generate")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestInvalid for empty-schema_version manifest, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestInvalid) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestInvalid) on Validate failure, got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
	}
}

// TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing
// pins the FASE 1 close-out typed-error chain: a handler that emits a
// syntactically-decodable manifest whose required-artifact entry has
// an empty Path fails the worker with the typed
// job.ErrArtifactManifestInvalid sentinel (the Validate-wrapped chain
// that ALSO surfaces job.ErrRequiredArtifactMissing via Go 1.20+
// dual-%w semantics). The worker path can errors.Is against either
// typed sentinel — the publisher-side
// domain/finalization.ErrRequiredArtifactMissing is reachable through
// the alias in internal/domain/job/artifact_errors.go.
func TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing(t *testing.T) {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf_required_missing",
		JobID:         "job_required_missing",
		Artifacts: []job.Artifact{
			{
				ID:       "job_required_missing:script",
				Kind:     job.ArtifactKindScriptJSON,
				Path:     "", // EMPTY PATH for Required => invalid
				Filename: "script.json",
				Required: true,
			},
		},
	}

	result := map[string]any{
		job.ManifestKey: manifestToRawJSON(t, manifest),
	}

	raw, extractErr := extractStagedArtifacts(result, "script.generate")
	if extractErr == nil {
		t.Fatalf("expected typed ErrArtifactManifestInvalid for required-with-empty-path, got nil err (raw=%s)", string(raw))
	}
	if !errors.Is(extractErr, job.ErrArtifactManifestInvalid) {
		t.Fatalf("expected errors.Is(extractErr, ErrArtifactManifestInvalid) for missing-required-path case, got %T: %v", extractErr, extractErr)
	}
	if !errors.Is(extractErr, job.ErrRequiredArtifactMissing) {
		t.Fatalf("expected errors.Is(extractErr, ErrRequiredArtifactMissing) for missing-required-path case (dual-sentinel wrap), got %T: %v", extractErr, extractErr)
	}
	if raw != nil {
		t.Fatalf("expected nil json.RawMessage on error path, got %s", string(raw))
	}
}

// manifestToRawJSON marshals a manifest to json.RawMessage for use as a
// handler result value. Uses json.Marshal so job.Decode recognises it as
// a []byte payload and unmarshals it back into *ArtifactManifest.
func manifestToRawJSON(t *testing.T, m *job.ArtifactManifest) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	return json.RawMessage(b)
}

type jobRegistryRecorderFake struct {
	jobs      []capjobregistry.Job
	steps     []capjobregistry.Step
	metrics   []capjobregistry.Metric
	relations []capjobregistry.AssetRelation
	events    []capjobregistry.Event
}

func (f *jobRegistryRecorderFake) RecordJob(_ context.Context, j capjobregistry.Job) error {
	f.jobs = append(f.jobs, j)
	return nil
}
func (f *jobRegistryRecorderFake) UpdateJob(_ context.Context, j capjobregistry.Job) error {
	f.jobs = append(f.jobs, j)
	return nil
}
func (f *jobRegistryRecorderFake) RecordStep(_ context.Context, s capjobregistry.Step) error {
	f.steps = append(f.steps, s)
	return nil
}
func (f *jobRegistryRecorderFake) RecordMetric(_ context.Context, m capjobregistry.Metric) error {
	f.metrics = append(f.metrics, m)
	return nil
}
func (f *jobRegistryRecorderFake) RelateAsset(_ context.Context, a capjobregistry.AssetRelation) error {
	f.relations = append(f.relations, a)
	return nil
}
func (f *jobRegistryRecorderFake) AppendEvent(_ context.Context, e capjobregistry.Event) (int64, error) {
	f.events = append(f.events, e)
	return int64(len(f.events)), nil
}
func (f *jobRegistryRecorderFake) Stats(context.Context, string, string) (capjobregistry.Stats, error) {
	return capjobregistry.Stats{}, nil
}

func TestJobRegistryRecorder_RecordsCanonicalFinalizationAssetIDs(t *testing.T) {
	fake := &jobRegistryRecorderFake{}
	recorder := NewJobRegistryRecorder(fake, nil)
	recorder.RecordCanonicalOutputs(context.Background(), "job-canonical", "RENDERED", []string{"asset-1", "asset-2", ""})
	if len(fake.relations) != 2 {
		t.Fatalf("expected two canonical output relations, got %+v", fake.relations)
	}
	for i, relation := range fake.relations {
		if relation.JobID != "job-canonical" || relation.Relation != "RENDERED" || relation.Ordinal != i {
			t.Fatalf("unexpected canonical relation %d: %+v", i, relation)
		}
	}
}

func TestJobRegistryRecorder_PreservesPayloadAndRuntimeLineage(t *testing.T) {
	fake := &jobRegistryRecorderFake{}
	recorder := NewJobRegistryRecorder(fake, nil)
	started := time.Now().UTC().Add(-time.Second)
	j := &job.Job{ID: "job-ledger-test", Type: "script.generate", Status: job.StatusRunning, Revision: 3, Payload: json.RawMessage(`{"parent_job_id":"parent-1","input_assets":[{"asset_id":"input-1"}]}`), CreatedAt: started.Add(-time.Second), StartedAt: &started}
	stepID := recorder.Start(context.Background(), j, "worker-1", "attempt-1")
	report := &kernobs.RunReport{RunID: "run-1", JobID: j.ID, AttemptID: "attempt-1", Status: kernobs.StatusSucceeded, WallTimeMs: 42, QueueWaitMs: 7, Operations: []kernobs.OperationReport{{Operation: "whisper", DurationMs: 11, Items: 1, Bytes: 12}}}
	recorder.Finish(context.Background(), j, stepID, "worker-1", "attempt-1", "SUCCEEDED", []byte(`{"output_assets":[{"asset_id":"output-1"}]}`), nil, report)
	if len(fake.jobs) != 2 || fake.jobs[0].PayloadJSON != string(j.Payload) || fake.jobs[1].PayloadJSON != string(j.Payload) {
		t.Fatalf("payload was not preserved in both lifecycle writes: %+v", fake.jobs)
	}
	if len(fake.steps) < 2 {
		t.Fatalf("expected start and terminal steps, got %d", len(fake.steps))
	}
	if len(fake.metrics) < 5 {
		t.Fatalf("expected runtime metrics, got %d", len(fake.metrics))
	}
	if len(fake.relations) != 2 || fake.relations[0].Relation != "INPUT" || fake.relations[1].Relation != "GENERATED" {
		t.Fatalf("lineage = %+v", fake.relations)
	}
	if len(fake.events) != 2 || fake.events[0].EventType != "JOB_CLAIMED" || fake.events[1].EventType != "JOB_COMPLETED" {
		t.Fatalf("events = %+v", fake.events)
	}
}

// TestJobRegistryRecorder_FinishAnchorsStartedAtToRunStart pins the
// telemetry anchoring fix: started_at and worker.execution.started_at
// must be the run's start (RunReport.StartedAt), not the claim (job.StartedAt),
// so completed_at − started_at reconciles with duration_ms (RunReport.WallTimeMs).
func TestJobRegistryRecorder_FinishAnchorsStartedAtToRunStart(t *testing.T) {
	fake := &jobRegistryRecorderFake{}
	recorder := NewJobRegistryRecorder(fake, nil)

	claim := time.Now().UTC().Add(-5 * time.Second)
	runStart := time.Now().UTC().Add(-4 * time.Second)
	j := &job.Job{ID: "job-anchor-finish", Type: "script.generate", Status: job.StatusRunning, Revision: 1, CreatedAt: claim.Add(-time.Second), StartedAt: &claim}

	report := &kernobs.RunReport{
		RunID:      "run-anchor-finish",
		JobID:      j.ID,
		AttemptID:  "attempt-anchor-finish",
		Status:     kernobs.StatusSucceeded,
		StartedAt:  runStart,
		FinishedAt: runStart.Add(3 * time.Second),
		WallTimeMs: 3000,
	}
	stepID := recorder.Start(context.Background(), j, "worker-1", "attempt-anchor-finish")
	recorder.Finish(context.Background(), j, stepID, "worker-1", "attempt-anchor-finish", "SUCCEEDED", nil, nil, report)

	wantStarted := runStart.UTC().Format(time.RFC3339Nano)
	if len(fake.jobs) != 2 {
		t.Fatalf("jobs writes = %d, want 2", len(fake.jobs))
	}
	if got := fake.jobs[1].StartedAt; got != wantStarted {
		t.Fatalf("started_at = %q, want run start %q (not claim)", got, wantStarted)
	}
	if got := fake.jobs[1].DurationMS; got != 3000 {
		t.Fatalf("duration_ms = %d, want 3000 (wall time)", got)
	}

	lastStep := fake.steps[len(fake.steps)-1]
	if lastStep.StepName != "worker.execution" {
		t.Fatalf("last step = %q, want worker.execution", lastStep.StepName)
	}
	if got := lastStep.StartedAt; got != wantStarted {
		t.Fatalf("worker.execution.started_at = %q, want run start %q (not claim)", got, wantStarted)
	}
	if got := lastStep.DurationMS; got != 3000 {
		t.Fatalf("worker.execution.duration_ms = %d, want 3000 (wall time)", got)
	}
}

// TestJobRegistryRecorder_StartAnchorsToRunFromContext pins the RUNNING-state
// anchoring: when a run is bound to the context, Start writes worker.execution
// and the job row with the run's start, not the claim — so the terminal upsert
// (which never overwrites started_at) preserves the correct anchor.
func TestJobRegistryRecorder_StartAnchorsToRunFromContext(t *testing.T) {
	fake := &jobRegistryRecorderFake{}
	recorder := NewJobRegistryRecorder(fake, nil)

	claim := time.Now().UTC().Add(-5 * time.Second)
	j := &job.Job{ID: "job-anchor-start", Type: "script.generate", Status: job.StatusRunning, Revision: 1, CreatedAt: claim.Add(-time.Second), StartedAt: &claim}

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{JobID: j.ID, JobType: j.Type, AttemptID: "attempt-anchor-start"})
	runStart := run.Report().StartedAt.UTC().Format(time.RFC3339Nano)

	recorder.Start(kernobs.WithRun(context.Background(), run), j, "worker-1", "attempt-anchor-start")

	if got := fake.jobs[len(fake.jobs)-1].StartedAt; got != runStart {
		t.Fatalf("started_at = %q, want run start %q (not claim)", got, runStart)
	}
	step := fake.steps[len(fake.steps)-1]
	if got := step.StartedAt; got != runStart {
		t.Fatalf("worker.execution.started_at = %q, want run start %q (not claim)", got, runStart)
	}
}
