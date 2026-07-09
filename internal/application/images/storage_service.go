package images

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
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
	publisher     delivery.Publisher
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

// publishToDrive is the P0-2 canonical bridge from the legacy
// AssetDestinationRequest shape to delivery.Publisher.Publish.
// The call routes through Publisher.Publish with proper ConflictPolicy
// and DestinationKey resolution. The legacy mediaStore.UploadToDrive
// fallback was RETIRED per P0-2 godlike/07 closure (July 2026) — nil
// publisher at construction time now fails-closed.
//
// Returns the (fileID, webViewLink) pair for backward compat with
// existing call sites that use the Store.UploadToDrive triple-return
// shape.
func (s *ImageStorageService) publishToDrive(ctx context.Context, req drive.AssetDestinationRequest, filePath string) (string, string, error) {
	if s == nil {
		return "", "", fmt.Errorf("ImageStorageService.publishToDrive: nil receiver")
	}
	if s.publisher == nil {
		return "", "", fmt.Errorf("ImageStorageService.publishToDrive: publisher not configured (P0-2 godlike/07: nil publisher fail-closed)")
	}
	result, err := s.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:        mapMediaTypeToDestination(req.MediaType),
		LocalPath:          filePath,
		Filename:           filepath.Base(filePath),
		Style:              req.Style,
		Subject:            req.Subject,
		Group:              req.Subject,
		ConflictPolicy:     mapMediaTypeToConflictPolicy(req),
		RootFolderOverride: req.DriveRootOverride,
	})
	if err != nil {
		return "", "", fmt.Errorf("publishToDrive: %w", err)
	}
	return result.FileID, result.WebViewLink, nil
}

// mapMediaTypeToDestination resolves a drive.MediaType to the canonical
// delivery.DestinationKey. P0-2 (July 2026).
func mapMediaTypeToDestination(mt drive.MediaType) delivery.DestinationKey {
	switch mt {
	case drive.MediaTypeSoundEffect:
		return delivery.DestinationSoundEffect
	default:
		return delivery.DestinationImage
	}
}

// mapMediaTypeToConflictPolicy chooses the canonical ConflictPolicy for
// the given AssetDestinationRequest. metadata JSON files are treated as
// regenerable (ConflictOverwrite); all other media (images, audio) use
// ConflictRename to ensure no file is silently lost on name collision
// (godlike/07 NO-FAKE-AVAILABILITY: ConflictSkip would silently drop
// images on duplicate filenames without operator visibility).
func mapMediaTypeToConflictPolicy(req drive.AssetDestinationRequest) delivery.ConflictPolicy {
	if req.Ext == ".json" || req.Hash == "metadata" {
		return delivery.ConflictOverwrite
	}
	return delivery.ConflictRename
}
