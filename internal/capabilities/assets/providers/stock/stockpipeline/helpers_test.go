package stockpipeline

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/plan"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

type mockCutter struct {
	lastReq core.CutRequest
	res     core.CutBatchResult
	err     error
	called  int
}

func (m *mockCutter) Cut(ctx context.Context, req core.CutRequest) (core.CutBatchResult, error) {
	m.called++
	m.lastReq = req
	return m.res, m.err
}

type fakeStepRunner struct {
	runInput *core.RunInput
	cfg      core.OrchestratorConfig
	state    *core.RunState
	planner  core.ClipPlanner
	cutter   core.VideoCutter
}

func (f *fakeStepRunner) Cfg() core.OrchestratorConfig                                 { return f.cfg }
func (f *fakeStepRunner) RunInput() *core.RunInput                                     { return f.runInput }
func (f *fakeStepRunner) JobID() string                                                { return "test-job" }
func (f *fakeStepRunner) PolicyVersion() string                                        { return f.cfg.PolicyVersion }
func (f *fakeStepRunner) Planner() core.ClipPlanner                                    { return f.planner }
func (f *fakeStepRunner) SourceStager() acquisition.SourceStager                       { return nil }
func (f *fakeStepRunner) Cutter() core.VideoCutter                                     { return f.cutter }
func (f *fakeStepRunner) Builder() core.ManifestBuilder                                { return nil }
func (f *fakeStepRunner) Writer() core.TransactionalAssetWriter                        { return nil }
func (f *fakeStepRunner) Projection() core.ProjectionPort                              { return nil }
func (f *fakeStepRunner) SourceDurationProbe() core.SourceDurationProbe                { return nil }
func (f *fakeStepRunner) ArtifactPreparation() finalization.ArtifactPreparationService { return nil }
func (f *fakeStepRunner) JobFinalizer() finalization.JobFinalizer                      { return nil }
func (f *fakeStepRunner) RunFingerprint() string                                       { return "test-fingerprint" }
func (f *fakeStepRunner) DestinationReconciler() core.DestinationReconciler            { return nil }
func (f *fakeStepRunner) Log() *zap.Logger                                             { return zap.NewNop() }
func (f *fakeStepRunner) LocalFS() core.LocalFSPort                                    { return newRealishFakeLocalFS() }
func (f *fakeStepRunner) State() *core.RunState                                        { return f.state }
func (f *fakeStepRunner) BatchRepository() core.StockBatchRepository                   { return nil }

func newFakeRunner(clips []core.ClipSpec, clipDur int, policyVer string) *fakeStepRunner {
	if policyVer == "" {
		policyVer = "test-policy-v1"
	}
	return &fakeStepRunner{
		runInput: &core.RunInput{
			Clips:        clips,
			ClipDuration: clipDur,
			TotalMinutes: 1,
		},
		cfg: core.OrchestratorConfig{
			PolicyVersion: policyVer,
		},
		state:   &core.RunState{},
		planner: plan.NewDeterministicPlanner(),
	}
}

// boundedStagingStager measures peak concurrent Prepare calls and can inject
// per-URL failures.
type boundedStagingStager struct {
	mu        sync.Mutex
	active    int
	maxActive int
	delay     time.Duration
	failURLs  map[string]bool
}

var _ acquisition.SourceStager = (*boundedStagingStager)(nil)

func (s *boundedStagingStager) Prepare(ctx context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()

	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.failURLs[req.Source.URL] {
		return nil, errors.New("injected staging failure")
	}
	return &acquisition.PrepareContext{ID: req.Source.URL, LocalPath: "/staged/" + req.Source.URL, SizeBytes: 2048}, nil
}

func (s *boundedStagingStager) Release(_ context.Context, _ string) error { return nil }

type stagingFakeRunner struct {
	*fakeStepRunner
	stager acquisition.SourceStager
}

func (r *stagingFakeRunner) SourceStager() acquisition.SourceStager { return r.stager }

func newStagingFakeRunner(plans []core.ClipPlan, stager acquisition.SourceStager, maxDownloads int) *stagingFakeRunner {
	return &stagingFakeRunner{
		fakeStepRunner: &fakeStepRunner{
			runInput: &core.RunInput{ClipDuration: 5, TotalMinutes: 1},
			cfg:      core.OrchestratorConfig{PolicyVersion: "test-policy-v1", MaxConcurrentDownloads: maxDownloads},
			state:    &core.RunState{Plan: plans},
		},
		stager: stager,
	}
}
