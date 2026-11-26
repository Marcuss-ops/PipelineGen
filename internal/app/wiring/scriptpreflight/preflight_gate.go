package scriptpreflight

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
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/script"
)

// scriptAssetPreflightGate projects operator.AssetInspection onto the facts the
// preflight needs.
type scriptAssetPreflightGate struct {
	reader operator.AssetInventoryReader
}

// NewScriptAssetPreflightGate returns nil when the read model is unavailable, so
// the caller can leave the port unwired (the route then reports
// unverified=true instead of pretending the catalog is clean).
func NewScriptAssetPreflightGate(reader operator.AssetInventoryReader) scriptapi.AssetPreflightReader {
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
		// The 360-degree inspection carries both the location projection
		// (has_local_file / has_drive_file, derived from the same CTE the
		// operator list uses) and the raw metadata (local_path / drive_link).
		// Reading both here is what lets the preflight separate "media
		// assente" from "drive link assente" instead of collapsing them.
		HasLocalFile: inspection.HasLocalFile,
		HasDriveFile: inspection.HasDriveFile,
		LocalPath:    metadataString(inspection.Metadata, "local_path"),
		DriveLink:    metadataString(inspection.Metadata, "drive_link"),
	}, true, nil
}

// metadataString reads one string entry from the raw metadata_json map. The
// projection is untyped by design (asset.SetMetadataString writes strings), so
// this is the only coercion the gate needs.
func metadataString(metadata map[string]any, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
