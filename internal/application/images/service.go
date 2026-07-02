package images

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

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

// ── Step 9 compile-time interface assertions ──
// These lock the parent-package types against the new Step 9
// subpackage ports. Drift between the structural interfaces
// and the concrete methods surfaces at build time rather than
// at the first runtime panic.
//
// Note: GeneratedSearchServicePort requires ListImagesByOrigin
// on *ImageStorageService, which doesn't exist yet (it's a
// Step-9 forward-pointer). Compilation assertions for that
// port live in generated/generated_search_test.go instead.
// Same pattern for CatalogSearch — read-only SQLite impl is a
// forward-pointer; assertions live in catalog tests.
var (
	_ retrieved.SearchServicePort = (*ImageStorageService)(nil)
	_ retrieved.IngestServicePort = (*ImageStorageService)(nil)
	_ routing.Service             = (*retrieved.SearchServiceAdapter)(nil)
	_ routing.Service             = (*generated.GeneratedSearchServiceAdapter)(nil)
	_ catalog.CatalogSearch       = (*catalog.InMemoryCatalogSearch)(nil)
)

// NOTE: GenerateImageRequest, GeneratedImage, and ImageGenerator
// are defined in the parent images/ports.go. External callers
// already access them as `imgservice.GenerateImageRequest` or
// `images.GenerateImageRequest` — no new alias is needed because
// the canonical types live here. Step 9 keeps them where they
// are; future steps may move them into generated/ once a
// ImageGeneratorBackend contract is fully owned by that subpkg.

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

// ── Public types ──────────────────────────────────────────────────────

// DiagnosticsReport is the health report for the images subsystem.
type DiagnosticsReport struct {
	OK                       bool                            `json:"ok"`
	Services                 []string                        `json:"services"`
	RepoConfigured           bool                            `json:"repo_configured"`
	DriveConfigured          bool                            `json:"drive_configured"`
	NvidiaConfigured         bool                            `json:"nvidia_configured"`
	IngestConfigured         bool                            `json:"ingest_configured"`
	WikidataWorks            bool                            `json:"wikidata_works"`
	ImageGenWired            bool                            `json:"image_gen_wired"`
	ImageGenHealthy          bool                            `json:"image_gen_healthy"`
	ImageGenCooldownProfiles int                             `json:"image_gen_cooldown_profiles"`
	Capabilities             map[Capability]CapabilityStatus `json:"capabilities"`
}

// SemanticMetadataPayload is the cross-package carrier for video metadata.
type SemanticMetadataPayload = semantic.Payload

// ErrImageGenNotImplemented is returned when the Google Slides generator is not wired.
var ErrImageGenNotImplemented = fmt.Errorf("image generation via Google Slides is not configured")

// ── Dependency bundles ────────────────────────────────────────────────

// ImagesDeps groups constructor dependencies by real capability.
type ImagesDeps struct {
	Core      ImagesCoreDeps
	Storage   ImagesStorageDeps
	GenAI     ImagesGenAIDeps
	External  ImagesExternalDeps
	Retrieval *retrieved.RetrievalProviderRegistry
	Generated *generated.GenerationProviderRegistry
}

// ImagesCoreDeps — config + logging + concurrency config.
type ImagesCoreDeps struct {
	Cfg *config.Config
	Log *zap.Logger
}

// ImagesStorageDeps — repositories + Drive client + media store.
type ImagesStorageDeps struct {
	ImageRepo   *assets.ImagesRepository
	ClipsRepo   *assets.ClipsRepository
	DriveReader drive.Reader
	MediaStore  *drive.Store

	// DestResolver is the canonical destinationKey -> Drive folder ID lookup.
	DestResolver destinations.DestinationResolver
}

// ImagesGenAIDeps — AI generation: LLM, metadata, style and the sole
// Google Slides image generator. NvidiaCfg is retained temporarily only for
// legacy diagnostics/config compatibility; it is not a generation provider.
type ImagesGenAIDeps struct {
	LLMGen         *ollama.Generator
	MetaWriter     *semantic.MetadataWriter
	StyleRegistry  *generation.StyleRegistry
	ImageGen       ImageGenerator
	NvidiaCfg      NvidiaConfig
	RemoteImageURL string
}

// ImagesExternalDeps — ingest, dispatcher, Velox, Google Accounting.
type ImagesExternalDeps struct {
	IngestSvc    *ingest.Service
	Dispatcher   *outbox.Dispatcher
	VeloxBaseURL string
	GACfg        GoogleAccountingConfig
}

// ── Facade ────────────────────────────────────────────────────────────

// Service is the thin facade over the four sub-services. All public API
// methods delegate to the appropriate sub-service.
type Service struct {
	Gen   *GenerationService
	Store *ImageStorageService
	Meta  *MetadataService
	Diag  *DiagnosticsService

	// Styles is the underlying canonical *generation.StyleRegistry.
	Styles *generation.StyleRegistry
}

// StylesRegistry returns the wrapped StyleRegistry handle. nil-safe.
func (s *Service) StylesRegistry() *generation.StyleRegistry {
	if s == nil {
		return nil
	}
	return s.Styles
}

// RetrievalRegistry exposes the canonical retrieval registry used only for
// finding existing web/archive images. It is separate from AI generation.
func (s *Service) RetrievalRegistry() *retrieved.RetrievalProviderRegistry {
	if s == nil || s.Store == nil {
		return nil
	}
	return s.Store.retrievalRegistry
}

// NewService constructs the four sub-services and returns the facade.
func NewService(deps ImagesDeps) *Service {
	cfg := deps.Core.Cfg
	log := deps.Core.Log

	maxNvidia := cfg.Concurrency.MaxConcurrentNvidiaGenerations
	if maxNvidia <= 0 {
		maxNvidia = 1
	}

	// 1. DiagnosticsService (no cross-deps)
	diag := &DiagnosticsService{
		repo:         deps.Storage.ImageRepo,
		driveReader:  deps.Storage.DriveReader,
		imageGen:     deps.GenAI.ImageGen,
		ingestSvc:    deps.External.IngestSvc,
		log:          log,
		nvidiaAPIKey: deps.GenAI.NvidiaCfg.APIKey,
	}

	// 2. MetadataService (no cross-deps)
	meta := &MetadataService{
		metaWriter: deps.GenAI.MetaWriter,
		mediaStore: deps.Storage.MediaStore,
		tempDir:    cfg.Storage.TempPath(),
		log:        log,
	}

	// 3. ImageStorageService (depends on MetadataService)
	store := &ImageStorageService{
		repo:          deps.Storage.ImageRepo,
		stockRepo:     deps.Storage.ClipsRepo,
		mediaStore:    deps.Storage.MediaStore,
		driveReader:   deps.Storage.DriveReader,
		cfg:           cfg,
		imagesDir:     cfg.Storage.ImagesPath(),
		tempDir:       cfg.Storage.TempPath(),
		driveFolderID: cfg.Drive.RootFolder(),
		client: &http.Client{
			Timeout: 10 * time.Minute,
		},
		dispatcher:    deps.External.Dispatcher,
		nvidiaSem:     make(chan struct{}, maxNvidia),
		log:           log,
		gaServerURL:   deps.External.GACfg.ServerURL,
		gaDownloadDir: deps.External.GACfg.DownloadDir,
		vidsProjectID: deps.External.GACfg.VidsProjectID,
		meta:          meta,
		destResolver:  deps.Storage.DestResolver,
	}

	// 4. Retrieval providers remain available for web/archive search; they
	// are not AI generation backends.
	retrievalRegistry := deps.Retrieval
	if retrievalRegistry == nil {
		retrievalRegistry = retrieved.NewDefaultProviderRegistry(
			store, store.client, log, "en", cfg.External.SearxngURL,
		)
	}
	store.retrievalRegistry = retrievalRegistry

	// AI generation has exactly one backend: Google Slides through the
	// Chrome/Playwright ImageGenerator adapter.
	generatedRegistry := deps.Generated
	if generatedRegistry == nil {
		generatedRegistry = generated.NewDefaultProviderRegistry(
			log, NewImageGeneratorAdapter(deps.GenAI.ImageGen),
		)
	}

	// 5. GenerationService.
	gen := &GenerationService{
		imageGen: deps.GenAI.ImageGen,
		styles:   deps.GenAI.StyleRegistry,
		log:      log,
		storage:  store,
		registry: generatedRegistry,
	}

	return &Service{
		Gen:    gen,
		Store:  store,
		Meta:   meta,
		Diag:   diag,
		Styles: deps.GenAI.StyleRegistry,
	}
}

// ── Delegate methods: Generation ──────────────────────────────────────

func (s *Service) GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts []string, tags []string, width, height int, model string, skipDrive bool) (*asset.ImageAsset, error) {
	if s == nil || s.Gen == nil {
		return nil, ErrImageGenNotImplemented
	}
	return s.Gen.GenerateSmartImage(ctx, subject, topic, style, prompts, tags, width, height, model, skipDrive)
}

func (s *Service) GenerateSmartImageWithAccount(ctx context.Context, subject, topic, style string, prompts []string, tags []string, width, height int, model string, skipDrive bool, account, projectID string) (*asset.ImageAsset, error) {
	if s == nil || s.Gen == nil {
		return nil, ErrImageGenNotImplemented
	}
	return s.Gen.GenerateSmartImageWithAccount(ctx, subject, topic, style, prompts, tags, width, height, model, skipDrive, account, projectID)
}

func (s *Service) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	s.Gen.TriggerPrewarm(ctx, jobID, count)
}

func (s *Service) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	return s.Gen.HandleJob(ctx, j, tools)
}

// RegisterHandler registers the image-generation job handler with the jobs system.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("images.Service.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := s.Gen.RegisterHandler(jobsSvc); err != nil {
		return fmt.Errorf("images.Service.RegisterHandler: bind via GenerationService: %w", err)
	}
	return nil
}

// ── Delegate methods: Storage ─────────────────────────────────────────

func (s *Service) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*asset.ImageAsset, error) {
	return s.Store.SearchAndDownload(ctx, subjectSlug, displayName, query, lang, tags)
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

func (s *Service) SyncFromDrive(ctx context.Context) error {
	return s.Store.SyncFromDrive(ctx)
}

func (s *Service) FormatDriveLink(id string) string {
	return s.Store.FormatDriveLink(id)
}

// ── Delegate methods: Metadata ────────────────────────────────────────

func (s *Service) UploadBatchMetadata(ctx context.Context, genID, slug, style, prompt, generator string, imageAssets []*asset.ImageAsset) {
	s.Meta.UploadBatchMetadata(ctx, genID, slug, style, prompt, generator, imageAssets)
}

// ── Delegate methods: Diagnostics ─────────────────────────────────────

func (s *Service) Diagnostics() DiagnosticsReport {
	return s.Diag.Diagnostics()
}

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

func (s *Service) Log() *zap.Logger {
	return s.Diag.Log()
}

func (s *Service) Repo() *assets.ImagesRepository {
	return s.Diag.Repo()
}

func (s *Service) SyncAssets() error {
	return s.Diag.SyncAssets()
}
