// Package overlayplan owns the overlay-plan family extracted from the scripts
// root: scene/entity annotations, the v2 plan request and envelope, the audit
// projections and the semantic render bundle. It depends on the contracts and
// leaves directly; it must never import the scripts root.
//
// This file is the package's explicit wiring surface. It names the contract
// types and the leaf helpers the family reads, so the moved production files
// keep their historical unqualified names and there is exactly one place to
// audit the package's external inputs.
package overlayplan

import (
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	finaljob "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/finaljob"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/geography"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/modelvalues"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlayproject"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/semantics"
)

// ImageOptions is the named form of the two image-planning switches shared by
// the compile entry point and its root callers.
type ImageOptions = overlayPlanImageOptions

// Contract types.
type (
	AudioReference                = contract.AudioReference
	EntityImageDecisionOwner      = contract.EntityImageDecisionOwner
	GenerateArtifacts             = contract.GenerateArtifacts
	GenerateResult                = contract.GenerateResult
	Language                      = contract.Language
	OverlayAudit                  = contract.OverlayAudit
	OverlayAuditEntity            = contract.OverlayAuditEntity
	OverlayAuditEntityConcordance = contract.OverlayAuditEntityConcordance
	OverlayAuditEntityConflict    = contract.OverlayAuditEntityConflict
	OverlayAuditEntityImages      = contract.OverlayAuditEntityImages
	OverlayAuditImage             = contract.OverlayAuditImage
	OverlayAuditImagePipeline     = contract.OverlayAuditImagePipeline
	OverlayAuditIntake            = contract.OverlayAuditIntake
	OverlayAuditItem              = contract.OverlayAuditItem
	OverlayAuditMissingImage      = contract.OverlayAuditMissingImage
	OverlayAuditOverlayTiming     = contract.OverlayAuditOverlayTiming
	OverlayAuditTimingItem        = contract.OverlayAuditTimingItem
	PhraseAligner                 = contract.PhraseAligner
	ResolvedScene                 = contract.ResolvedScene
	Scene                         = contract.Scene
	SemanticBindingIssue          = contract.SemanticBindingIssue
	SemanticProductionPlan        = contract.SemanticProductionPlan
	Source                        = contract.Source
)

// Contract constants and pure contract helpers.
const (
	ProductionSearchSatisfied = contract.ProductionSearchSatisfied
)

var normalizeIdentitySurface = contract.NormalizeIdentitySurface

// Pure model values shared with the scripts root.
const (
	SourceText     = modelvalues.SourceText
	SourceClips    = modelvalues.SourceClips
	SourceCatalog  = modelvalues.SourceCatalog
	SourceSearch   = modelvalues.SourceSearch
	SourceCurate   = modelvalues.SourceCurate
	SourceResearch = modelvalues.SourceResearch
)

var (
	CloneAnimationCounts = modelvalues.CloneAnimationCounts
	CloneFixedPlayback   = modelvalues.CloneFixedPlayback
	cloneAnimationCounts = modelvalues.CloneAnimationCounts
	cloneFixedPlayback   = modelvalues.CloneFixedPlayback
)

// Final-job leaf: the canvas value and the pure style/group projections.
type OverlayCanvasSpec = finaljob.OverlayCanvasSpec

var (
	applyOverlayImageStyle          = finaljob.ApplyOverlayImageStyle
	dumpOverlayGateDebug            = finaljob.DumpOverlayGateDebug
	effectiveImageStyle             = finaljob.EffectiveImageStyle
	isImageOverlayItem              = finaljob.IsImageOverlayItem
	overlayBackgroundFromPayload    = finaljob.OverlayBackgroundFromPayload
	overlayStatusPlanSummary        = finaljob.OverlayStatusPlanSummary
	validateOverlayEditorialDensity = finaljob.ValidateOverlayEditorialDensity
)

const (
	overlayGroupLongPhrases  = finaljob.OverlayGroupLongPhrases
	overlayGroupShortPhrases = finaljob.OverlayGroupShortPhrases
)

// Audio leaf: canonical timeline and scene resolution.
var (
	CompileCanonicalTimeline = scriptaudio.CompileCanonicalTimeline
	overlayResolvedScenesFor = scriptaudio.OverlayResolvedScenesFor
	resolvedScenesFor        = scriptaudio.ResolvedScenesFor
)

// Overlay projection, phrase and semantic leaves.
var (
	annotationsForLanguage            = overlayproject.AnnotationsForLanguage
	imageOverlayItemsBySceneWhere     = overlayproject.ImageOverlayItemsBySceneWhere
	completeGroundedPhrase            = phrases.CompleteGroundedPhrase
	phraseSelectionProfileForLanguage = phrases.ProfileForLanguage
	findEntitySpan                    = semantics.FindEntitySpan
	isPlaceEntityType                 = geography.IsPlaceEntityType
)

type (
	PhraseSelectionRequest = phrases.PhraseSelectionRequest
	PhraseSelector         = phrases.PhraseSelector
)

// EntityImageDecisionMediaPipeline is the media-pipeline owner constant the
// semantic binding must name. It lives here because the semantic binding is the
// only overlay-plan consumer; the scripts root aliases it for the public
// decision-owner surface.
const EntityImageDecisionMediaPipeline EntityImageDecisionOwner = "media_pipeline"
