package scriptgeneration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
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
