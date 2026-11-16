package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
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
	plan, err := a.runCompileEditorialPlan(ctx, compileEditorialPlanRequest{
		Operation:     "compile_editorial_plan",
		FPSNum:        fpsNum,
		FPSDen:        fpsDen,
		SceneWindows:  normalizeOverlaySceneWindows(sceneWindows),
		Items:         buildResolveMapOverlapsItems(items),
		EntityScenes:  append([]capabilityentities.EntityOverlayRankingScene{}, entityScenes...),
		EntranceItems: buildCascadeEntranceItems(entranceItems),
	})
	if err != nil {
		return nil, err
	}
	preBudgetItems, err := applyMapTimingEdits(items, plan.PreBudgetEdits, editorialPlanLabel)
	if err != nil {
		return nil, err
	}
	resolvedItems, err := applyMapTimingEdits(items, plan.TimingEdits, editorialPlanLabel)
	if err != nil {
		return nil, err
	}
	if err := validateEntitySelections(entityScenes, plan.RankedEntities, editorialPlanLabel); err != nil {
		return nil, err
	}
	entranceItems, err = applyCascadeEntranceEdits(entranceItems, plan.EntranceEdits, editorialPlanLabel)
	if err != nil {
		return nil, err
	}
	return &EditorialPlanCompilation{
		PreBudgetItems: preBudgetItems,
		ResolvedItems:  resolvedItems,
		RankedEntities: plan.RankedEntities,
		EntranceItems:  entranceItems,
	}, nil
}

// compiledOverlayPlanV2 is the final-plan response. Items decode directly into
// the renderer DTO; the planner counts stay explicit because OverlayPlan keeps
// them application-only (json:"-").
type compiledOverlayPlanV2 struct {
	SchemaVersion   string                 `json:"schema_version"`
	PlanID          string                 `json:"plan_id"`
	VideoID         string                 `json:"video_id"`
	ProjectID       string                 `json:"project_id,omitempty"`
	Language        string                 `json:"language"`
	Width           int                    `json:"width"`
	Height          int                    `json:"height"`
	FPSNum          int                    `json:"fps_num"`
	FPSDen          int                    `json:"fps_den"`
	DurationMS      int64                  `json:"duration_ms"`
	Background      json.RawMessage        `json:"background,omitempty"`
	Items           []capmodel.OverlayItem `json:"items"`
	RecapPlacements int                    `json:"recap_placements"`
	RecapCards      int                    `json:"recap_cards"`
	ChartPlacements int                    `json:"chart_placements"`
	ChartCards      int                    `json:"chart_cards"`
	TimedScenes     int                    `json:"timed_scenes"`
	OutputProfileID string                 `json:"output_profile_id,omitempty"`
}

type compileOverlayPlanV2Response struct {
	OK            bool                   `json:"ok"`
	Error         string                 `json:"error,omitempty"`
	SchemaVersion string                 `json:"schema_version"`
	Plan          *compiledOverlayPlanV2 `json:"plan,omitempty"`
}

// CompileOverlayPlanV2 sends the complete editorial_plan.v2 envelope as the
// top-level request. The worker answers with the sealed final plan; the legacy
// sparse batch wrapper is not involved. The request is compacted to one NDJSON
// line and the response is decoded into the renderer's OverlayPlan.
func (a *SemanticEngineAdapter) CompileOverlayPlanV2(ctx context.Context, request json.RawMessage) (*capmodel.OverlayPlan, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("%s: executor is not configured", editorialPlanLabel)
	}
	var envelope struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(request, &envelope); err != nil {
		return nil, fmt.Errorf("%s decode request: %w", editorialPlanLabel, err)
	}
	if envelope.SchemaVersion != overlayplan.SchemaVersionV2 {
		return nil, fmt.Errorf("%s unsupported schema_version %q", editorialPlanLabel, envelope.SchemaVersion)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, request); err != nil {
		return nil, fmt.Errorf("%s compact request: %w", editorialPlanLabel, err)
	}
	stdout, _, err := a.executor.Run(ctx, compact.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%s execute: %w", editorialPlanLabel, err)
	}
	var response compileOverlayPlanV2Response
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("%s decode: %w", editorialPlanLabel, err)
	}
	if !response.OK {
		return nil, fmt.Errorf("%s worker: %s", editorialPlanLabel, strings.TrimSpace(response.Error))
	}
	if response.Plan == nil {
		return nil, fmt.Errorf("%s returned no final plan", editorialPlanLabel)
	}
	plan := response.Plan
	if response.SchemaVersion != overlayplan.SchemaVersionV2 || plan.SchemaVersion != overlayplan.SchemaVersionV2 {
		return nil, fmt.Errorf("%s returned schema %q/%q, want %q", editorialPlanLabel, response.SchemaVersion, plan.SchemaVersion, overlayplan.SchemaVersionV2)
	}
	out := &capmodel.OverlayPlan{
		SchemaVersion:   plan.SchemaVersion,
		PlanID:          plan.PlanID,
		VideoID:         plan.VideoID,
		ProjectID:       plan.ProjectID,
		Language:        plan.Language,
		Width:           plan.Width,
		Height:          plan.Height,
		FPSNum:          plan.FPSNum,
		FPSDen:          plan.FPSDen,
		DurationMS:      plan.DurationMS,
		Items:           plan.Items,
		RecapPlacements: plan.RecapPlacements,
		RecapCards:      plan.RecapCards,
		ChartPlacements: plan.ChartPlacements,
		ChartCards:      plan.ChartCards,
		TimedScenes:     plan.TimedScenes,
		OutputProfileID: plan.OutputProfileID,
	}
	if background := strings.TrimSpace(string(plan.Background)); background != "" && background != "null" {
		var decoded capmodel.OverlayBackground
		if err := json.Unmarshal(plan.Background, &decoded); err != nil {
			return nil, fmt.Errorf("%s decode background: %w", editorialPlanLabel, err)
		}
		out.Background = &decoded
	}
	return out, nil
}

func (a *SemanticEngineAdapter) runCompileEditorialPlan(ctx context.Context, request compileEditorialPlanRequest) (*compiledEditorialPlan, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("%s: executor is not configured", editorialPlanLabel)
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
	return response.Plan, nil
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

var _ interface {
	CompileOverlayPlanV2(context.Context, json.RawMessage) (*capmodel.OverlayPlan, error)
} = (*SemanticEngineAdapter)(nil)
