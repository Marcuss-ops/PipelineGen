package rustexec

import (
	"context"
	"encoding/json"
	"fmt"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

type cascadeEntrancesRequest struct {
	Operation string                `json:"operation"`
	FPSNum    int                   `json:"fps_num"`
	FPSDen    int                   `json:"fps_den"`
	Items     []cascadeEntranceItem `json:"items"`
}

type cascadeEntranceItem struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	TemplateID      string         `json:"template_id"`
	StartMs         int64          `json:"start_ms"`
	EndMs           int64          `json:"end_ms"`
	MotionParams    map[string]any `json:"motion_params,omitempty"`
	ImageLayerCount int            `json:"image_layer_count"`
	AssetRefCount   int            `json:"asset_ref_count"`
	EntityCaption   string         `json:"entity_caption"`
	Text            string         `json:"text"`
	EditorialRole   any            `json:"editorial_role"`
}

type cascadeEntrancesResponse struct {
	OK    bool                  `json:"ok"`
	Error string                `json:"error,omitempty"`
	Edits []cascadeEntranceEdit `json:"edits"`
}

type cascadeEntranceEdit struct {
	Index       int    `json:"index"`
	ID          string `json:"id"`
	EnterFrames int    `json:"enter_frames"`
}

// CascadeClusteredEntrances delegates the frame-based entrance cascade to
// BrainRust and applies sparse enter_frames edits to PipelineGen's full DTOs.
func (a *SemanticEngineAdapter) CascadeClusteredEntrances(ctx context.Context, items []capmodel.OverlayItem, fpsNum, fpsDen int) ([]capmodel.OverlayItem, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine entrance cascade: executor is not configured")
	}
	if len(items) == 0 {
		return []capmodel.OverlayItem{}, nil
	}
	request := cascadeEntrancesRequest{
		Operation: "cascade_clustered_entrances", FPSNum: fpsNum, FPSDen: fpsDen,
		Items: make([]cascadeEntranceItem, len(items)),
	}
	for i := range items {
		item := &items[i]
		var role any
		if item.Params != nil {
			role = item.Params["editorial_role"]
		}
		request.Items[i] = cascadeEntranceItem{
			ID: item.ID, Kind: item.Kind, TemplateID: item.TemplateID,
			StartMs: item.StartMs, EndMs: item.EndMs, MotionParams: item.MotionParams,
			ImageLayerCount: len(item.ImageLayers), AssetRefCount: len(item.AssetRefs),
			EntityCaption: item.EntityCaption, Text: item.Text, EditorialRole: role,
		}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine entrance cascade encode: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine entrance cascade execute: %w", err)
	}
	var response cascadeEntrancesResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine entrance cascade decode: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("semantic-engine entrance cascade worker: %s", response.Error)
	}
	out := append([]capmodel.OverlayItem(nil), items...)
	seen := make(map[int]struct{}, len(response.Edits))
	for _, edit := range response.Edits {
		if edit.Index < 0 || edit.Index >= len(items) {
			return nil, fmt.Errorf("semantic-engine entrance cascade returned item index %d outside %d items", edit.Index, len(items))
		}
		if _, duplicate := seen[edit.Index]; duplicate {
			return nil, fmt.Errorf("semantic-engine entrance cascade repeated item index %d", edit.Index)
		}
		seen[edit.Index] = struct{}{}
		if edit.ID != items[edit.Index].ID {
			return nil, fmt.Errorf("semantic-engine entrance cascade changed item identity at index %d", edit.Index)
		}
		if edit.EnterFrames <= 0 || edit.EnterFrames > 240 {
			return nil, fmt.Errorf("semantic-engine entrance cascade returned invalid enter_frames %d for %q", edit.EnterFrames, edit.ID)
		}
		out[edit.Index].MotionParams = cloneCascadeMotionParams(items[edit.Index].MotionParams)
		if out[edit.Index].MotionParams == nil {
			return nil, fmt.Errorf("semantic-engine entrance cascade edited item %q without motion parameters", edit.ID)
		}
		out[edit.Index].MotionParams["enter_frames"] = edit.EnterFrames
	}
	return out, nil
}

func cloneCascadeMotionParams(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

var _ interface {
	CascadeClusteredEntrances(context.Context, []capmodel.OverlayItem, int, int) ([]capmodel.OverlayItem, error)
} = (*SemanticEngineAdapter)(nil)
