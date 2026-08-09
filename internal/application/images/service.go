// Package images (application/images) — service.go holds the thin
// Service struct, constructor, and the two registry accessors
// (StylesRegistry + RetrievalRegistry). Per PR-IMG-SPLIT-4
// (July 2026), every method group lives in its own capability file.
//
// File layout:
//
//	deps.go                    — ImagesDeps + 4 sub-bags
//	service.go                 — Service struct + NewService (thin ctor)
//	service_generated.go       — GenerateSmartImage / TriggerPrewarm / HandleJob / RegisterHandler
//	service_retrieved.go       — SearchAndDownload / SearchWebImage
//	service_generated_read.go  — ListImagesByOrigin (generated territory read seam)
//	service_storage.go         — IngestImage / UploadToStyleDrive / SyncFromDrive etc.
//	service_diagnostics.go     — Diagnostics / CapabilityResolution / SyncAssets / StopChromeProvider
package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/generated"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/retrieved"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
)

// ── Compile-time satisfaction pins ──────────────────────────────────

var (
	_ retrieved.SearchServicePort = (*ImageStorageService)(nil)
	_ retrieved.IngestServicePort = (*ImageStorageService)(nil)
	_ routing.Service             = (*retrieved.SearchServiceAdapter)(nil)
	_ routing.Service             = (*generated.GeneratedSearchServiceAdapter)(nil)
	_ catalog.CatalogSearch       = (*catalog.InMemoryCatalogSearch)(nil)
)

// userAgent identifies the service to upstream Wikimedia and image providers.
// A stable, descriptive agent is required for provider rate-limit handling;
// browser impersonation makes upstream diagnostics and throttling worse.
const userAgent = "PipelineGen/1.0 (VidRush asset retrieval; contact admin)"

// SemanticMetadataPayload is retained as the application-owned payload
// name for callers that use images.SemanticMetadataPayload directly.
type SemanticMetadataPayload = SemanticPayload

// Service is the top-level facade for the images subsystem. It composes
// five sub-services: Gen (generation), JobHandler (async jobs), Store
// (storage/search), Meta (metadata), and Diag (diagnostics).
type Service struct {
	Gen        *GenerationService
	JobHandler *JobHandler
	Store      *ImageStorageService
	Meta       *MetadataService
	Diag       *DiagnosticsService
	Styles     *generation.StyleRegistry
}

// StylesRegistry returns the held generation.StyleRegistry, or nil.
func (s *Service) StylesRegistry() *generation.StyleRegistry {
	if s == nil {
		return nil
	}
	return s.Styles
}

// RetrievalRegistry returns the held retrieval registry (reached through
// the Store sub-service), or nil.
func (s *Service) RetrievalRegistry() *retrieved.RetrievalProviderRegistry {
	if s == nil || s.Store == nil {
		return nil
	}
	return s.Store.retrievalRegistry
}

// NewService is the canonical constructor. Wires the five sub-services
// (Gen, JobHandler, Store, Meta, Diag) from the ImagesDeps bag. Falls
// back to default registries when Generated/Retrieval are nil. The
// composition root supplies RemoteFetch; direct image retrieval calls
// fail closed through httpjson when it is absent.
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
		publisher:  deps.Storage.Publisher,
		tempDir:    cfg.Storage.TempPath(),
		log:        log,
	}

	store := &ImageStorageService{
		repo:          deps.Storage.ImageRepo,
		publisher:     deps.Storage.Publisher,
		driveReader:   deps.Storage.DriveReader,
		cfg:           cfg,
		imagesDir:     cfg.Storage.ImagesPath(),
		tempDir:       cfg.Storage.TempPath(),
		driveFolderID: cfg.Drive.RootFolder(),
		// RemoteFetch is injected from the composition root. Image retrieval
		// depends only on the application-owned transport port.
		client: deps.External.RemoteFetch,

		committer: deps.External.Committer,
		// PR-SOURCESTAGER-CONSOLIDATE (July 2026): SourceStager is
		// the canonical port for staging remote URLs into
		// deterministic local files. downloadAndIngest routes web
		// image downloads through it so the inline http boilerplate
		// no longer leaks into the processor. Nil is tolerated
		// (test fixture, partial deploy); downloadAndIngest fails
		// closed with a typed error when nil (godlike/07).
		sourceStager:  deps.External.SourceStager,
		log:           log,
		gaServerURL:   deps.External.GACfg.ServerURL,
		gaDownloadDir: deps.External.GACfg.DownloadDir,
		vidsProjectID: deps.External.GACfg.VidsProjectID,
		meta:          meta,
		destResolver:  deps.Storage.DestResolver,
		// PR C9 (July 2026): wire the typed SubjectTagsService port.
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
