package videocreate

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"go.uber.org/zap"

	steps "github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// fakeStageStore mirrors the real adapter's UPSERT semantics (one row per
// (job_id, stage), the latest producing step wins). Modelling the upsert
// rather than an append is what lets the assertions below talk about the
// stage TABLE instead of about individual writes.
type fakeStageStore struct {
	mu      sync.Mutex
	rows    []job.JobStageStatus
	upserts int
	err     error
}

func (f *fakeStageStore) UpsertJobStageStatus(_ context.Context, s job.JobStageStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts++
	if f.err != nil {
		return f.err
	}
	for i := range f.rows {
		if f.rows[i].JobID == s.JobID && f.rows[i].Stage == s.Stage {
			f.rows[i] = s
			return nil
		}
	}
	f.rows = append(f.rows, s)
	return nil
}

func (f *fakeStageStore) ListJobStageStatuses(context.Context, string) ([]job.JobStageStatus, error) {
	return nil, nil
}

func (f *fakeStageStore) snapshot() []job.JobStageStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]job.JobStageStatus, len(f.rows))
	copy(out, f.rows)
	return out
}

func (f *fakeStageStore) find(stage job.StageName) (job.JobStageStatus, bool) {
	for _, row := range f.snapshot() {
		if row.Stage == stage {
			return row, true
		}
	}
	return job.JobStageStatus{}, false
}

func newStageStatusTestRun(t *testing.T, store job.JobStageStatusStore) *Run {
	t.Helper()
	deps := Deps{Workspace: t.TempDir(), Log: zap.NewNop()}
	tools := &job.JobExecutionTools{StageStatus: store}
	return newRun(&job.Job{ID: "job-1", Type: "video.create"}, appjobs.VideoCreatePayload{}, deps, WorkflowState{}, tools)
}

func stepByKey(t *testing.T, key string) StepSpec {
	t.Helper()
	spec, ok := StepByKey(key)
	if !ok {
		t.Fatalf("step %q is not in the canonical ladder", key)
	}
	return spec
}

// TestCanonicalStageCoversTheWholeLadder is the completeness pin: EVERY step
// the workflow can execute must have a canonical stage to report into.
// A step added without a mapping would silently produce no stage row at all,
// which is exactly the "populated by callers only" failure this producer path
// exists to remove.
func TestCanonicalStageCoversTheWholeLadder(t *testing.T) {
	if len(WorkflowSteps) == 0 {
		t.Fatal("WorkflowSteps is empty")
	}
	for _, spec := range WorkflowSteps {
		canonical, ok := CanonicalStage(spec.Stage)
		if !ok {
			t.Fatalf("step %q (stage %s) has no canonical stage mapping", spec.StepKey, spec.Stage)
		}
		if !canonical.Valid() {
			t.Fatalf("step %q maps to %q, which is not a canonical kernel stage", spec.StepKey, canonical)
		}
	}
}

// TestCanonicalStagePinsTheOperatorFacingPhases pins the phases operators
// actually look for on a Calendar card / stage table. These are the
// video.create producers for the canonical stages, so a re-mapping that
// silently moved, say, publication onto the render row would fail here.
func TestCanonicalStagePinsTheOperatorFacingPhases(t *testing.T) {
	cases := []struct {
		stage Stage
		want  job.StageName
	}{
		{StageScripting, job.StageScript},      // script LLM
		{StageMediaSearch, job.StageStock},     // media discovery
		{StageMediaAcquire, job.StageClips},    // download + cut + register
		{StageVoiceover, job.StageVoiceover},   // voiceover
		{StageAudioMaster, job.StageVoiceover}, // audio master
		{StageOverlayPlan, job.StageOverlay},   // overlay plan
		{StageRendering, job.StageRender},      // render
		{StageAssembling, job.StageRender},     // assembly completes the render
		{StageAudioFinal, job.StageVoiceover},  // final mux
		{StageVerifying, job.StagePersistence}, // ffprobe + SHA-256 proof
		{StageFinalizing, job.StageUpload},     // Drive + media registry
	}
	for _, tc := range cases {
		got, ok := CanonicalStage(tc.stage)
		if !ok || got != tc.want {
			t.Fatalf("CanonicalStage(%s) = %q, %v; want %q, true", tc.stage, got, ok, tc.want)
		}
	}

	// An invented stage is refused rather than written into the table:
	// godlike/07 no-fake-availability — the table may not advertise a stage
	// with no producer.
	for _, invented := range []Stage{"FINAL_VIDEO_SENT", "FINAL_VIDEO_CREATED", ""} {
		if got, ok := CanonicalStage(invented); ok {
			t.Fatalf("CanonicalStage(%q) = %q, true; want false for a non-canonical stage", invented, got)
		}
	}
}

// TestStageTransitionsWriteTheCanonicalStageTable is the producer pin: the four
// transitions coordinator.go performs must each land as a durable row with the
// canonical stage, the canonical stage status and the step's band position.
func TestStageTransitionsWriteTheCanonicalStageTable(t *testing.T) {
	store := &fakeStageStore{}
	run := newStageStatusTestRun(t, store)
	ctx := context.Background()

	script := stepByKey(t, "01_script")
	voiceover := stepByKey(t, "04_voiceover")
	render := stepByKey(t, "07_render")

	run.stageStarted(ctx, script)
	run.stageSucceeded(ctx, script)
	run.stageSkipped(ctx, voiceover)
	run.stageFailed(ctx, render, "ffmpeg exploded")

	rows := store.snapshot()
	if len(rows) != 3 {
		t.Fatalf("stage table has %d rows (%+v), want one row per (job, stage)", len(rows), rows)
	}
	for _, row := range rows {
		if row.JobID != "job-1" {
			t.Fatalf("row %+v is not attributed to the running job", row)
		}
	}

	// The script stage ran to completion: the upsert must have REPLACED the
	// `running` row with the `completed` one, not appended a second row.
	got, ok := store.find(job.StageScript)
	if !ok {
		t.Fatal("no row for the canonical script stage")
	}
	if got.Status != job.StageCompleted || got.Progress != script.BandEnd {
		t.Fatalf("script row = %+v, want completed at band-end %d", got, script.BandEnd)
	}
	if got.Detail != script.Title+": completed" {
		t.Fatalf("script detail = %q, want operator-facing step description %q", got.Detail, script.Title+": completed")
	}

	// A skipped optional step reports `skipped`, NOT a completion.
	got, ok = store.find(job.StageVoiceover)
	if !ok {
		t.Fatal("no row for the canonical voiceover stage")
	}
	if got.Status != job.StageSkipped || got.Progress != voiceover.BandEnd {
		t.Fatalf("voiceover row = %+v, want skipped at band-end %d", got, voiceover.BandEnd)
	}
	if got.Detail != voiceover.Title+": optional step skipped by payload" {
		t.Fatalf("voiceover detail = %q, want the step and skip reason", got.Detail)
	}

	// A failed step reports `failed` plus the operator-facing reason.
	got, ok = store.find(job.StageRender)
	if !ok {
		t.Fatal("no row for the canonical render stage")
	}
	if got.Status != job.StageFailed || got.Detail != render.Title+": ffmpeg exploded" || got.Progress != render.BandStart {
		t.Fatalf("render row = %+v, want failed at band-start %d with the step and failure reason", got, render.BandStart)
	}
}

// TestStageStatusEmissionIsFailSoft pins the contract that makes it safe to
// emit from inside the render: a store failure (locked database, cancelled
// context) must never abort the workflow, and the transient progress bar must
// keep working.
func TestStageStatusEmissionIsFailSoft(t *testing.T) {
	store := &fakeStageStore{err: errors.New("database is locked")}
	run := newStageStatusTestRun(t, store)

	progress := 0
	run.Progress = func(int, string) { progress++ }

	script := stepByKey(t, "01_script")
	run.stageStarted(context.Background(), script)
	run.stageSucceeded(context.Background(), script)

	if progress != 2 {
		t.Fatalf("progress callbacks = %d, want 2: a stage-table failure must not stop progress reporting", progress)
	}
	if len(store.snapshot()) != 0 {
		t.Fatalf("store failure must not fabricate rows, got %+v", store.snapshot())
	}
	if store.upserts != 2 {
		t.Fatalf("upsert attempts = %d, want 2 (the emission must still be attempted)", store.upserts)
	}
}

type concurrentCompletionStore struct {
	*fakeStore
	completeOnStart bool
	completedOutput json.RawMessage
}

func (s *concurrentCompletionStore) MarkStarted(ctx context.Context, key steps.StepKey) error {
	if !s.completeOnStart {
		return s.fakeStore.MarkStarted(ctx, key)
	}
	s.completeOnStart = false
	if err := s.fakeStore.MarkStarted(ctx, key); err != nil {
		return err
	}
	if err := s.fakeStore.MarkCompleted(ctx, key, s.completedOutput, nil); err != nil {
		return err
	}
	return steps.ErrStepAlreadyCompleted
}

func (f *fakeStageStore) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = nil
	f.upserts = 0
}

// expectedLadderStages is the canonical stage set a full video.create ladder
// produces. Several ladder steps share a canonical stage (the audio steps and
// the render/assemble pair), so this is deliberately SMALLER than the eleven
// step keys; `translation` is absent because no video.create step translates.
func expectedLadderStages() []job.StageName {
	return []job.StageName{
		job.StageScript, job.StageStock, job.StageClips, job.StageVoiceover,
		job.StageOverlay, job.StageRender, job.StagePersistence, job.StageUpload,
	}
}

// TestFullLadderPopulatesTheStageTable is the end-to-end producer proof: the
// REAL workflow (every step, driven through the handler exactly as the worker
// drives it) must leave the canonical stage table populated, with no help at
// all from the HTTP surface. This is the assertion that the table is written
// by the work itself.
func TestFullLadderPopulatesTheStageTable(t *testing.T) {
	children := newFakeChildren()
	deps, _, _ := newTestDeps(t, children, fakeProbe{})
	store := &fakeStageStore{}
	tools := &job.JobExecutionTools{StageStatus: store}

	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	if _, err := handler(context.Background(), testJob("job_stage_table", "calendar:item-9:2026-09-24"), tools); err != nil {
		t.Fatalf("handler: %v", err)
	}

	want := expectedLadderStages()
	rows := store.snapshot()
	if len(rows) != len(want) {
		t.Fatalf("stage table has %d rows (%+v), want %d", len(rows), rows, len(want))
	}
	for _, stage := range want {
		row, ok := store.find(stage)
		if !ok {
			t.Fatalf("no row for canonical stage %q after a full ladder run (rows=%+v)", stage, rows)
		}
		if row.JobID != "job_stage_table" {
			t.Fatalf("row %+v is not attributed to the running job", row)
		}
		if row.Status != job.StageCompleted && row.Status != job.StageSkipped {
			t.Fatalf("stage %q ended %q, want a truthful terminal status", stage, row.Status)
		}
		if row.Progress < 0 || row.Progress > 100 {
			t.Fatalf("stage %q progress %d out of range", stage, row.Progress)
		}
	}
	if _, ok := store.find(job.StageTranslation); ok {
		t.Fatal("the table advertised `translation`, which no video.create step performs")
	}

	// The last producer of each shared stage wins: publication ends the job at
	// 100, and `render` reflects the assembly tail rather than the bare
	// render band-start.
	upload, _ := store.find(job.StageUpload)
	if upload.Status != job.StageCompleted || upload.Progress != 100 {
		t.Fatalf("upload row = %+v, want completed at 100", upload)
	}
	render, _ := store.find(job.StageRender)
	if render.Progress < stepByKey(t, "08_assemble").BandEnd {
		t.Fatalf("render row = %+v, want the assembly tail (%d)", render, stepByKey(t, "08_assemble").BandEnd)
	}
}

// TestResumedLadderRepopulatesTheStageTable covers the restart case, which is
// when an operator most needs the table: after a crash-resume the ladder skips
// the already-durable steps, so without the resume emission the table would
// only show what ran AFTER the restart. Clearing the stage table between the
// two runs is what isolates the resume path from the first run's writes.
func TestResumedLadderRepopulatesTheStageTable(t *testing.T) {
	children := newFakeChildren()
	deps, _, _ := newTestDeps(t, children, fakeProbe{})
	store := &fakeStageStore{}
	tools := &job.JobExecutionTools{StageStatus: store}

	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	j := testJob("job_stage_resume", "calendar:item-10:2026-09-24")
	if _, err := handler(context.Background(), j, tools); err != nil {
		t.Fatalf("first run: %v", err)
	}
	store.reset()

	if _, err := handler(context.Background(), j, tools); err != nil {
		t.Fatalf("resumed run: %v", err)
	}

	want := expectedLadderStages()
	if got := len(store.snapshot()); got != len(want) {
		t.Fatalf("resumed run wrote %d stage rows (%+v), want the full table of %d", got, store.snapshot(), len(want))
	}
	for _, stage := range want {
		row, ok := store.find(stage)
		if !ok {
			t.Fatalf("resumed run left no row for canonical stage %q", stage)
		}
		if row.Status != job.StageCompleted && row.Status != job.StageSkipped {
			t.Fatalf("stage %q = %+v after resume, want a truthful terminal status", stage, row)
		}
	}
}

func TestCoordinatorResumePublishesCurrentProgress(t *testing.T) {
	state := NewWorkflowState()
	for _, spec := range WorkflowSteps {
		state.Stages[spec.StepKey].Status = StageSucceeded
	}
	state.Refresh()

	stageStore := &fakeStageStore{}
	run := newStageStatusTestRun(t, stageStore)
	run.State = state
	var progress int
	var message string
	run.Progress = func(value int, text string) {
		progress, message = value, text
	}
	coordinator := &Coordinator{Store: newFakeStore()}
	if err := coordinator.Run(context.Background(), run); err != nil {
		t.Fatalf("Coordinator.Run: %v", err)
	}

	if progress != 100 || message != "resumed: "+state.Describe() {
		t.Fatalf("resume progress = %d, %q; want 100 and current workflow description", progress, message)
	}
}

func TestConcurrentCompletedStepRestoresStageStatus(t *testing.T) {
	stageStore := &fakeStageStore{}
	run := newStageStatusTestRun(t, stageStore)
	run.State = NewWorkflowState()

	store := &concurrentCompletionStore{
		fakeStore: newFakeStore(), completeOnStart: true,
		completedOutput: mustRaw(StageOutput{ScriptAssetID: "winning-script", Scenes: []string{"scene-1"}}),
	}
	spec := stepByKey(t, "01_script")
	coordinator := &Coordinator{Store: store}
	if err := coordinator.runStep(context.Background(), run, spec); err != nil {
		t.Fatalf("runStep after concurrent completion: %v", err)
	}

	if !run.State.Completed(spec.StepKey) {
		t.Fatalf("concurrently completed step was not restored into workflow state: %s", run.State.Describe())
	}
	if run.Facts.ScriptAssetID != "winning-script" || len(run.Facts.Scenes) != 1 || run.Facts.Scenes[0] != "scene-1" {
		t.Fatalf("concurrent winner output was not rehydrated: %+v", run.Facts)
	}
	row, ok := stageStore.find(job.StageScript)
	if !ok {
		t.Fatal("concurrent completion did not repopulate the durable canonical stage row")
	}
	if row.Status != job.StageCompleted || row.Progress != spec.BandEnd || row.Detail != spec.Title+": completed" {
		t.Fatalf("stage row after concurrent completion = %+v, want completed %q at %d", row, spec.Title, spec.BandEnd)
	}
}

// TestStageStatusWithoutStoreIsNoOp pins the optional-port behaviour: a
// deployment (or test) without the durable projection keeps working, and the
// emission path is nil-safe instead of conditional at every call site.
func TestStageStatusWithoutStoreIsNoOp(t *testing.T) {
	run := newRun(&job.Job{ID: "job-1"}, appjobs.VideoCreatePayload{},
		Deps{Workspace: t.TempDir(), Log: zap.NewNop()}, WorkflowState{}, nil)
	if run.stageStatus != nil {
		t.Fatal("reporter must be nil when Deps.Stages is not wired")
	}

	script := stepByKey(t, "01_script")
	// None of these may panic.
	run.stageStarted(context.Background(), script)
	run.stageSucceeded(context.Background(), script)
	run.stageSkipped(context.Background(), script)
	run.stageFailed(context.Background(), script, "boom")

	// A job-less reporter is nil too (nothing to attribute the row to).
	if r := newStageStatusReporter(&fakeStageStore{}, "", zap.NewNop()); r != nil {
		t.Fatal("reporter must be nil without a job id")
	}
}
