// Package sources owns the legacy flat HTTP handlers for media sources that
// have NOT yet been migrated to api/assets/.
//
// PR4 (June 2026): voiceover, soundeffect, and register-from-youtube
// extracted to api/assets/{voiceover,soundeffect,register}/.
package sources

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	clipsources "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// SourcesHandler handles non-clip media operations and routes the clip
// surface via the embedded clipsources.Handler.
//
// PR4 (June 2026): Voiceover, SoundEffect, and RegisterFromYouTube moved
// to api/assets/. SyncDriveFolder and LocalToDrive remain as thin wrappers.
type SourcesHandler struct {
	cfg            *config.Config
	jobsSvc        *jobservice.Service
	catalogRepo    *catalog.Repository
	assetIndexSvc  *assetindex.Service
	artlistRepo    *assets.ClipsRepository
	clipsRepo      *assets.ClipsRepository
	stockRepo      *assets.ClipsRepository
	voiceoverRepo  *assets.VoiceoversRepository
	imagesRepo     *assets.ImagesRepository
	folderMemSvc   *foldermemory.Service
	assetTreeSvc   *assettree.Service
	driveUploader  *drive.Uploader
	mediaProcessor asset.Processor
	deletionSvc    *deletion.DeletionService
	catalogSync    *catalogsync.Service
	maintenanceSvc *maintenance.Service
	realtimeSvc    *realtime.Service
	clipIndexer    *clipindexer.Service
	vectorStore    *qdrant.Service
	metaWriter     *semantic.MetadataWriter
	artifactSvc    *artifacts.Service
	assetRepo      asset.Repository
	log            *zap.Logger

	providerRegistry *providers.Registry

	// clips owns ALL clip routes via clipsources.Handler.RegisterRoutes.
	clips *clipsources.Handler
}

// NewSourcesHandler creates a new common media handler.
//
// PR4 (June 2026): voiceoverSvc and voiceoverSync params removed —
// voiceover handler lives in api/assets/voiceover/ now.
func NewSourcesHandler(
	cfg *config.Config,
	jobsSvc *jobservice.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
	artlistRepo, clipsRepo, stockRepo *assets.ClipsRepository,
	assetRepo asset.Repository,
	voiceoverRepo *assets.VoiceoversRepository,
	imagesRepo *assets.ImagesRepository,
	folderMemSvc *foldermemory.Service,
	assetTreeSvc *assettree.Service,
	driveUploader *drive.Uploader,
	mediaProcessor asset.Processor,
	deletionSvc *deletion.DeletionService,
	catalogSync *catalogsync.Service,
	maintenanceSvc *maintenance.Service,
	providerRegistry *providers.Registry,
	realtimeSvc *realtime.Service,
	clipIndexer *clipindexer.Service,
	vectorStore *qdrant.Service,
	metaWriter *semantic.MetadataWriter,
	artifactSvc *artifacts.Service,
	log *zap.Logger,
) *SourcesHandler {
	h := &SourcesHandler{
		cfg:              cfg,
		jobsSvc:          jobsSvc,
		catalogRepo:      catalogRepo,
		assetIndexSvc:    assetIndexSvc,
		artlistRepo:      artlistRepo,
		clipsRepo:        clipsRepo,
		stockRepo:        stockRepo,
		voiceoverRepo:    voiceoverRepo,
		imagesRepo:       imagesRepo,
		folderMemSvc:     folderMemSvc,
		assetTreeSvc:     assetTreeSvc,
		driveUploader:    driveUploader,
		mediaProcessor:   mediaProcessor,
		deletionSvc:      deletionSvc,
		catalogSync:      catalogSync,
		maintenanceSvc:   maintenanceSvc,
		realtimeSvc:      realtimeSvc,
		clipIndexer:      clipIndexer,
		vectorStore:      vectorStore,
		metaWriter:       metaWriter,
		artifactSvc:      artifactSvc,
		assetRepo:        assetRepo,
		providerRegistry: providerRegistry,
		log:              log,
	}

	// PR-A Phase 4 BULK: build the unified clips.Handler with all constructor deps.
	h.clips = clipsources.NewHandler(clipsources.Deps{
		SourceResolver: artifacts.NewSourceResolver(h.artlistRepo, h.clipsRepo, h.stockRepo),
		AssetRepo:      h.assetRepo,
		ClipsRepo:      h.clipsRepo,
		StockRepo:      h.stockRepo,
		ArtlistRepo:    h.artlistRepo,
		DeletionSvc:    h.deletionSvc,
		DriveUploader:  h.driveUploader,
		MediaProcessor: h.mediaProcessor,
		AssetTreeSvc:   h.assetTreeSvc,
		MetaWriter:     h.metaWriter,
		ClipIndexer:    h.clipIndexer,
		VectorStore:    h.vectorStore,
		JobsSvc:        h.jobsSvc,
		Cfg:            h.cfg,
		Log:            h.log,
		VoiceoverRepo:  h.voiceoverRepo,
		ImagesRepo:     h.imagesRepo,
		ArtifactSvc:    h.artifactSvc,
		FolderMemSvc:   h.folderMemSvc,
	})

	if jobsSvc != nil {
		if err := h.RegisterJobHandlers(); err != nil {
			log.Warn("failed to register sources job handlers", zap.Error(err))
		}
	}

	return h
}

// RegisterJobHandlers delegates to h.clips.RegisterJobHandlers.
// Kept on SourcesHandler so composition sites (init_core.go /
// compose_media.go) keep their existing h.RegisterJobHandlers() call
// shape without churn.
func (h *SourcesHandler) RegisterJobHandlers() error {
	if h.clips == nil {
		return nil
	}
	return h.clips.RegisterJobHandlers()
}

// Clips exposes the embedded clips.Handler for callers (mainly tests /
// integration suites) that want to drive clip routes through the same
// receiver the router uses. Returns nil if NewSourcesHandler has not run.
func (h *SourcesHandler) Clips() *clipsources.Handler {
	return h.clips
}

// RegisterRoutes registers non-clip media routes.
//
// PR4 (June 2026): Voiceover, SoundEffect, and RegisterFromYouTube
// routes migrated to api/assets/. SyncDriveFolder and LocalToDrive
// remain as legacy wrappers.
func (h *SourcesHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering common media routes")

	// ── PR 3: search, recommend routes migrated to api/assets/search/ ──
	// ── PR 3: diagnostics, qdrant routes migrated to api/assets/diagnostics/ ──
	// ── PR 3: drive ops routes migrated to api/assets/storage/ ──
	// ── PR 4: voiceover, soundeffect, register-from-youtube migrated to api/assets/ ──

	// Sync any Drive folder recursively into DB + asset tree + Qdrant.
	r.POST("/sync-drive-folder", h.SyncDriveFolder)

	// Upload local .mp4 + metadata.json to Drive (async job).
	r.POST("/local-to-drive", h.LocalToDrive)
}
