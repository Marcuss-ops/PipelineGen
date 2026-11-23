package publish

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// TestStockPipelineLegacyContractsCompile pins the public and orchestration
// seams used by callers. Any incompatible change to StepRunner or the
// existing ports fails at compile time rather than during integration.
//
// The neutral reconcile package (and its boundary test
// TestReconcileBoundaryHasNoBackImport) was deleted in the dead-code sweep —
// it had zero production importers and was reachable only from that test.
func TestStockPipelineLegacyContractsCompile(t *testing.T) {
	var _ core.Step = StockPublishStep{}
	var _ core.Step = finalize.StockFinalizeStep{}
	var _ core.StepRunner = (*boundaryRunner)(nil)
	var _ acquisition.SourceStager = (*boundaryStager)(nil)
	var _ finalization.ArtifactPreparationService = (*boundaryPreparation)(nil)
	var _ core.StockBatchRepository = (*boundaryBatchRepository)(nil)
}

type boundaryRunner struct{}

func (*boundaryRunner) Cfg() core.OrchestratorConfig                                 { return core.OrchestratorConfig{} }
func (*boundaryRunner) RunInput() *core.RunInput                                     { return nil }
func (*boundaryRunner) JobID() string                                                { return "" }
func (*boundaryRunner) PolicyVersion() string                                        { return "" }
func (*boundaryRunner) Planner() core.ClipPlanner                                    { return nil }
func (*boundaryRunner) SourceStager() acquisition.SourceStager                       { return nil }
func (*boundaryRunner) Cutter() core.VideoCutter                                     { return nil }
func (*boundaryRunner) Builder() core.ManifestBuilder                                { return nil }
func (*boundaryRunner) Writer() core.TransactionalAssetWriter                        { return nil }
func (*boundaryRunner) Projection() core.ProjectionPort                              { return nil }
func (*boundaryRunner) SourceDurationProbe() core.SourceDurationProbe                { return nil }
func (*boundaryRunner) BatchRepository() core.StockBatchRepository                   { return nil }
func (*boundaryRunner) LocalFS() core.LocalFSPort                                    { return nil }
func (*boundaryRunner) ArtifactPreparation() finalization.ArtifactPreparationService { return nil }
func (*boundaryRunner) JobFinalizer() finalization.JobFinalizer                      { return nil }
func (*boundaryRunner) RunFingerprint() string                                       { return "" }
func (*boundaryRunner) DestinationReconciler() core.DestinationReconciler            { return nil }
func (*boundaryRunner) Log() *zap.Logger                                             { return zap.NewNop() }
func (*boundaryRunner) State() *core.RunState                                        { return nil }

// The remaining fake methods pin the legacy acquisition/finalization ports.
type boundaryStager struct{}

func (*boundaryStager) Prepare(context.Context, acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	return nil, nil
}
func (*boundaryStager) Release(context.Context, string) error { return nil }

type boundaryPreparation struct{}

func (*boundaryPreparation) Prepare(context.Context, finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	return finalization.PublishedArtifact{}, nil
}

type boundaryBatchRepository struct{}

func (*boundaryBatchRepository) CreateBatch(context.Context, *core.StockBatch) error { return nil }
func (*boundaryBatchRepository) GetBatch(context.Context, string) (*core.StockBatch, error) {
	return nil, nil
}
func (*boundaryBatchRepository) UpdateBatchStatus(context.Context, string, core.BatchState, string) error {
	return nil
}
func (*boundaryBatchRepository) CreateGroup(context.Context, *core.StockBatchGroup) error { return nil }
func (*boundaryBatchRepository) GetGroup(context.Context, string) (*core.StockBatchGroup, error) {
	return nil, nil
}
func (*boundaryBatchRepository) UpdateGroupStatus(context.Context, string, core.GroupState, string) error {
	return nil
}
func (*boundaryBatchRepository) ListGroups(context.Context, string) ([]core.StockBatchGroup, error) {
	return nil, nil
}
func (*boundaryBatchRepository) CreateArtifact(context.Context, *core.StockArtifact) error {
	return nil
}
func (*boundaryBatchRepository) GetArtifact(context.Context, string) (*core.StockArtifact, error) {
	return nil, nil
}
func (*boundaryBatchRepository) MarkArtifactExtracting(context.Context, string) error { return nil }
func (*boundaryBatchRepository) MarkArtifactExtracted(context.Context, string, string, string, int) error {
	return nil
}
func (*boundaryBatchRepository) MarkArtifactPublished(context.Context, string, string, string, string) error {
	return nil
}
func (*boundaryBatchRepository) MarkArtifactVerified(context.Context, string) error { return nil }
func (*boundaryBatchRepository) MarkArtifactFailed(context.Context, string, core.ArtifactState, string) error {
	return nil
}
func (*boundaryBatchRepository) MarkGroupSucceeded(context.Context, string, int) error { return nil }
func (*boundaryBatchRepository) MarkBatchSucceeded(context.Context, string, int) error { return nil }
func (*boundaryBatchRepository) FindIncompleteArtifacts(context.Context, string, int) ([]core.StockArtifact, error) {
	return nil, nil
}
