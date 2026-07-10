package stockpipeline

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

type stageURLRecordingRunner struct {
	state  *runState
	stager assets.SourceStager
}

func (r *stageURLRecordingRunner) Cfg() OrchestratorConfig                  { return OrchestratorConfig{} }
func (r *stageURLRecordingRunner) RunInput() *RunInput                      { return &RunInput{} }
func (r *stageURLRecordingRunner) JobID() string                            { return "stage-url-test" }
func (r *stageURLRecordingRunner) PolicyVersion() string                    { return "v1" }
func (r *stageURLRecordingRunner) Planner() ClipPlanner                     { return nil }
func (r *stageURLRecordingRunner) SourceStager() assets.SourceStager        { return r.stager }
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
func (r *stageURLRecordingRunner) State() *runState                        { return r.state }

var _ StepRunner = (*stageURLRecordingRunner)(nil)

type stageURLRecordingStager struct {
	lastURL string
}

var _ assets.SourceStager = (*stageURLRecordingStager)(nil)

func (s *stageURLRecordingStager) StageSource(_ context.Context, ref assets.SourceRef) (*assets.StagedAsset, error) {
	s.lastURL = ref.URL
	return &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", Bytes: 1}, nil
}

func (s *stageURLRecordingStager) Cleanup(_ context.Context, _ *assets.StagedAsset) error { return nil }

func TestStockStageSourcesStep_CanonicalizesYouTubeURL(t *testing.T) {
	stager := &stageURLRecordingStager{}
	runner := &stageURLRecordingRunner{
		stager: stager,
		state: &runState{
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
