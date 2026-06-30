package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"golang.org/x/sync/singleflight"
	"net/http"
)

// ImageStorageService handles image storage, retrieval, Drive operations,
// web search, and media asset registration. It delegates metadata operations
// to MetadataService.
type ImageStorageService struct {
	repo          *assets.ImagesRepository
	stockRepo     *assets.ClipsRepository
	mediaStore    *drive.Store
	driveSvc      *driveapi.Service
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
}
