// Package scriptgeneration — overlay_plan_freeze.go owns the seams around the
// sealed OverlayPlan.
//
// First seam, the render-boundary promotion of the pre-timing OverlayIntents:
// the exact join that binds each authoring intent to the plan item it
// materialized from, and the copy of that item's certified facts onto the
// intent.
//
// Second seam, the per-language plan boundary: ONE plan per language that has
// its own translated voiceover timing, compiled by the same SSOT compiler and
// never substituting the source plan for a missing translation.
//
// Both are seams around an already sealed plan rather than compilation steps, so
// they live apart from overlay_plan.go. The intent join is the identity-sensitive
// half of the lowering — the intent is
// authored before media resolution, so it is matched to the final item by
// IDENTITY, never by the display text two independent surfaces happen to carry
// — while the per-language boundary is the publication-sensitive half.
package overlayplan

import (
	"context"
	"fmt"
	"os"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	overlaystatus "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlaystatus"
)

// CompileResultOverlayPlan attaches the sealed source/localized plans and
// freezes their semantic intent lineage without mutating the prepare input.
func CompileResultOverlayPlan(ctx context.Context, result *contract.GenerateResult, language contract.Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec, plates capabilityoverlay.PlateResolver, compiler OverlayPlanCompiler, perSceneImages ...overlayPlanImageOptions) error {
	if result == nil {
		return nil
	}
	// Translated plans are lowered from localized annotations, while the
	// verified portrait is written on the source annotation by the media
	// stage. Copy that identity media BEFORE the bundle is built: the bundle
	// is the planner's only entity-media source, and a localized card without
	// it plans text-only even though the bytes were acquired and published.
	HydrateLocalizedEntityImages(result, language)
	// Build the verified asset bundle BEFORE lowering the plan. The bundle is
	// the planner's only entity-media source; building it afterwards left the
	// planner reading annotations that may already be cleared or localized.
	bundle, bundleErr := BuildSemanticRenderBundleFromResult(result, language, planID, planID, perSceneImages...)
	if bundleErr != nil {
		return fmt.Errorf("overlay plan: build semantic render bundle: %w", bundleErr)
	}
	if bundle != nil {
		// The hash describes the plan that is about to be compiled, not the
		// previous result's plan. Re-stamp it after sealing.
		bundle.OverlayPlanHash = ""
	}
	result.SemanticRenderBundle = bundle
	plan, err := compileOverlayPlanForLanguage(ctx, result, language, planID, projectID, driveFolderID, canvas, plates, compiler, perSceneImages...)
	if err != nil {
		return err
	}
	result.OverlayPlan = plan
	var phraseItems []capmodel.OverlayItem
	if plan != nil {
		phraseItems = plan.Items
		// Intake is planner-side telemetry carried beside the wire plan: it
		// explains the gap between the requested overlay mix and the items
		// that were actually admitted.
		result.OverlayPlanIntake = plan.Intake
	}
	phraseBudget, phraseBudgetErr := capabilityoverlay.ApplyPhraseBudgetPolicy(
		capabilityoverlay.MeasurePhraseOverlayBudgetWithLimit(phraseItems, canvas.MaxPhraseOverlays),
		phraseOverlayBudgetPolicy(),
	)
	result.PhraseOverlayBudget = &phraseBudget
	if phraseBudgetErr != nil {
		overlaystatus.Update(planID, func(status *overlaystatus.Status) {
			status.Stage = "phrase_budget_failed"
			status.Error = phraseBudgetErr.Error()
		})
		return phraseBudgetErr
	}
	if err := buildLocalizedOverlayPlans(ctx, result, language, planID, projectID, driveFolderID, canvas, plates, compiler, perSceneImages...); err != nil {
		return err
	}
	if plan == nil {
		return nil
	}
	if bundle != nil {
		bundle.OverlayPlanHash = plan.Fingerprint
		result.SemanticRenderBundle = bundle
	}
	// The lineage is computed by the freeze pass itself, then projected into
	// the audit: the audit must describe the plan that was lowered, not the
	// pre-freeze intent set.
	resolved := append([]capmodel.OverlayIntent(nil), result.OverlayIntents...)
	lineage := FreezeOverlayIntents(resolved, plan.Items)
	result.ResolvedOverlayIntents = resolved
	result.OverlayAudit = BuildOverlayAuditWithLineage(bundle, plan, lineage)
	var admittedIdentityImages map[string]struct{}
	if result.SemanticProductionPlan != nil && len(result.SemanticProductionPlan.Identity) > 0 {
		admittedIdentityImages = result.SemanticProductionPlan.IdentitiesAdmittedForMedia()
	}
	ApplyRequestedEntityImageContract(result.OverlayAudit, bundle, plan, canvas.RequireEntityImages, canvas.EntityImagesRequireDrive, canvas.RequiredEntityTypes, admittedIdentityImages)
	// The wire plan does not carry the requested budgets back, and the contract
	// above rebuilds the entity-image audit; keep the diagnostic tied to the
	// canvas ceiling instead of reporting "0" after the fact.
	ApplyOverlayAuditImageBudget(result.OverlayAudit, canvas.MaxImageOverlays)
	if result.OverlayAudit != nil && result.OverlayAudit.EntityImages != nil && canvas.StrictEntityImageRequirement {
		result.OverlayAudit.EntityImages.Required = true
	}
	overlaystatus.Update(planID, func(status *overlaystatus.Status) {
		status.PlanID = planID
		status.Stage = "plan_frozen"
		status.Audit = result.OverlayAudit
		status.Plan = overlayStatusPlanSummary(plan)
	})
	if err := EnforceEntityImageGate(result.OverlayAudit); err != nil {
		overlaystatus.Update(planID, func(status *overlaystatus.Status) {
			status.Stage = "entity_gate_failed"
			status.Error = err.Error()
		})
		dumpOverlayGateDebug(result, planID)
		return err
	}
	return nil
}

// phraseOverlayBudgetPolicy reads the operator policy. Strict fails closed in
// CompileResultOverlayPlan before any render work; warn (default) publishes the
// warning on the persisted budget.
func phraseOverlayBudgetPolicy() string {
	for _, key := range []string{"PIPELINEGEN_PHRASE_BUDGET_POLICY", "VELOX_PHRASE_BUDGET_POLICY"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return capabilityoverlay.PhraseBudgetPolicyWarn
}

// SetOverlayDriveJobID keeps public broker identity separate from semantic
// idempotent plan identity; only the broker job owns the Drive tree.
func SetOverlayDriveJobID(result *contract.GenerateResult, jobID string) {
	if result == nil {
		return
	}
	jobID = strings.TrimSpace(jobID)
	if result.OverlayPlan != nil {
		result.OverlayPlan.DriveJobID = jobID
		result.OverlayPlan.ResultJobID = jobID
	}
	for _, plan := range result.LocalizedOverlayPlans {
		if plan != nil {
			plan.DriveJobID = jobID
			plan.ResultJobID = jobID
		}
	}
}

// compileOverlayPlanForLanguage is the side-effect-free per-language plan
// boundary shared by the source-language compatibility plan and translated
// plans. Drive routing stays in the application-only plan field and never
// crosses the RenderingGen queue contract.
func compileOverlayPlanForLanguage(ctx context.Context, result *contract.GenerateResult, language contract.Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec, plates capabilityoverlay.PlateResolver, compiler OverlayPlanCompiler, perSceneImages ...overlayPlanImageOptions) (*capmodel.OverlayPlan, error) {
	plan, err := CompileOverlayPlanWithRustResolver(ctx, result, language, canvas, planID, planID, projectID, plates, compiler, perSceneImages...)
	if err != nil {
		return nil, err
	}
	if plan != nil {
		plan.DriveFolderID = strings.TrimSpace(driveFolderID)
	}
	return plan, nil
}

// buildLocalizedOverlayPlans compiles plans only for target languages that
// have translated voiceover timing. It never substitutes the source plan for
// a missing translation: a language without its own annotations/timing has no
// localized overlay artifact to publish.
func buildLocalizedOverlayPlans(ctx context.Context, result *contract.GenerateResult, sourceLanguage contract.Language, planID, projectID, driveFolderID string, canvas OverlayCanvasSpec, plates capabilityoverlay.PlateResolver, compiler OverlayPlanCompiler, perSceneImages ...overlayPlanImageOptions) error {
	if result == nil {
		return nil
	}
	result.LocalizedOverlayPlans = nil
	seen := map[contract.Language]struct{}{sourceLanguage: {}}
	for i := range result.Scenes {
		for language := range result.Scenes[i].Voiceover {
			if language == "" || language == sourceLanguage {
				continue
			}
			seen[language] = struct{}{}
		}
	}
	if len(seen) <= 1 {
		return nil
	}
	result.LocalizedOverlayPlans = make(map[contract.Language]*capmodel.OverlayPlan, len(seen)-1)
	for language := range seen {
		if language == sourceLanguage {
			continue
		}
		localizedPlanID := planID + "-" + strings.ToLower(string(language))
		plan, err := compileOverlayPlanForLanguage(ctx, result, language, localizedPlanID, projectID, driveFolderID, canvas, plates, compiler, perSceneImages...)
		if err != nil {
			return fmt.Errorf("compile %s overlay plan: %w", language, err)
		}
		if plan != nil {
			result.LocalizedOverlayPlans[language] = plan
		}
	}
	return nil
}
