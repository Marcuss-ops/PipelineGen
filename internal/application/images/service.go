package images

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/destinations"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/retrieved"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

var (
	_ retrieved.SearchServicePort = (*ImageStorageService)(nil)
	_ retrieved.IngestServicePort = (*ImageStorageService)(nil)
	_ routing.Service             = (*retrieved.SearchServiceAdapter)(nil)
	_ routing.Service             = (*generated.GeneratedSearchServiceAdapter)(nil)
	_ catalog.CatalogSearch       = (*catalog.InMemoryCatalogSearch)(nil)
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

type DiagnosticsReport struct {
	OK                       bool                            `json:"ok"`
	Services                 []string                        `json:"services"`
	RepoConfigured           bool                            `json:"repo_configured"`
	DriveConfigured          bool                            `json:"drive_configured"`
	IngestConfigured         bool                            `json:"ingest_configured"`
	WikidataWorks            bool                            `json:"wikidata_works"`
	ImageGenWired            bool                            `json:"image_gen_wired"`
	ImageGenHealthy          bool                            `json:"image_gen_healthy"`
	ImageGenCooldownProfiles int                             `json:"image_gen_cooldown_profiles"`
	Capabilities             map[Capability]CapabilityStatus `json:"capabilities"`
}

type SemanticMetadataPayload = semantic.Payload

var ErrImageGenNotImplemented = fmt.Errorf("image generation via Google Slides is not configured")

type ImagesDeps struct {
	Core      ImagesCoreDeps
	Storage   ImagesStorageDeps
	GenAI     ImagesGenAIDeps
	External  ImagesExternalDeps
	Retrieval *retrieved.RetrievalProviderRegistry
	Generated *generated.GenerationProviderRegistry
}

type ImagesCoreDeps struct {
	Cfg *config.Config
	Log *zap.Logger
}

type ImagesStorageDeps struct {
	ImageRepo    *assets.ImagesRepository
	ClipsRepo    *assets.ClipsRepository
	DriveReader  drive.Reader
	MediaStore   *drive.Store
	Publisher    delivery.Publisher
	DestResolver destinations.DestinationResolver
}

type ImagesGenAIDeps struct {
	LLMGen        *ollama.Generator
	MetaWriter    *semantic.MetadataWriter
	StyleRegistry *generation.StyleRegistry
	ImageGen      ImageGenerator
}

type ImagesExternalDeps struct {
	IngestSvc    *ingest.Service
	Dispatcher   *outbox.Dispatcher
	VeloxBaseURL string
	GACfg        GoogleAccountingConfig
}

type Service struct {
	Gen        *GenerationService
	JobHandler *JobHandler
	Store      *ImageStorageService
	Meta       *MetadataService
	Diag       *DiagnosticsService
	Styles     *generation.StyleRegistry
}

func (s *Service) StylesRegistry() *generation.StyleRegistry {
	if s == nil {
		return nil
	}
	return s.Styles
}

func (s *Service) RetrievalRegistry() *retrieved.RetrievalProviderRegistry {
	if s == nil || s.Store == nil {
		return nil
	}
	return s.Store.retrievalRegistry
}

func NewService(deps ImagesDeps) *Service {
	cfg := deps.Core.Cfg
	log := deps.Core.Log

	diag := &DiagnosticsService{
		repo:        deps.Storage.ImageRepo,
		driveReader: deps.Storage.DriveReader,
		imageGen:    deps.GenAI.ImageGen,
		ingestSvc:   deps.External.IngestSvc,
		log:         log,
	}

	meta := &MetadataService{
		metaWriter: deps.GenAI.MetaWriter,
		mediaStore: deps.Storage.MediaStore,
		publisher:  deps.Storage.Publisher,
		tempDir:    cfg.Storage.TempPath(),
		log:        log,
	}

	store := &ImageStorageService{
		repo:          deps.Storage.ImageRepo,
		stockRepo:     deps.Storage.ClipsRepo,
		mediaStore:    deps.Storage.MediaStore,
		publisher:     deps.Storage.Publisher,
		driveReader:   deps.Storage.DriveReader,
		cfg:           cfg,
		imagesDir:     cfg.Storage.ImagesPath(),
		tempDir:       cfg.Storage.TempPath(),
		driveFolderID: cfg.Drive.RootFolder(),
		client:        &http.Client{Timeout: 10 * time.Minute},
		dispatcher:    deps.External.Dispatcher,
		log:           log,
		gaServerURL:   deps.External.GACfg.ServerURL,
		gaDownloadDir: deps.External.GACfg.DownloadDir,
		vidsProjectID: deps.External.GACfg.VidsProjectID,
		meta:          meta,
		destResolver:  deps.Storage.DestResolver,
		// PR C9 (July 2026): wire the typed SubjectTagsService port.
		// Concrete is leaf-only (no external deps), so inline construction
		// at composition root is safe + minimal-ripple.
		subjectTags: NewDefaultSubjectTagsService(),
	}

	retrievalRegistry := deps.Retrieval
	if retrievalRegistry == nil {
		retrievalRegistry = retrieved.NewDefaultProviderRegistry(store, store.client, log, "en", cfg.External.SearxngURL)
	}
	store.retrievalRegistry = retrievalRegistry

	generatedRegistry := deps.Generated
	if generatedRegistry == nil {
		generatedRegistry = generated.NewDefaultProviderRegistry(log, NewImageGeneratorAdapter(deps.GenAI.ImageGen))
	}

	gen := NewGenerationService(generatedRegistry, deps.GenAI.StyleRegistry, log, store)
	jobHandler := NewJobHandler(generatedRegistry, deps.GenAI.StyleRegistry, log)

	return &Service{Gen: gen, JobHandler: jobHandler, Store: store, Meta: meta, Diag: diag, Styles: deps.GenAI.StyleRegistry}
}

func (s *Service) GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts []string, tags []string, width, height int, model string, skipDrive bool) (*asset.ImageAsset, error) {
	if s == nil || s.Gen == nil {
		return nil, ErrImageGenNotImplemented
	}
	return s.Gen.GenerateSmartImage(ctx, subject, topic, style, prompts, tags, width, height, model, skipDrive)
}

func (s *Service) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	s.Gen.TriggerPrewarm(ctx, jobID, count)
}

// HandleJob delegates to the held JobHandler (constructed once at
// NewService time per PR-IMAGES-SHIM-REMOVAL, 2026-07-04). The
// pre-removal pattern of constructing a fresh NewJobHandler(...)
// per call is RETIRED — composition root owns the canonical wiring.
func (s *Service) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if s == nil || s.JobHandler == nil {
		return nil, fmt.Errorf("images.Service.HandleJob: JobHandler not wired (composition must call NewService): %w", appjobs.ErrMissingDeps)
	}
	return s.JobHandler.HandleJob(ctx, j, tools)
}

func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("images.Service.RegisterHandler: jobsSvc is nil: %w", appjobs.ErrMissingDeps)
	}
	if s.JobHandler == nil {
		return fmt.Errorf("images.Service.RegisterHandler: JobHandler not wired (composition must call NewService): %w", appjobs.ErrMissingDeps)
	}
	if err := s.JobHandler.RegisterHandler(jobsSvc); err != nil {
		return fmt.Errorf("images.Service.RegisterHandler: %w", err)
	}
	return nil
}

func (s *Service) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error) {
	return s.Store.SearchAndDownload(ctx, subjectSlug, displayName, query, lang, tags)
}

// ListImagesByOrigin returns all image media_assets rows with the
// specified origin, ordered by created_at DESC, hard-capped at
// 200 (per PR-GENERATED-SEARCH-FIX, July 2026). Thin delegate to
// the canonical repo surface at
// internal/infrastructure/database/sqlite/assets/images_repository.go.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this method is the
// canonical SOLE application-layer entry point for the generated
// territory read seam. The handler at
// internal/api/images/territory_handlers.go::GeneratedSearch routes
// through here; the port interface GeneratedSearchServicePort at
// internal/application/images/generated/generated_search.go is the
// structural contract (parent *ImageStorageService satisfies it
// transitively via s.Repo().ListImagesByOrigin). Future cross-cutting
// concerns (caching, metrics, additional filtering) can be added in
// one place — the handler does not bypass the service.
func (s *Service) ListImagesByOrigin(ctx context.Context, origin asset.ImageOrigin, limit int) ([]asset.ImageAsset, error) {
	if s == nil {
		return nil, nil
	}
	repo := s.Repo()
	if repo == nil {
		return nil, nil
	}
	return repo.ListImagesByOrigin(ctx, origin, limit)
}

func (s *Service) SearchWebImage(ctx context.Context, prompt, slug string, tags []string) (*asset.ImageAsset, error) {
	return s.Store.SearchWebImage(ctx, prompt, slug, tags)
}

func (s *Service) IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive, skipMetadata bool) (*asset.ImageAsset, error) {
	return s.Store.IngestImage(ctx, slug, style, genID, data, filename, sourceURL, description, tags, skipDrive, skipMetadata)
}

func (s *Service) UploadToStyleDrive(ctx context.Context, imageAsset *asset.ImageAsset, style string) (string, string, error) {
	return s.Store.UploadToStyleDrive(ctx, imageAsset, style)
}

func (s *Service) RegisterVideoAsset(ctx context.Context, filePath, description, source, style string, durationSec int, existingDriveFileID, existingDriveLink string) error {
	return s.Store.RegisterVideoAsset(ctx, filePath, description, source, style, durationSec, existingDriveFileID, existingDriveLink)
}

func (s *Service) SyncFromDrive(ctx context.Context) error { return s.Store.SyncFromDrive(ctx) }
func (s *Service) FormatDriveLink(id string) string        { return s.Store.FormatDriveLink(id) }

func (s *Service) UploadBatchMetadata(ctx context.Context, genID, slug, style, prompt, generator string, imageAssets []*asset.ImageAsset) {
	s.Meta.UploadBatchMetadata(ctx, genID, slug, style, prompt, generator, imageAssets)
}

func (s *Service) Diagnostics() DiagnosticsReport { return s.Diag.Diagnostics() }

func (s *Service) CapabilityResolution(cap Capability) CapabilityStatus {
	if s == nil || s.Diag == nil {
		return StatusNotImplemented
	}
	return s.Diag.CapabilityResolution(cap)
}

func (s *Service) AllCapabilities() map[Capability]CapabilityStatus {
	if s == nil || s.Diag == nil {
		return nil
	}
	return s.Diag.AllCapabilities()
}

func (s *Service) Log() *zap.Logger               { return s.Diag.Log() }
func (s *Service) Repo() *assets.ImagesRepository { return s.Diag.Repo() }
func (s *Service) SyncAssets() error              { return s.Diag.SyncAssets() }

// StopChromeProvider shuts down the persistent Chrome worker subprocess
// (slide_worker.py) if it is wired. Nil-safe and idempotent — safe to call
// even when the image generator is nil, not a ChromeImageProvider, or
// already stopped.
//
// VO-DECOMPOSITION P0 #1 CUTOVER follow-up (July 2026): mirrors the TTS
// worker Stop() pattern in DomainBundle.AudioProcessor.Stop(). Wired into
// shutdown.go::buildCleanup alongside the TTS worker.
func (s *Service) StopChromeProvider() error {
	if s == nil || s.Diag == nil || s.Diag.imageGen == nil {
		return nil
	}
	cp, ok := s.Diag.imageGen.(*ChromeImageProvider)
	if !ok || cp == nil {
		return nil
	}
	return cp.Stop()
}
