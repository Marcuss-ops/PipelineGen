// Test-only facade: the overlay-plan tests live in the scripts root and still
// name the historical lower-case helpers. This file keeps those names pointing
// at the extracted package without widening the production facade.
package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlayplan"

const (
	maxGenericSceneImagesPerScene = overlayplan.MaxGenericSceneImagesPerScene
	requireEntityImagesEnv        = overlayplan.RequireEntityImagesEnv
)

var (
	applyChainSegmentPresets           = overlayplan.ApplyChainSegmentPresets
	applyImageAssetStyles              = overlayplan.ApplyImageAssetStyles
	applyOverlayAuditImageBudget       = overlayplan.ApplyOverlayAuditImageBudget
	applyPayloadOverlayStyleToItems    = overlayplan.ApplyPayloadOverlayStyleToItems
	applyRequestedEntityImageContract  = overlayplan.ApplyRequestedEntityImageContract
	applyRequestedMapAnimation         = overlayplan.ApplyRequestedMapAnimation
	applyRequestedMapPresentation      = overlayplan.ApplyRequestedMapPresentation
	applySegmentEntityAnnotations      = overlayplan.ApplySegmentEntityAnnotations
	buildOverlayAuditEntityConcordance = overlayplan.BuildOverlayAuditEntityConcordance
	buildOverlayAuditWithLineage       = overlayplan.BuildOverlayAuditWithLineage
	buildOverlayPlanV2Request          = overlayplan.BuildOverlayPlanV2Request
	bundleNameKey                      = overlayplan.BundleNameKey
	compileOverlayPlanV2               = overlayplan.CompileOverlayPlanV2
	compileOverlayPlanWithMotionOffset = overlayplan.CompileOverlayPlanWithMotionOffset
	enforceEntityImageGate             = overlayplan.EnforceEntityImageGate
	entityAssetLocalPaths              = overlayplan.EntityAssetLocalPaths
	entityAssetLookupIDs               = overlayplan.EntityAssetLookupIDs
	entityCardMediaIndex               = overlayplan.EntityCardMediaIndex
	entityImageBindingFor              = overlayplan.EntityImageBindingFor
	entityImageCardReservation         = overlayplan.EntityImageCardReservation
	entityImageOutcome                 = overlayplan.EntityImageOutcome
	entityResultHasValues              = overlayplan.EntityResultHasValues
	entitySourcesFromAnnotations       = overlayplan.EntitySourcesFromAnnotations
	expandMapPresetPresentation        = overlayplan.ExpandMapPresetPresentation
	expandPersonName                   = overlayplan.ExpandPersonName
	findLocalizedEntitySpan            = overlayplan.FindLocalizedEntitySpan
	freezeOverlayIntents               = overlayplan.FreezeOverlayIntents
	genericSceneImageLimit             = overlayplan.GenericSceneImageLimit
	hydrateLocalizedEntityImages       = overlayplan.HydrateLocalizedEntityImages
	materializeEditorialMapItems       = overlayplan.MaterializeEditorialMapItems
	overlayDisplayAlias                = overlayplan.OverlayDisplayAlias
	overlayPlanCanonicalEntityIDs      = overlayplan.OverlayPlanCanonicalEntityIDs
	overlayPlanCanvas                  = overlayplan.OverlayPlanCanvas
	overlayPlanClips                   = overlayplan.OverlayPlanClips
	overlayPlanEntityTimeline          = overlayplan.OverlayPlanEntityTimeline
	overlayPlanMedia                   = overlayplan.OverlayPlanMedia
	prepareOverlayPlanCompile          = overlayplan.PrepareOverlayPlanCompile
	projectEntityCompatibility         = overlayplan.ProjectEntityCompatibility
	projectSceneEntityResult           = overlayplan.ProjectSceneEntityResult
	resolvedEntityAssets               = overlayplan.ResolvedEntityAssets
	sceneImageCandidate                = overlayplan.SceneImageCandidate
	sceneImageCandidateReady           = overlayplan.SceneImageCandidateReady
	sceneImageCandidates               = overlayplan.SceneImageCandidates
	sealCompiledOverlayPlan            = overlayplan.SealCompiledOverlayPlan
	visualRecipesForScene              = overlayplan.VisualRecipesForScene
)
