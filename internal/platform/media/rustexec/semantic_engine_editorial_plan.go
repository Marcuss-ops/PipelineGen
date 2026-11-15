package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

const editorialPlanLabel = "semantic-engine editorial plan"

type compileEditorialPlanRequest struct {
	Operation     string                                         `json:"operation"`
	FPSNum        int                                            `json:"fps_num"`
	FPSDen        int                                            `json:"fps_den"`
	SceneWindows  map[string][2]int64                            `json:"scene_windows"`
	Items         []resolveMapOverlapsItem                       `json:"items"`
	EntityScenes  []capabilityentities.EntityOverlayRankingScene `json:"entity_scenes"`
	EntranceItems []cascadeEntranceItem                          `json:"entrance_items"`
}

type compileEditorialPlanResponse struct {
	OK    bool                   `json:"ok"`
	Error string                 `json:"error,omitempty"`
	Plan  *compiledEditorialPlan `json:"plan,omitempty"`
}

type compiledEditorialPlan struct {
	PreBudgetEdits []resolveMapOverlapsEdit `json:"pre_budget_edits"`
	TimingEdits    []resolveMapOverlapsEdit `json:"timing_edits"`
	RankedEntities [][]int                  `json:"ranked_entities"`
	EntranceEdits  []cascadeEntranceEdit    `json:"entrance_edits"`
}

// EditorialPlanCompilation is the apply-edits projection of one
// compile_editorial_plan response. The caller keeps its full overlay DTOs;
// Rust returns only the decisions, never a second render representation.
type EditorialPlanCompilation struct {
	PreBudgetItems []capmodel.OverlayItem
	ResolvedItems  []capmodel.OverlayItem
	RankedEntities [][]int
	EntranceItems  []capmodel.OverlayItem
}

// CompileEditorialPlan sends the timing, entity-ranking and entrance inputs
// in one worker round-trip. It is the batch contract; the per-concern
// methods remain available while the Go planner has not yet collapsed the
// different compilation phases into this call.
func (a *SemanticEngineAdapter) CompileEditorialPlan(
	ctx context.Context,
	items []capmodel.OverlayItem,
	entranceItems []capmodel.OverlayItem,
	entityScenes []capabilityentities.EntityOverlayRankingScene,
	sceneWindows map[string][2]int64,
	fpsNum, fpsDen int,
) (*EditorialPlanCompilation, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("%s: executor is not configured", editorialPlanLabel)
	}
	request := compileEditorialPlanRequest{
		Operation:     "compile_editorial_plan",
		FPSNum:        fpsNum,
		FPSDen:        fpsDen,
		SceneWindows:  normalizeOverlaySceneWindows(sceneWindows),
		Items:         buildResolveMapOverlapsItems(items),
		EntityScenes:  entityScenes,
		EntranceItems: buildCascadeEntranceItems(entranceItems),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("%s encode: %w", editorialPlanLabel, err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("%s execute: %w", editorialPlanLabel, err)
	}
	var response compileEditorialPlanResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("%s decode: %w", editorialPlanLabel, err)
	}
	if !response.OK {
		return nil, fmt.Errorf("%s worker: %s", editorialPlanLabel, strings.TrimSpace(response.Error))
	}
	if response.Plan == nil {
		return nil, fmt.Errorf("%s returned no plan", editorialPlanLabel)
	}
	preBudgetItems, err := applyMapTimingEdits(items, response.Plan.PreBudgetEdits, editorialPlanLabel)
	if err != nil {
		return nil, err
	}
	resolvedItems, err := applyMapTimingEdits(items, response.Plan.TimingEdits, editorialPlanLabel)
	if err != nil {
		return nil, err
	}
	if err := validateEntitySelections(entityScenes, response.Plan.RankedEntities, editorialPlanLabel); err != nil {
		return nil, err
	}
	entranceItems, err = applyCascadeEntranceEdits(entranceItems, response.Plan.EntranceEdits, editorialPlanLabel)
	if err != nil {
		return nil, err
	}
	return &EditorialPlanCompilation{
		PreBudgetItems: preBudgetItems,
		ResolvedItems:  resolvedItems,
		RankedEntities: response.Plan.RankedEntities,
		EntranceItems:  entranceItems,
	}, nil
}

func buildResolveMapOverlapsItems(items []capmodel.OverlayItem) []resolveMapOverlapsItem {
	out := make([]resolveMapOverlapsItem, len(items))
	for i := range items {
		item := &items[i]
		out[i] = resolveMapOverlapsItem{
			ID: item.ID, SceneID: item.SceneID, Kind: item.Kind, TemplateID: item.TemplateID,
			StartMs: item.StartMs, EndMs: item.EndMs, StartUS: item.StartUS, DurationUS: item.DurationUS,
			HasMap: item.Map != nil, Params: item.Params,
		}
	}
	return out
}

func applyMapTimingEdits(items []capmodel.OverlayItem, edits []resolveMapOverlapsEdit, label string) ([]capmodel.OverlayItem, error) {
	if len(edits) != len(items) {
		return nil, fmt.Errorf("%s returned %d edits for %d items", label, len(edits), len(items))
	}
	out := append([]capmodel.OverlayItem(nil), items...)
	seen := make([]bool, len(items))
	kept := make([]capmodel.OverlayItem, 0, len(items))
	for _, edit := range edits {
		if edit.Index < 0 || edit.Index >= len(items) || seen[edit.Index] {
			return nil, fmt.Errorf("%s returned invalid or duplicate item index %d", label, edit.Index)
		}
		if edit.ID != items[edit.Index].ID {
			return nil, fmt.Errorf("%s changed item identity at index %d", label, edit.Index)
		}
		seen[edit.Index] = true
		if !edit.Keep {
			continue
		}
		original := &out[edit.Index]
		startUS, durationUS := overlayItemWindowUS(*original)
		if edit.StartUS != startUS || edit.DurationUS != durationUS {
			if edit.DurationUS <= 0 || edit.StartUS < 0 {
				return nil, fmt.Errorf("%s returned invalid window for item %q", label, original.ID)
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
			return nil, fmt.Errorf("%s omitted item index %d", label, index)
		}
	}
	return kept, nil
}

func buildCascadeEntranceItems(items []capmodel.OverlayItem) []cascadeEntranceItem {
	out := make([]cascadeEntranceItem, len(items))
	for i := range items {
		item := &items[i]
		var role any
		if item.Params != nil {
			role = item.Params["editorial_role"]
		}
		out[i] = cascadeEntranceItem{
			ID: item.ID, Kind: item.Kind, TemplateID: item.TemplateID,
			StartMs: item.StartMs, EndMs: item.EndMs, MotionParams: item.MotionParams,
			ImageLayerCount: len(item.ImageLayers), AssetRefCount: len(item.AssetRefs),
			EntityCaption: item.EntityCaption, Text: item.Text, EditorialRole: role,
		}
	}
	return out
}

func applyCascadeEntranceEdits(items []capmodel.OverlayItem, edits []cascadeEntranceEdit, label string) ([]capmodel.OverlayItem, error) {
	out := append([]capmodel.OverlayItem(nil), items...)
	seen := make(map[int]struct{}, len(edits))
	for _, edit := range edits {
		if edit.Index < 0 || edit.Index >= len(items) {
			return nil, fmt.Errorf("%s returned item index %d outside %d items", label, edit.Index, len(items))
		}
		if _, duplicate := seen[edit.Index]; duplicate {
			return nil, fmt.Errorf("%s repeated item index %d", label, edit.Index)
		}
		seen[edit.Index] = struct{}{}
		if edit.ID != items[edit.Index].ID {
			return nil, fmt.Errorf("%s changed item identity at index %d", label, edit.Index)
		}
		if edit.EnterFrames <= 0 || edit.EnterFrames > 240 {
			return nil, fmt.Errorf("%s returned invalid enter_frames %d for %q", label, edit.EnterFrames, edit.ID)
		}
		out[edit.Index].MotionParams = cloneCascadeMotionParams(items[edit.Index].MotionParams)
		if out[edit.Index].MotionParams == nil {
			return nil, fmt.Errorf("%s edited item %q without motion parameters", label, edit.ID)
		}
		out[edit.Index].MotionParams["enter_frames"] = edit.EnterFrames
	}
	return out, nil
}

func validateEntitySelections(scenes []capabilityentities.EntityOverlayRankingScene, selected [][]int, label string) error {
	if len(selected) != len(scenes) {
		return fmt.Errorf("%s returned %d scene selections for %d scenes", label, len(selected), len(scenes))
	}
	for sceneIndex, scene := range scenes {
		seenIndices := make(map[int]struct{}, len(selected[sceneIndex]))
		seenOverlayIDs := make(map[string]struct{}, len(selected[sceneIndex]))
		for _, index := range selected[sceneIndex] {
			if index < 0 || index >= len(scene.Entities) {
				return fmt.Errorf("%s returned index %d outside scene %d with %d entities", label, index, sceneIndex, len(scene.Entities))
			}
			if _, duplicate := seenIndices[index]; duplicate {
				return fmt.Errorf("%s repeated index %d in scene %d", label, index, sceneIndex)
			}
			seenIndices[index] = struct{}{}
			id := scene.Entities[index].OverlayID
			if _, duplicate := seenOverlayIDs[id]; duplicate {
				return fmt.Errorf("%s repeated overlay id %q in scene %d", label, id, sceneIndex)
			}
			seenOverlayIDs[id] = struct{}{}
		}
		expected := make(map[string]struct{}, len(scene.Entities))
		for _, entity := range scene.Entities {
			expected[entity.OverlayID] = struct{}{}
		}
		if len(seenOverlayIDs) != len(expected) {
			return fmt.Errorf("%s selected %d unique overlays for %d identities in scene %d", label, len(seenOverlayIDs), len(expected), sceneIndex)
		}
	}
	return nil
}

var _ interface {
	CompileEditorialPlan(context.Context, []capmodel.OverlayItem, []capmodel.OverlayItem, []capabilityentities.EntityOverlayRankingScene, map[string][2]int64, int, int) (*EditorialPlanCompilation, error)
} = (*SemanticEngineAdapter)(nil)
