// Package scriptgeneration — vidrush_aliases.go is the facade over
// scripts/vidrush, the package that owns the incremental VidRush pipeline.
//
// The VidRush pipeline, its ports and its value types moved out of the root so
// that the pipeline no longer imports the package it is composed into: the
// dependency now runs one way, root -> vidrush. These aliases keep every
// existing name in this package (and in the packages that consume
// scriptgen.VidRushPipeline and friends) resolving, with no call-site churn.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/vidrush"

type (
	LocalStockResolverPort    = vidrush.LocalStockResolverPort
	MediaCertBarrier          = vidrush.MediaCertBarrier
	MediaCertSpecResolver     = vidrush.MediaCertSpecResolver
	MediaCertSpecResolverFunc = vidrush.MediaCertSpecResolverFunc
	MediaCertifierFunc        = vidrush.MediaCertifierFunc
	MediaCertifierPort        = vidrush.MediaCertifierPort
	NERBackend                = vidrush.NERBackend
	SceneIRSegmentEnricher    = vidrush.SceneIRSegmentEnricher
	SegmentEnricher           = vidrush.SegmentEnricher
	SegmentMaterializer       = vidrush.SegmentMaterializer
	SegmentProviderResolver   = vidrush.SegmentProviderResolver
	SegmentResearcher         = vidrush.SegmentResearcher
	SemanticAndFanoutResolver = vidrush.SemanticAndFanoutResolver
	SemanticProviderResolver  = vidrush.SemanticProviderResolver
	VidRushBackpressure       = vidrush.VidRushBackpressure
	VidRushBarrier            = vidrush.VidRushBarrier
	VidRushMetrics            = vidrush.VidRushCoordinatorMetrics
	VidRushPipeline           = vidrush.VidRushPipeline
	VidRushPlanResolver       = vidrush.VidRushPlanResolver
	VidRushPlanResolverFunc   = vidrush.VidRushPlanResolverFunc
	VidRushRunTimings         = vidrush.VidRushRunTimings
	VidRushTimingRecorder     = vidrush.VidRushTimingRecorder
	VisualNERPort             = vidrush.VisualNERPort
)

// Functions re-exported from scripts/vidrush.
var (
	DefaultVidRushBackpressure   = vidrush.DefaultVidRushBackpressure
	NewMediaCertBarrier          = vidrush.NewMediaCertBarrier
	NewSceneIRSegmentEnricher    = vidrush.NewSceneIRSegmentEnricher
	NewSemanticAndFanoutResolver = vidrush.NewSemanticAndFanoutResolver
	NewSemanticProviderResolver  = vidrush.NewSemanticProviderResolver
)
