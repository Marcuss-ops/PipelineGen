package images

import (
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	persistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/destinations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/retrieved"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ImageStorageService handles image storage, retrieval, Drive operations,
// web search, and image asset registration. It delegates metadata operations
// to MetadataService.
//
// ImageStorageService writes assets atomically via Publisher and
// AssetCommitter. The legacy drive.Store / mediaStore surface has
// been removed.
type ImageStorageService struct {
	repo          ImageRepository
	publisher     delivery.Publisher
	driveReader   DriveReader
	cfg           *config.Config
	imagesDir     string
	tempDir       string
	driveFolderID string
	client        RemoteFetchPort
	// committer is the canonical SINGLE-transaction asset commit surface
	// for image ingest. Used by ingestDirect to atomically write
	// media_assets + asset_locations + typed metadata + the
	// asset.index.requested outbox event inside a single SQLite
	// transaction.
	committer persistence.AssetCommitter
	// sourceStager is the canonical port for staging remote URLs into
	// deterministic local files. downloadAndIngest routes web image
	// downloads through it.
	sourceStager  acquisition.SourceStager
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
	// Pace the shared Commons API across concurrent VidRush segment queries.
	// Without this process-wide gate, the bounded fan-out still produces a
	// burst that Commons answers with HTTP 429 and an empty candidate set.
	commonsSearchMu   sync.Mutex
	commonsLastSearch time.Time

	// subjectTags is the typed port for extracting subject slug + tag list
	// from a free-form description (PR C9, July 2026). Replaces the
	// silent-fake `extractSubjectAndTags` stub that violated godlike/07
	// no-fake-availability. Concrete wiring is inline in service.go::NewService.
	subjectTags SubjectTagsService
}
