package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	assetindex "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/stockbatches"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/filesystem"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// compositionSourceStager is deliberately small because this composition
// test verifies dependency propagation, not acquisition behavior.
type compositionSourceStager struct{}

func (compositionSourceStager) Prepare(context.Context, acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	return nil, nil
}
func (compositionSourceStager) Release(context.Context, string) error { return nil }

var _ acquisition.SourceStager = compositionSourceStager{}

type compositionSourceProbe struct{}

func (*compositionSourceProbe) ProbeDurationSec(context.Context, string) (float64, error) {
	return 60, nil
}

var _ stockpipeline.SourceDurationProbe = (*compositionSourceProbe)(nil)

type compositionCutter struct{}

func (compositionCutter) Cut(context.Context, stockpipeline.CutRequest) (stockpipeline.CutBatchResult, error) {
	return stockpipeline.CutBatchResult{}, nil
}

var _ stockpipeline.VideoCutter = compositionCutter{}

type compositionRenderer struct{}

func (compositionRenderer) Render(context.Context, stockpipeline.RenderRequest) (stockpipeline.RenderResult, error) {
	return stockpipeline.RenderResult{}, nil
}

var _ stockpipeline.StockRenderer = compositionRenderer{}

type compositionPublisherPort struct{}

func (compositionPublisherPort) Publish(context.Context, finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	return finalization.AssetLocation{Provider: "test", FileID: "composition-file"}, nil
}

var _ finalization.PublisherPort = compositionPublisherPort{}

type compositionProjection struct{}

func (compositionProjection) Project(context.Context, *job.ArtifactManifest) error { return nil }

var _ stockpipeline.ProjectionPort = compositionProjection{}

type compositionFolderCreator struct{}

func (compositionFolderCreator) GetOrCreateFolder(context.Context, string, string) (string, error) {
	return "composition-folder", nil
}

var _ stockpipeline.StockFolderCreator = compositionFolderCreator{}

func TestBuildStockBundle_WiresSourceDurationProbeIntoProductionService(t *testing.T) {
	log := zap.NewNop()
	db, err := storage.NewSQLiteDB(t.TempDir(), "stock-composition-probe.db", log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	jobsBundle, err := wiring.BuildJobsBundle(db, log, nil, nil, nil, nil)
	require.NoError(t, err)

	clipsRepo := sqassets.NewClipsRepository(db.DB, log)
	assetIndexService := assetindex.NewService(assetindex.NewRepository(db.DB))
	batchRepo := stockbatches.NewRepository(db.DB)
	probe := &compositionSourceProbe{}

	result, err := BuildStockBundle(StockBundleDeps{
		Runtime: StockRuntimeDeps{
			Cfg: &config.Config{
				Storage:  config.StorageConfig{DataDir: t.TempDir()},
				Features: config.FeaturesConfig{StockPipelineEnabled: true},
			},
			Log:        log,
			DB:         db.DB,
			JobCreator: jobsBundle.Repo,
			StepStore:  steps.NewSQLiteStore(db.DB),
		},
		Delivery: StockDeliveryDeps{
			Publisher:     noOpPublisher{},
			PublisherPort: compositionPublisherPort{},
			Finalizer:     noOpFinalizer{},
			Projection:    compositionProjection{},
		},
		Acquisition: StockAcquisitionDeps{
			SourceStager:    compositionSourceStager{},
			SourceProbe:     probe,
			ClipsRepo:       clipsRepo,
			AssetIndex:      assetIndexService,
			Dispatcher:      outbox.NewDispatcher(nil, nil, nil, nil, log),
			BatchRepository: batchRepo,
		},
		Media: StockMediaDeps{Cutter: compositionCutter{}, Renderer: compositionRenderer{}},
		Orchestration: StockOrchestrationDeps{
			Jobs:          jobsBundle.Service,
			FolderCreator: compositionFolderCreator{},
		},
		Feature:     StockFeatureGate{StockPipelineEnabled: func() bool { return true }},
		SourceCache: StockSourceCacheDeps{LocalFS: filesystem.NewLocal()},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Same(t, probe, result.Service.SourceDurationProbe(),
		"the production composition root must preserve the exact SourceDurationProbe instance")
}
