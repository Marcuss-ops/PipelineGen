package stock

import (
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	ytService "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

// StockBundle is the typed composition input for the stock pipeline.
type StockBundle struct {
	DriveUploader      *driveup.Uploader
	Jobs               *appjobs.Service
	JobFacade          jobs.Service
	AssetIndexService  *assetindex.Service
	ClipsRepo          *assets.ClipsRepository
	YoutubeClipService *ytService.Service
	ClipIndexerService *clipindexer.Service
	Dispatcher         *outbox.Dispatcher
	Publisher          delivery.Publisher
}

// StockPipelineWiring is the published stock pipeline surface.
type StockPipelineWiring struct {
	Module      api.Module
	BatchModule api.Module
	Service     *stockpipeline.Service
}
