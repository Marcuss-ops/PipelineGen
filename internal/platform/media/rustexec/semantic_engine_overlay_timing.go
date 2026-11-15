package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

type resolveMapOverlapsRequest struct {
	Operation    string                   `json:"operation"`
	FPSNum       int                      `json:"fps_num"`
	FPSDen       int                      `json:"fps_den"`
	SceneWindows map[string][2]int64      `json:"scene_windows"`
	Items        []resolveMapOverlapsItem `json:"items"`
}

type resolveMapOverlapsItem struct {
	ID         string         `json:"id"`
	SceneID    string         `json:"scene_id"`
	Kind       string         `json:"kind"`
	TemplateID string         `json:"template_id"`
	StartMs    int64          `json:"start_ms"`
	EndMs      int64          `json:"end_ms"`
	StartUS    int64          `json:"start_us"`
	DurationUS int64          `json:"duration_us"`
	HasMap     bool           `json:"has_map"`
	Params     map[string]any `json:"params"`
}

type resolveMapOverlapsResponse struct {
	OK    bool                     `json:"ok"`
	Error string                   `json:"error,omitempty"`
	Edits []resolveMapOverlapsEdit `json:"edits"`
}

type resolveMapOverlapsEdit struct {
	Index      int    `json:"index"`
	ID         string `json:"id"`
	Keep       bool   `json:"keep"`
	StartUS    int64  `json:"start_us"`
	DurationUS int64  `json:"duration_us"`
	StartMs    int64  `json:"start_ms"`
	EndMs      int64  `json:"end_ms"`
}

// ResolveMapOverlaps delegates the final map collision pass to semantic-engine
// and applies only its timing edits, preserving all typed and opaque Go fields.
func (a *SemanticEngineAdapter) ResolveMapOverlaps(ctx context.Context, items []capmodel.OverlayItem, sceneWindows map[string][2]int64, fpsNum, fpsDen int) ([]capmodel.OverlayItem, error) {
	return a.mapTiming(ctx, "resolve_map_overlaps", items, sceneWindows, fpsNum, fpsDen)
}

// TrimMapWindowsBeforeBudget delegates the planner's early map duration pass
// to BrainRust while preserving all non-timing fields in PipelineGen.
func (a *SemanticEngineAdapter) TrimMapWindowsBeforeBudget(ctx context.Context, items []capmodel.OverlayItem, fpsNum, fpsDen int) ([]capmodel.OverlayItem, error) {
	return a.mapTiming(ctx, "trim_map_windows_before_budget", items, nil, fpsNum, fpsDen)
}

func (a *SemanticEngineAdapter) mapTiming(ctx context.Context, operation string, items []capmodel.OverlayItem, sceneWindows map[string][2]int64, fpsNum, fpsDen int) ([]capmodel.OverlayItem, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine overlay timing: executor is not configured")
	}
	if len(items) == 0 {
		return []capmodel.OverlayItem{}, nil
	}
	request := resolveMapOverlapsRequest{
		Operation: operation, FPSNum: fpsNum, FPSDen: fpsDen,
		SceneWindows: normalizeOverlaySceneWindows(sceneWindows), Items: make([]resolveMapOverlapsItem, len(items)),
	}
	for i := range items {
		item := &items[i]
		request.Items[i] = resolveMapOverlapsItem{
			ID: item.ID, SceneID: item.SceneID, Kind: item.Kind, TemplateID: item.TemplateID,
			StartMs: item.StartMs, EndMs: item.EndMs, StartUS: item.StartUS, DurationUS: item.DurationUS,
			HasMap: item.Map != nil, Params: item.Params,
		}
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine overlay timing encode: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine overlay timing execute: %w", err)
	}
	var response resolveMapOverlapsResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine overlay timing decode: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("semantic-engine overlay timing worker: %s", strings.TrimSpace(response.Error))
	}
	if len(response.Edits) != len(items) {
		return nil, fmt.Errorf("semantic-engine overlay timing returned %d edits for %d items", len(response.Edits), len(items))
	}
	out := append([]capmodel.OverlayItem(nil), items...)
	seen := make([]bool, len(items))
	kept := make([]capmodel.OverlayItem, 0, len(items))
	for _, edit := range response.Edits {
		if edit.Index < 0 || edit.Index >= len(items) || seen[edit.Index] {
			return nil, fmt.Errorf("semantic-engine overlay timing returned invalid or duplicate item index %d", edit.Index)
		}
		if edit.ID != items[edit.Index].ID {
			return nil, fmt.Errorf("semantic-engine overlay timing changed item identity at index %d", edit.Index)
		}
		seen[edit.Index] = true
		if !edit.Keep {
			continue
		}
		original := &out[edit.Index]
		startUS, durationUS := overlayItemWindowUS(*original)
		if edit.StartUS != startUS || edit.DurationUS != durationUS {
			if edit.DurationUS <= 0 || edit.StartUS < 0 {
				return nil, fmt.Errorf("semantic-engine overlay timing returned invalid window for item %q", original.ID)
			}
			original.StartUS = edit.StartUS
			original.DurationUS = edit.DurationUS
			original.StartMs = edit.StartMs
			original.EndMs = edit.EndMs
		}
		kept = append(kept, *original)
	}
	for index, found := range seen {
		if !found {
			return nil, fmt.Errorf("semantic-engine overlay timing omitted item index %d", index)
		}
	}
	return kept, nil
}

func overlayItemWindowUS(item capmodel.OverlayItem) (int64, int64) {
	if item.DurationUS > 0 {
		return item.StartUS, item.DurationUS
	}
	return item.StartMs * 1000, (item.EndMs - item.StartMs) * 1000
}

func normalizeOverlaySceneWindows(windows map[string][2]int64) map[string][2]int64 {
	normalized := make(map[string][2]int64, len(windows))
	for sceneID, window := range windows {
		normalized[strings.TrimSpace(sceneID)] = window
	}
	return normalized
}

var _ interface {
	ResolveMapOverlaps(context.Context, []capmodel.OverlayItem, map[string][2]int64, int, int) ([]capmodel.OverlayItem, error)
} = (*SemanticEngineAdapter)(nil)

var _ interface {
	TrimMapWindowsBeforeBudget(context.Context, []capmodel.OverlayItem, int, int) ([]capmodel.OverlayItem, error)
} = (*SemanticEngineAdapter)(nil)
