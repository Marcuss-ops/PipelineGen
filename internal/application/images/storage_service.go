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
	driveReader   drive.Reader
	cfg           *config.Config
	imagesDir     string
	tempDir       string
	driveFolderID string
	client        *http.Client
	dispatcher    *outbox.Dispatcher
	dedup         singleflight.Group
	log           *zap.Logger
	gaServerURL   string
	gaDownloadDir string
	vidsProjectID string
	meta          *MetadataService
	destResolver  destinations.DestinationResolver

	// retrievalRegistry composes Wikipedia/SearXNG/DuckDuckGo/Drive providers
	// for finding existing images. It is separate from AI generation.
	retrievalRegistry *retrieved.RetrievalProviderRegistry

	// subjectTags is the typed port for extracting subject slug + tag list
	// from a free-form description (PR C9, July 2026). Replaces the
	// silent-fake `extractSubjectAndTags` stub that violated godlike/07
	// no-fake-availability. Concrete wiring is inline in service.go::NewService.
	subjectTags SubjectTagsService
}
