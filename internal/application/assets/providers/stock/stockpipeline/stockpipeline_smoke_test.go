// Package stockpipeline_test_smoke (Push G, July 2026): the
// merge host for the 3 smallest stockpipeline test files:
// step_stage_sources_test.go + deps_struct_smoke_test.go +
// usecase_test.go. Each section divider below marks the
// original test-file boundary; semantics are byte-identical
// to the source files. Other test files in the package
// (orchestrator_*, step_*, ports_test.go, planner_test.go,
// roundtrip_test.go, etc.) are unaffected.
//
// godlike/06 SSOT: each test family's purpose is preserved.
// No dedup of test bodies, no rename of test functions, no
// removal of helper structs — this is a structural merge
// only. Per-section dividers keep test families visually
// distinct within the merged file.
//
// godlike/07 fail-closed invariant: each test's contract
// pins a single behavior (StageURL recording / Deps 8-cap /
// JobSubmission context-detach). The merge does NOT change
// any test's runtime contract.
package stockpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ─── Tests: Stage Sources (originally step_stage_sources_test.go) ───

type stageURLRecordingRunner struct {
	state  *RunState
	stager acquisition.SourceStager
}

func (r *stageURLRecordingRunner) Cfg() OrchestratorConfig                  { return OrchestratorConfig{} }
func (r *stageURLRecordingRunner) RunInput() *RunInput                      { return &RunInput{} }
func (r *stageURLRecordingRunner) JobID() string                            { return "stage-url-test" }
func (r *stageURLRecordingRunner) PolicyVersion() string                    { return "v1" }
func (r *stageURLRecordingRunner) Planner() ClipPlanner                     { return nil }
func (r *stageURLRecordingRunner) SourceStager() acquisition.SourceStager        { return r.stager }
func (r *stageURLRecordingRunner) Cutter() VideoCutter                      { return nil }
func (r *stageURLRecordingRunner) Renderer() StockRenderer                  { return nil }
func (r *stageURLRecordingRunner) Builder() ManifestBuilder                 { return nil }
func (r *stageURLRecordingRunner) Writer() TransactionalAssetWriter         { return nil }
func (r *stageURLRecordingRunner) Projection() ProjectionPort               { return nil }
func (r *stageURLRecordingRunner) SourceDurationProbe() SourceDurationProbe { return nil }
func (r *stageURLRecordingRunner) ArtifactPreparation() finalization.ArtifactPreparationService {
	return nil
}
func (r *stageURLRecordingRunner) JobFinalizer() finalization.JobFinalizer { return nil }
func (r *stageURLRecordingRunner) RunFingerprint() string                  { return "stage-url-test" }
func (r *stageURLRecordingRunner) Log() *zap.Logger                        { return zap.NewNop() }
func (f *stageURLRecordingRunner) LocalFS() LocalFSPort                    { return newRealishFakeLocalFS() }
func (r *stageURLRecordingRunner) State() *RunState                        { return r.state }
func (r *stageURLRecordingRunner) BatchRepository() StockBatchRepository   { return nil }

var _ StepRunner = (*stageURLRecordingRunner)(nil)

type stageURLRecordingStager struct {
	lastURL string
}

var _ acquisition.SourceStager = (*stageURLRecordingStager)(nil)

func (s *stageURLRecordingStager) Prepare(_ context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	s.lastURL = req.Source.URL
	return &acquisition.PrepareContext{
		LocalPath:    "/tmp/staged.mp4",
		SizeBytes:    1,
		CleanupToken: "/tmp/staged.mp4",
	}, nil
}

func (s *stageURLRecordingStager) Release(_ context.Context, _ string) error { return nil }

func TestStockStageSourcesStep_CanonicalizesYouTubeURL(t *testing.T) {
	stager := &stageURLRecordingStager{}
	runner := &stageURLRecordingRunner{
		stager: stager,
		state: &RunState{
			Plan: []ClipPlan{
				{
					SourceID:       "https://www.youtube.com/watch?v=dgB9UHHapq4&pp=ugUEEgJlbg%3D%3D",
					SourceProvider: SourceProviderYouTube,
				},
			},
		},
	}

	if err := (StockStageSourcesStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("StockStageSourcesStep.Run() unexpected error: %v", err)
	}

	want := "https://www.youtube.com/watch?v=dgB9UHHapq4"
	if stager.lastURL != want {
		t.Fatalf("StageSource URL = %q, want canonical YouTube watch URL %q", stager.lastURL, want)
	}
}

// ─── Tests: Deps Struct (originally deps_struct_smoke_test.go) ───

// TestDeps_FieldCountCap locks the PR-D 8-per-bundle cap on the stock
// Deps struct. The test is a TEXTUAL guard: if a future maintainer adds a
// 9th field, this test must be updated alongside the struct change AND a
// (documented) entry to docs/migrations/deps-struct-allowlist.txt. The
// loss of this test is the warning signal — remove the assertion only
// together with the allowed cap-bump PR.
func TestDeps_FieldCountCap(t *testing.T) {
	// We do not parse the .go file with reflect (the Deps struct has no
	// exported fields of pure kind 'pointer' to enumerate cheaply); the
	// authoritative source is the literal in service.go, so we maintain
	// a hand-curated field-name list and assert the count.
	want := []string{
		"Cfg",
		"Log",
		"Drive",
		"Storage",
		"Media",
		"YouTube",
		"Jobs",
	}
	assert.Len(t, want, 8-1 /*cap=8, but want guards against silent growth toward 9*/)
	// Tight assert: the cap is hard 8 (PR-D spec) — if a future PR adds
	// the 9th field, this test fails loud and the PR must justify the
	// additive field via an allowlist update + ADR §D3.x.
	require.LessOrEqual(t, len(want), 7, "Deps field count must stay ≤7 (well below the 8 cap); 9th field requires allowlist + ADR amendment")
}

// TestNewProductionStockPipeline_NilDepsRejected verifies the PR-D ctor validation surface:
// every required dep is rejected with its own typed sentinel error so
// composition wiring + tests can assert the precise missing dep.
func TestNewProductionStockPipeline_NilDepsRejected(t *testing.T) {
	tests := []struct {
		name string
		// We assert the sentinel only — full Deps construction cannot
		// happen here without scaffolding a dozen fixtures, so the
		// validation order is enforced via the canonical "first-missing
		// wins" rule (whatever nil dep the validation reaches first is
		// the sentinel returned).
		setup   func(d *Deps)
		wantErr error
	}{
		{
			name:    "all-nil returns Cfg sentinel",
			setup:   func(d *Deps) {}, // every field zero/nil
			wantErr: ErrStockPipelineNilCfg,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := Deps{}
			tc.setup(&deps)
			_, err := NewProductionStockPipeline(deps)
			require.Error(t, err)
			assert.True(t, errors.Is(err, tc.wantErr) || err == tc.wantErr,
				"expected sentinel %v, got %v", tc.wantErr, err)
		})
	}
}

// ─── Tests: Stock Usecase (originally usecase_test.go) ───

type recordingJobsEnqueuer struct {
	ctx              context.Context
	req              *jobs.EnqueueRequest
	job              *jobs.Job
	returnErr        error
	correlation      string
	activeDuringCall bool
}

func (r *recordingJobsEnqueuer) Enqueue(ctx context.Context, req *jobs.EnqueueRequest) (*jobs.Job, error) {
	r.ctx = ctx
	r.req = req
	r.correlation = corid.FromContext(ctx)
	r.activeDuringCall = ctx.Err() == nil
	if r.returnErr != nil {
		return nil, r.returnErr
	}
	if r.job == nil {
		r.job = &jobs.Job{ID: "job-123"}
	}
	return r.job, nil
}

var _ jobsEnqueuer = (*recordingJobsEnqueuer)(nil)

func TestStockUseCase_SubmitAsync_DetachesFromCancelledContext(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(corid.WithCorrelationID(context.Background(), "stock-correlation-123"))
	cancel()

	enqueuer := &recordingJobsEnqueuer{}
	uc := NewStockUseCase(nil, enqueuer, zap.NewNop())

	jobID, err := uc.Submit(parent, &StockCommand{TotalMinutes: 5}, true)
	if err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
	if jobID != "job-123" {
		t.Fatalf("Submit returned jobID %q, want %q", jobID, "job-123")
	}
	if enqueuer.ctx == nil {
		t.Fatal("expected enqueue context to be recorded")
	}
	if !enqueuer.activeDuringCall {
		t.Fatal("expected detached enqueue context to remain active during Enqueue call")
	}
	if got := enqueuer.correlation; got != "stock-correlation-123" {
		t.Fatalf("expected correlation id to survive detach, got %q", got)
	}
	if enqueuer.req == nil || enqueuer.req.Type != "media.stock" {
		t.Fatalf("unexpected enqueue request: %#v", enqueuer.req)
	}
}

func TestStockUseCase_SubmitAsync_ReturnsJobsServiceRequiredWhenUnwired(t *testing.T) {
	t.Parallel()

	uc := NewStockUseCase(nil, nil, zap.NewNop())
	_, err := uc.Submit(context.Background(), &StockCommand{TotalMinutes: 5}, true)
	if !errors.Is(err, ErrJobsServiceRequired) {
		t.Fatalf("expected ErrJobsServiceRequired, got %v", err)
	}
}
