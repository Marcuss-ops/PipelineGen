package images

import (
	"net/http"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	assetapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	persistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
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
//
// PR-IMAGES-REMOVE-DRIVE-STORE (July 2026): the legacy `mediaStore
// *drive.Store` field (used for pre-scan path-only resolution + the
// AssetDestinationRequest→Publish bridge via s.publishToDrive) is
// RETIRED. The retained deps are: Local destination resolver
// (destResolver, YAML-backed), Publisher, AssetCommitter. The
// s.publishToDrive bridge + the mapMediaType* helpers are deleted
// with it (call sites in ingestDirect now call
// s.publisher.Publish(... delivery.PublishRequest{...}) directly).
type ImageStorageService struct {
	repo          *assets.ImagesRepository
	stockRepo     *assets.ClipsRepository
	publisher     delivery.Publisher
	driveReader   drive.Reader
	cfg           *config.Config
	imagesDir     string
	tempDir       string
	driveFolderID string
	client        *http.Client
	// dispatcher is retained (NOT removed) for the video + audio paths
	// in storage_drive.go that bypass CommitAsset. The image-ingest
	// path (ingestDirect) now uses committer exclusively for atomicity.
	dispatcher *outbox.Dispatcher
	// committer is the canonical SINGLE-transaction asset commit surface
	// for image ingest (PR-IMAGES-INGEST-ATOMIC, July 2026). Used by
	// ingestDirect to atomically write media_assets + asset_locations +
	// typed metadata + the asset.index.requested outbox event inside
	// a single SQLite transaction. Replaces the prior repo.AddImage +
	// dispatcher.EnqueueAndIndex two-transaction path that carried a
	// documented crash-risk window between SQLite and Qdrant.
	committer persistence.AssetCommitter
	// sourceStager is the canonical port for staging remote URLs into
	// deterministic local files (PR-SOURCESTAGER-CONSOLIDATE, July 2026).
	// downloadAndIngest routes web image downloads through StageSourceV2
	// so the inline `http.NewRequest + s.client.Do` boilerplate no
	// longer leaks into the processor. Nil fails closed with a typed
	// error (godlike/07) — the composition root MUST wire the stager
	// at NewService time. Pre-PR inline-http fallback was retired.
	sourceStager  assetapp.SourceStager
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
