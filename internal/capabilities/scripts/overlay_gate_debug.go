package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlaystatus "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlaystatus"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// dumpOverlayGateDebug writes the facts behind a strict entity-image failure:
// the audit rows, the semantic production plan, the raw scene bindings, the
// compiled items and the render bundle. It is diagnostic-only and runs only
// after the gate has already failed.
func dumpOverlayGateDebug(result *GenerateResult, planID string) {
	if result == nil {
		return
	}
	payload := struct {
		PlanID         string                                    `json:"plan_id"`
		EntityImages   *OverlayAuditEntityImages                 `json:"entity_images,omitempty"`
		ProductionPlan *SemanticProductionPlan                   `json:"semantic_production_plan,omitempty"`
		Scenes         []Scene                                   `json:"scenes,omitempty"`
		Items          []capmodel.OverlayItem                    `json:"overlay_items,omitempty"`
		Bundle         *capabilityoverlay.SemanticRenderBundleV1 `json:"bundle,omitempty"`
	}{
		PlanID:         planID,
		ProductionPlan: result.SemanticProductionPlan,
		Scenes:         result.Scenes,
		Bundle:         result.SemanticRenderBundle,
	}
	if result.OverlayAudit != nil {
		payload.EntityImages = result.OverlayAudit.EntityImages
	}
	if result.OverlayPlan != nil {
		payload.Items = result.OverlayPlan.Items
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		zap.L().Warn("overlay gate debug dump: marshal failed", zap.Error(err))
		return
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, planID)
	if name == "" {
		name = "unknown"
	}
	path := filepath.Join(os.TempDir(), "pipelinegen-overlay-gate-"+name+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		zap.L().Warn("overlay gate debug dump: write failed", zap.String("path", path), zap.Error(err))
		return
	}
	zap.L().Warn("overlay gate debug dump written", zap.String("path", path), zap.Int("bytes", len(data)))
}

// overlayStatusRequest copies the caller-visible overlay contract before
// planning, so a failed run still explains what was asked for.
func overlayStatusRequest(req GenerateRequest) *overlaystatus.Request {
	status := &overlaystatus.Request{
		EntityImages:     req.MediaPlan.Extraction.EntityImages.Enabled,
		EntityTypes:      append([]string(nil), req.MediaPlan.Extraction.EntityImages.EntityTypes...),
		EntityScope:      req.MediaPlan.Extraction.EntityImages.Scope,
		MaxImageOverlays: req.MaxImageOverlays,
		FinalJob:         req.FinalJob,
	}
	if req.OverlayBackground != nil {
		status.BackgroundKind = req.OverlayBackground.Kind
		status.BackgroundAssetID = req.OverlayBackground.AssetID
		status.BackgroundFit = req.OverlayBackground.Fit
	}
	return status
}

// overlayStatusBackground projects a resolved asset, or the failure that
// prevented resolution, without inventing a local path or hash.
func overlayStatusBackground(assetID string, asset *scriptpkg.OverlayBackgroundSpec, failure error) *overlaystatus.Background {
	if assetID == "" && asset == nil && failure == nil {
		return nil
	}
	status := &overlaystatus.Background{AssetID: assetID}
	if asset != nil {
		if asset.AssetID != "" {
			status.AssetID = asset.AssetID
		}
		status.LocalPath = asset.LocalPath
		status.URL = asset.URL
		status.SHA256 = asset.SHA256
		status.MediaType = asset.MediaType
		status.Resolved = failure == nil
	}
	if failure != nil {
		status.Error = failure.Error()
	}
	return status
}

// overlayStatusPlanSummary keeps the status document compact while preserving
// the plan shape and the two image lanes that decide whether admission worked.
func overlayStatusPlanSummary(plan *capmodel.OverlayPlan) *overlaystatus.PlanSummary {
	if plan == nil {
		return nil
	}
	summary := &overlaystatus.PlanSummary{
		PlanID:     plan.PlanID,
		PlanSHA256: plan.Fingerprint,
		Items:      len(plan.Items),
		ByKind:     make(map[string]int),
	}
	for _, item := range plan.Items {
		kind := strings.ToLower(strings.TrimSpace(item.Kind))
		if kind == "" {
			kind = strings.ToLower(strings.TrimSpace(item.TemplateID))
		}
		if kind == "" {
			kind = "unknown"
		}
		summary.ByKind[kind]++
		imageItem := len(item.AssetRefs) > 0 || len(item.ImageLayers) > 0 || strings.Contains(kind, "image") || kind == "logo" || kind == "product"
		if imageItem {
			summary.ImageItems++
		}
		if kind == "entity_image" || (strings.TrimSpace(item.EntityID) != "" && (len(item.AssetRefs) > 0 || len(item.ImageLayers) > 0)) {
			summary.EntityImageItems++
		}
	}
	return summary
}
