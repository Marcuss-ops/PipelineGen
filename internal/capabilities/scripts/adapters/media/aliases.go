// Package media is the compatibility facade over the image-resolver surface
// that now lives with the VidRush visual family. The implementation is in
// scripts/vidrush; this package keeps the resolver import path available while
// the remaining callers migrate.
package media

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/vidrush"

type (
	MediaResolverImageStage    = vidrush.MediaResolverImageStage
	MediaResolver              = vidrush.MediaResolver
	MediaManifestPins          = vidrush.MediaManifestPins
	SemanticCandidateScorer    = vidrush.SemanticCandidateScorer
	EntityImageContentStore    = vidrush.EntityImageContentStore
	EntityImageCatalogDecision = vidrush.EntityImageCatalogDecision
)

const (
	MediaManifestProvider   = vidrush.MediaManifestProvider
	MediaManifestCacheState = vidrush.MediaManifestCacheState
)

var (
	NewMediaResolverImageStageWithCatalog = vidrush.NewMediaResolverImageStageWithCatalog
	LoadMediaManifestPins                 = vidrush.LoadMediaManifestPins
	LoadMediaManifestPinsFromEnv          = vidrush.LoadMediaManifestPinsFromEnv
	ProjectEntityImageBindings            = vidrush.ProjectEntityImageBindings
)
