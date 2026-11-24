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
	PhraseTimingSource     = overlayplan.PhraseTimingSource
	timingProjectionSkip   = overlayplan.TimingProjectionSkip
)

const (
	EntityImageReasonBound                 = overlayplan.EntityImageReasonBound
	EntityImageReasonNoVerifiedAsset       = overlayplan.EntityImageReasonNoVerifiedAsset
	EntityImageReasonNotSelected           = overlayplan.EntityImageReasonNotSelected
	EntityImageReasonUploadFailed          = overlayplan.EntityImageReasonUploadFailed
	OverlayIntentReasonEntityNotPlanned    = overlayplan.OverlayIntentReasonEntityNotPlanned
	OverlayIntentReasonSourceTextNotInPlan = overlayplan.OverlayIntentReasonSourceTextNotInPlan
)

// Exported names that were already public in the root keep their surface.
var (
	ApplySemanticResolvedImages            = overlayplan.ApplySemanticResolvedImages
	BuildSemanticRenderBundleFromResult    = overlayplan.BuildSemanticRenderBundleFromResult
	CompileOverlayPlanFromGenerationResult = overlayplan.CompileOverlayPlanFromGenerationResult
	CompilePhraseTimings                   = overlayplan.CompilePhraseTimings
	CompileSceneSpeechTimings              = overlayplan.CompileSceneSpeechTimings
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
