package images

import (
	"net/http"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/destinations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/retrieved"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ImageStorageService handles image storage, retrieval, Drive operations,
// web search, and media asset registration. It delegates metadata operations
// to MetadataService.
type ImageStorageService struct {
	repo          *assets.ImagesRepository
	stockRepo     *assets.ClipsRepository
	mediaStore    *drive.Store
	driveReader    drive.Reader
	cfg           *config.Config
	imagesDir     string
	tempDir       string
	driveFolderID string
	client        *http.Client
	dispatcher    *outbox.Dispatcher
	dedup         singleflight.Group
	nvidiaSem     chan struct{}
	log           *zap.Logger
	gaServerURL   string
	gaDownloadDir string
	vidsProjectID string
	meta          *MetadataService

	// destResolver (FASE 2D EXPAND, July 2026) maps a logical
	// destinationKey (e.g. "ai-images/cinematic") to a concrete Google
	// Drive folder ID. Wired in NewService via
	// ImagesStorageDeps.DestResolver. NOT YET CALLED by the ingest
	// path — the BACKFILL commit introduces dual-read verification,
	// and CUTOVER switches the call site over.
	destResolver destinations.DestinationResolver

	// retrievalRegistry (Step 8) composes Wikipedia/SearXNG/DuckDuckGo/Drive
	// providers in fallback order. nil = fetch via the legacy inline cascade
	// (preserved for back-compat with existing tests that pre-date Step 8).
	retrievalRegistry *retrieved.RetrievalProviderRegistry
}
