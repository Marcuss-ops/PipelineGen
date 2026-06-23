// Package assets provides the unified Assets HTTP module that aggregates
// all asset-related sub-handlers: storage, diagnostics, search, voiceover,
// soundeffect, register, media-ingest, and scraper. A single module
// registers all routes under the /api/media prefix.
package assets

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/register"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/voiceover"
)

// Dependencies holds the pre-built sub-handlers for the Assets module.
type Dependencies struct {
	// PR 2: thin transport handlers
	Storage     *storage.Handler
	Diagnostics *diagnostics.Handler
	Search      *search.Handler
	Clips       *clips.Handler

	// PR 4: extracted from SourcesHandler
	Voiceover   *voiceover.Handler
	SoundEffect *soundeffect.Handler
	Register    *register.Handler

	// Legacy handlers from the flat api/assets package:
	MediaIngest *MediaingestHandler
	Scraper     *ScraperHandler
}

// Module is the unified Assets HTTP module.
type Module struct {
	deps Dependencies
	log  *zap.Logger
}

// NewModule creates an AssetsModule from pre-built dependencies.
func NewModule(deps Dependencies, log *zap.Logger) *Module {
	return &Module{deps: deps, log: log}
}

// RegisterRoutes registers all asset routes under the given parent group.
func (m *Module) RegisterRoutes(r *gin.RouterGroup) {
	m.log.Info("Registering unified Assets module routes")

	// Storage operations (drive/move-files, create-folders, etc.)
	if m.deps.Storage != nil {
		m.deps.Storage.RegisterRoutes(r)
	}

	// Diagnostics operations (index-health, qdrant health)
	if m.deps.Diagnostics != nil {
		m.deps.Diagnostics.RegisterRoutes(r)
	}

	// Search operations (cross-provider search, semantic-search, recommend)
	if m.deps.Search != nil {
		m.deps.Search.RegisterRoutes(r)
	}

	// Clip operations (/clips/*)
	if m.deps.Clips != nil {
		m.deps.Clips.RegisterRoutes(r)
	}

	// Voiceover operations (/voiceover/*)
	if m.deps.Voiceover != nil {
		voiceover := r.Group("/voiceover")
		m.deps.Voiceover.RegisterRoutes(voiceover)
	}

	// SoundEffect operations (/sound_effect/*)
	if m.deps.SoundEffect != nil {
		sfx := r.Group("/sound_effect")
		m.deps.SoundEffect.RegisterRoutes(sfx)
	}

	// YouTube registration (register-from-youtube, register-batch)
	if m.deps.Register != nil {
		m.deps.Register.RegisterRoutes(r)
	}

	// Legacy media-ingest routes
	if m.deps.MediaIngest != nil {
		m.deps.MediaIngest.RegisterRoutes(r)
	}

	// Legacy scraper routes
	if m.deps.Scraper != nil {
		m.deps.Scraper.RegisterRoutes(r)
	}
}
