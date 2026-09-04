// Package images (application/images) — service.go holds the thin
// Service struct, constructor, and the two registry accessors
// (StylesRegistry + RetrievalRegistry). Per PR-IMG-SPLIT-4
// (July 2026), every method group lives in its own capability file.
package images

import (
	retrieved "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/search"
)

const userAgent = "PipelineGen/1.0 (VidRush asset retrieval; contact admin)"

type SemanticMetadataPayload = SemanticPayload

// Service is the top-level facade for the images subsystem.
type Service struct {
	Gen        *GenerationService
	JobHandler *JobHandler
	Store      *ImageStorageService
	Meta       *MetadataService
	Diag       *DiagnosticsService
	Styles     *StyleRegistry
}

func (s *Service) StylesRegistry() *StyleRegistry {
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

// NewService is the canonical constructor for the root facade. Generation and
// retrieval registries remain independently owned and are composed once here.
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
		client:        deps.External.RemoteFetch,
		committer:     deps.External.Committer,
		sourceStager:  deps.External.SourceStager,
		log:           log,
		gaServerURL:   deps.External.GACfg.ServerURL,
		gaDownloadDir: deps.External.GACfg.DownloadDir,
		vidsProjectID: deps.External.GACfg.VidsProjectID,
		meta:          meta,
		destResolver:  deps.Storage.DestResolver,
		subjectTags:   NewDefaultSubjectTagsService(),
	}

	retrievalRegistry := deps.Retrieval
	if retrievalRegistry == nil {
		retrievalRegistry = retrieved.NewDefaultProviderRegistry(store, store.client, log, "en", cfg.External.SearxngURL)
	}
	store.retrievalRegistry = retrievalRegistry

	generatedRegistry := deps.Generated
	if generatedRegistry == nil {
		generatedRegistry = NewDefaultProviderRegistry(log, deps.GenAI.ImageGen)
	}

	gen := NewGenerationService(generatedRegistry, deps.GenAI.StyleRegistry, log, store)
	jobHandler := NewJobHandler(generatedRegistry, deps.GenAI.StyleRegistry, log)

	return &Service{Gen: gen, JobHandler: jobHandler, Store: store, Meta: meta, Diag: diag, Styles: deps.GenAI.StyleRegistry}
}
