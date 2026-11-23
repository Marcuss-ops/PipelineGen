package wiring

// script_preflight_gate.go adapts the operator inventory read model (the same
// projection GET /api/assets/operator/assets/:id serves) to the narrow port the
// script capability's POST /api/script/preflight consumes.
//
// Why an adapter instead of passing the reader through: the script capability
// must not import another capability's DTO (assets/operator). The adapter owns
// the one translation, so the preflight and the operator console can never read
// different facts about the same asset — both go through AssetInventoryReader.

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/script"
)

// scriptAssetPreflightGate projects operator.AssetInspection onto the facts the
// preflight needs.
type scriptAssetPreflightGate struct {
	reader operator.AssetInventoryReader
}

// newScriptAssetPreflightGate returns nil when the read model is unavailable, so
// the caller can leave the port unwired (the route then reports
// unverified=true instead of pretending the catalog is clean).
func newScriptAssetPreflightGate(reader operator.AssetInventoryReader) scriptapi.AssetPreflightReader {
	if reader == nil {
		return nil
	}
	return scriptAssetPreflightGate{reader: reader}
}

func (g scriptAssetPreflightGate) PreflightAssetFacts(ctx context.Context, clipID string) (scriptapi.AssetPreflightFact, bool, error) {
	inspection, err := g.reader.Get(ctx, clipID)
	if err != nil {
		return scriptapi.AssetPreflightFact{}, false, err
	}
	if inspection == nil {
		return scriptapi.AssetPreflightFact{}, false, nil
	}
	return scriptapi.AssetPreflightFact{
		ClipID:         inspection.ID,
		LifecycleState: string(inspection.LifecycleState),
		AssetState:     string(inspection.AssetState),
		DurationMS:     inspection.DurationMS,
		TextTracks:     len(inspection.TextTracks),
	}, true, nil
}
