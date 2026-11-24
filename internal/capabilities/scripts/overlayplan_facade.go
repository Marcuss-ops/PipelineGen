// Package scriptgeneration — overlayplan_facade.go keeps the historical root
// names for the overlay-plan family now owned by scripts/overlayplan. The
// implementations are aliases, not copies: the root imports overlayplan, never
// the other way around.
package scriptgeneration

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlayplan"
)

type (
	PhraseAlignmentRequest = overlayplan.PhraseAlignmentRequest
	timingProjectionSkip   = overlayplan.TimingProjectionSkip
)

// Exported names that were already public in the root keep their surface.
var (
	ApplySemanticResolvedImages            = overlayplan.ApplySemanticResolvedImages
	CompileOverlayPlanFromGenerationResult = overlayplan.CompileOverlayPlanFromGenerationResult
	DiagnosePhraseAnchoring                = overlayplan.DiagnosePhraseAnchoring
	ProjectSegmentAnnotations              = overlayplan.ProjectSegmentAnnotations
)

// Internal seams keep the historical lower-case root names so no root call
// site moves.
var (
	aggregateEntityResult               = overlayplan.AggregateEntityResult
	annotationCanonicalEntityID         = overlayplan.AnnotationCanonicalEntityID
	applyImageAssetStylesForResult      = overlayplan.ApplyImageAssetStylesForResult
	applySegmentEntityResults           = overlayplan.ApplySegmentEntityResults
	attachRandomImageEffectsToPlan      = overlayplan.AttachRandomImageEffectsToPlan
	compileResultEntityTimeline         = overlayplan.CompileResultEntityTimeline
	compileResultOverlayPlan            = overlayplan.CompileResultOverlayPlan
	compileResultPhraseTimings          = overlayplan.CompileResultPhraseTimings
	dropSurnameOnlyAnnotationDuplicates = overlayplan.DropSurnameOnlyAnnotationDuplicates
	effectiveVisualMoments              = overlayplan.EffectiveVisualMoments
	entityImageURL                      = overlayplan.EntityImageURL
	normalizeEntityAnnotationType       = overlayplan.NormalizeEntityAnnotationType
	occurrenceFor                       = overlayplan.OccurrenceFor
	projectEntityAnnotations            = overlayplan.ProjectEntityAnnotations
	setOverlayDriveJobID                = overlayplan.SetOverlayDriveJobID
	supplementSourceImportantPhrases    = overlayplan.SupplementSourceImportantPhrases
)
