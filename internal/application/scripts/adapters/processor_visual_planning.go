package adapters

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

// VisualCandidatePlanner is deliberately a closed-candidate seam. It may
// rerank candidates, but it cannot search providers or invent an asset ID.
type VisualCandidatePlanner interface {
	Select(context.Context, VisualSelectionRequest) (string, error)
}

type VisualSelectionRequest struct {
	SceneID    string
	SegmentID  string
	Text       string
	Slot       mediamemory.SlotKind
	Candidates []mediamemory.CandidateOption
}

// VisualPlanningProcessor resolves every open scene through one batched
// MediaMemory request. Locked assignments are applied locally and are never
// sent to the resolver or planner.
type VisualPlanningProcessor struct {
	resolver mediamemory.Resolver
	planner  VisualCandidatePlanner
	log      *zap.Logger
}

func NewVisualPlanningProcessor(resolver mediamemory.Resolver, planner VisualCandidatePlanner, log *zap.Logger) *VisualPlanningProcessor {
	if log == nil {
		log = zap.NewNop()
	}
	return &VisualPlanningProcessor{resolver: resolver, planner: planner, log: log}
}

func (p *VisualPlanningProcessor) Name() ProcessorName { return ProcessorVisualPlanning }
func (p *VisualPlanningProcessor) Policy(*scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *VisualPlanningProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil || plan.MediaPlan.Mode == mediadomain.MediaPlanModeDisabled || len(input.SpecScene.Scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	locked := make(map[string]mediamemory.SceneVisualPlan)
	for _, a := range plan.MediaPlan.Assignments {
		if !a.Locked {
			continue
		}
		key := a.SegmentID + "/" + a.Slot
		locked[key] = visualPlanFromAssignment(plan, a)
	}

	open := make([]mediamemory.SceneSpec, 0, len(input.SpecScene.Scenes))
	for i, scene := range input.SpecScene.Scenes {
		segmentID := scene.SegmentID
		if segmentID == "" && i < len(plan.Segments) {
			segmentID = plan.Segments[i].ID
		}
		if segmentID == "" {
			segmentID = scene.ID
		}
		if _, ok := locked[segmentID+"/primary_video"]; ok {
			continue
		}
		open = append(open, mediamemory.SceneSpec{ID: scene.ID, Text: scene.Text, Language: plan.Language, Slots: []mediamemory.SlotKind{mediamemory.SlotPrimaryVideo}})
	}

	plans := make([]mediamemory.SceneVisualPlan, 0, len(locked)+len(open))
	for _, scene := range input.SpecScene.Scenes {
		segmentID := scene.SegmentID
		if segmentID == "" {
			segmentID = scene.ID
		}
		if i := sceneIndex(input.SpecScene.Scenes, scene.ID); i >= 0 && segmentID == scene.ID && i < len(plan.Segments) {
			segmentID = plan.Segments[i].ID
		}
		if v, ok := locked[segmentID+"/primary_video"]; ok {
			v.SceneID, v.SegmentID, v.Text = scene.ID, segmentID, scene.Text
			plans = append(plans, v)
		}
	}
	if len(open) > 0 {
		if p.resolver == nil {
			return &PostProcessResult{Warnings: []string{"visual planning resolver unavailable"}}, nil
		}
		resolved, err := p.resolver.Resolve(ctx, mediamemory.ResolveRequest{ProjectID: plan.ID, Language: plan.Language, Scenes: open, Policy: mediamemory.ResolvePolicy{PreferApprovedBindings: true, AllowExternalSearch: plan.MediaPlan.Mode != mediadomain.MediaPlanModeCacheOnly, MaxCandidatesPerSlot: plannerLimit(plan)}})
		if err != nil {
			return &PostProcessResult{Warnings: []string{fmt.Sprintf("visual planning: %v", err)}}, nil
		}
		for _, v := range resolved.Plans {
			if p.planner != nil && len(v.Candidates) > 0 && len(v.Layers) > 0 {
				for i := range v.Layers {
					selected, err := p.planner.Select(ctx, VisualSelectionRequest{SceneID: v.SceneID, SegmentID: v.SegmentID, Text: v.Text, Slot: v.Layers[i].Slot, Candidates: v.Candidates})
					if err == nil && candidateExists(v.Candidates, selected) {
						v.Layers[i].AssetID = selected
					} else if err == nil && selected != "" {
						p.log.Warn("visual planner returned an unknown asset", zap.String("asset_id", selected))
					}
				}
			}
			plans = append(plans, v)
		}
		for _, w := range resolved.Warnings {
			plansWarning := w
			_ = plansWarning
		}
	}

	projected := cloneScenes(input.SpecScene.Scenes)
	projectVisualBindings(projected, plans, plan.MediaPlan.Assignments)
	return &PostProcessResult{VisualPlans: plans, SynthesizedScenes: projected, Changed: len(plans) > 0}, nil
}

func plannerLimit(plan *scriptpkg.ResolvedGenerationPlan) int {
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		return plan.MediaPlan.Planner.CandidateLimit
	}
	return 10
}
func candidateExists(c []mediamemory.CandidateOption, id string) bool {
	for _, x := range c {
		if x.AssetID == id {
			return true
		}
	}
	return false
}
func sceneIndex(s []scriptpkg.SpecScene, id string) int {
	for i := range s {
		if s[i].ID == id {
			return i
		}
	}
	return -1
}

func visualPlanFromAssignment(plan *scriptpkg.ResolvedGenerationPlan, a mediadomain.SegmentMediaAssignment) mediamemory.SceneVisualPlan {
	assetID := a.Asset.AssetID
	if assetID == "" {
		assetID = a.Asset.ClipID
	}
	provider := a.Asset.Provider
	return mediamemory.SceneVisualPlan{ProjectID: plan.ID, SegmentID: a.SegmentID, Source: provider, Layers: []mediamemory.Layer{{Slot: mediamemory.SlotKind(a.Slot), AssetID: assetID, Provider: provider, StartMs: a.Asset.StartMs, EndMs: a.Asset.EndMs}}}
}

func cloneScenes(in []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	out := append([]scriptpkg.SpecScene(nil), in...)
	for i := range out {
		out[i].Bindings = in[i].Bindings
	}
	return out
}

func projectVisualBindings(scenes []scriptpkg.SpecScene, plans []mediamemory.SceneVisualPlan, assignments []mediadomain.SegmentMediaAssignment) {
	for _, vp := range plans {
		for i := range scenes {
			if scenes[i].ID != vp.SceneID {
				continue
			}
			for _, layer := range vp.Layers {
				if layer.Slot != mediamemory.SlotPrimaryVideo || layer.AssetID == "" {
					continue
				}
				if scenes[i].Bindings.Stock == nil {
					scenes[i].Bindings.Stock = &scriptpkg.StockBinding{}
				}
				scenes[i].Bindings.Stock.AssetID = layer.AssetID
				scenes[i].Bindings.Stock.Source = layer.Provider
				scenes[i].Bindings.Stock.Score = layer.CandidateScore
			}
		}
	}
	for _, a := range assignments {
		if !a.Locked || a.Asset.ClipID == "" {
			continue
		}
		for i := range scenes {
			if scenes[i].SegmentID != a.SegmentID {
				continue
			}
			if scenes[i].Bindings.Clip == nil {
				scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{}
			}
			scenes[i].Bindings.Clip.ClipID = a.Asset.ClipID
			scenes[i].Bindings.Clip.StartMs = a.Asset.StartMs
			scenes[i].Bindings.Clip.EndMs = a.Asset.EndMs
			scenes[i].Bindings.Clip.DurationMs = a.Asset.EndMs - a.Asset.StartMs
		}
	}
}
