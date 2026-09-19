package stockpipeline

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
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
	var _ Step = StockPublishStep{}
	var _ Step = StockFinalizeStep{}
	var _ StepRunner = (*boundaryRunner)(nil)
	var _ acquisition.SourceStager = (*boundaryStager)(nil)
	var _ finalization.ArtifactPreparationService = (*boundaryPreparation)(nil)
	var _ StockBatchRepository = (*boundaryBatchRepository)(nil)
}

type boundaryRunner struct{}

func (*boundaryRunner) Cfg() OrchestratorConfig                                      { return OrchestratorConfig{} }
func (*boundaryRunner) RunInput() *RunInput                                          { return nil }
func (*boundaryRunner) JobID() string                                                { return "" }
func (*boundaryRunner) PolicyVersion() string                                        { return "" }
func (*boundaryRunner) Planner() ClipPlanner                                         { return nil }
func (*boundaryRunner) SourceStager() acquisition.SourceStager                       { return nil }
func (*boundaryRunner) Cutter() VideoCutter                                          { return nil }
func (*boundaryRunner) Renderer() StockRenderer                                      { return nil }
func (*boundaryRunner) Builder() ManifestBuilder                                     { return nil }
func (*boundaryRunner) Writer() TransactionalAssetWriter                             { return nil }
func (*boundaryRunner) Projection() ProjectionPort                                   { return nil }
func (*boundaryRunner) SourceDurationProbe() SourceDurationProbe                     { return nil }
func (*boundaryRunner) BatchRepository() StockBatchRepository                        { return nil }
func (*boundaryRunner) LocalFS() LocalFSPort                                         { return nil }
func (*boundaryRunner) ArtifactPreparation() finalization.ArtifactPreparationService { return nil }
func (*boundaryRunner) JobFinalizer() finalization.JobFinalizer                      { return nil }
func (*boundaryRunner) RunFingerprint() string                                       { return "" }
func (*boundaryRunner) DestinationReconciler() DestinationReconciler                 { return nil }
func (*boundaryRunner) Log() *zap.Logger                                             { return zap.NewNop() }
func (*boundaryRunner) State() *RunState                                             { return nil }

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

func (*boundaryBatchRepository) CreateBatch(context.Context, *StockBatch) error { return nil }
func (*boundaryBatchRepository) GetBatch(context.Context, string) (*StockBatch, error) {
	return nil, nil
}
func (*boundaryBatchRepository) UpdateBatchStatus(context.Context, string, BatchState, string) error {
	return nil
}
func (*boundaryBatchRepository) CreateGroup(context.Context, *StockBatchGroup) error { return nil }
func (*boundaryBatchRepository) GetGroup(context.Context, string) (*StockBatchGroup, error) {
	return nil, nil
}
func (*boundaryBatchRepository) UpdateGroupStatus(context.Context, string, GroupState, string) error {
	return nil
}
func (*boundaryBatchRepository) ListGroups(context.Context, string) ([]StockBatchGroup, error) {
	return nil, nil
}
func (*boundaryBatchRepository) CreateArtifact(context.Context, *StockArtifact) error { return nil }
func (*boundaryBatchRepository) GetArtifact(context.Context, string) (*StockArtifact, error) {
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
func (*boundaryBatchRepository) MarkArtifactFailed(context.Context, string, ArtifactState, string) error {
	return nil
}
func (*boundaryBatchRepository) MarkGroupSucceeded(context.Context, string, int) error { return nil }
func (*boundaryBatchRepository) MarkBatchSucceeded(context.Context, string, int) error { return nil }
func (*boundaryBatchRepository) FindIncompleteArtifacts(context.Context, string, int) ([]StockArtifact, error) {
	return nil, nil
}
