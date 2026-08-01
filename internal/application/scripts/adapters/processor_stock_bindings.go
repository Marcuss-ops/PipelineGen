package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// StockBindingsProcessor applies caller-supplied stock assets to the
// already clip-bound SpecScene. Direct bindings are deterministic and take
// precedence over any legacy/semantic stock value.
type StockBindingsProcessor struct{}

func NewStockBindingsProcessor() *StockBindingsProcessor { return &StockBindingsProcessor{} }
func (p *StockBindingsProcessor) Name() ProcessorName    { return ProcessorStockBindings }
func (p *StockBindingsProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorRequired
}

func (p *StockBindingsProcessor) Process(_ context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	// Explicit segment payloads define the canonical scene cardinality for
	// direct stock bindings. The LLM may legally return prose grouped into a
	// different number of scenes; accepting that shape would make a valid
	// binding for a later segment fail as "outside SpecScene". Normalize only
	// this explicit contract, leaving model-emitted scenes untouched for all
	// other jobs.
	if plan != nil && len(plan.Segments) > 0 && len(input.StockBindings) > 0 {
		input.SpecScene.Scenes = normalizeScenesForExplicitSegments(input.SpecScene.Scenes, plan.Segments)
	}
	stockEnabled := input.StockEnabled.AsBool() ||
		(input.StockEnabled != scriptpkg.ToggleDisabled && len(input.StockBindings) > 0)
	if !stockEnabled {
		for i := range input.SpecScene.Scenes {
			input.SpecScene.Scenes[i].Bindings.Stock = nil
		}
		return &PostProcessResult{Changed: true, UpdatedSpecScene: input.SpecScene}, nil
	}
	if len(input.StockBindings) == 0 {
		return nil, fmt.Errorf("%w: stock_bindings enabled but no direct stock bindings were supplied", scriptpkg.ErrPostprocessFailed)
	}
	seen := make(map[int]struct{}, len(input.StockBindings))
	for _, in := range input.StockBindings {
		if in.Index < 0 || in.Index >= len(input.SpecScene.Scenes) {
			return nil, fmt.Errorf("%w: stock binding index %d is outside SpecScene", scriptpkg.ErrPostprocessFailed, in.Index)
		}
		if _, ok := seen[in.Index]; ok {
			return nil, fmt.Errorf("%w: duplicate stock binding index %d", scriptpkg.ErrPostprocessFailed, in.Index)
		}
		seen[in.Index] = struct{}{}
		scene := &input.SpecScene.Scenes[in.Index]
		if strings.TrimSpace(in.SceneID) != "" && in.SceneID != scene.ID {
			return nil, fmt.Errorf("%w: stock binding index %d targets scene_id %q, want %q", scriptpkg.ErrPostprocessFailed, in.Index, in.SceneID, scene.ID)
		}
		if strings.TrimSpace(in.SegmentID) != "" && in.SegmentID != scene.SegmentID {
			return nil, fmt.Errorf("%w: stock binding index %d targets segment_id %q, want %q", scriptpkg.ErrPostprocessFailed, in.Index, in.SegmentID, scene.SegmentID)
		}
		if strings.TrimSpace(in.AssetID) == "" && strings.TrimSpace(in.DriveLink) == "" {
			return nil, fmt.Errorf("%w: stock binding index %d requires asset_id or drive_link", scriptpkg.ErrPostprocessFailed, in.Index)
		}
		if in.StartMs < 0 || in.EndMs <= in.StartMs {
			return nil, fmt.Errorf("%w: stock binding index %d requires end_ms > start_ms", scriptpkg.ErrPostprocessFailed, in.Index)
		}
		driveLink := strings.TrimSpace(in.DriveLink)
		assetID := strings.TrimSpace(in.AssetID)
		// Some existing direct YouTube stock records carry the Drive file
		// ID in both the asset and link fields. Normalize that transport
		// shape to a candidate URL; the Drive reconciliation processor still
		// verifies the file before publication.
		if in.Source == "youtube" && assetID != "" && (driveLink == "" || driveLink == assetID) {
			driveLink = "https://drive.google.com/file/d/" + assetID + "/view"
		}
		scene.Bindings.Stock = &scriptpkg.StockBinding{
			AssetID: assetID, Name: in.Name, Source: in.Source,
			DriveLink: driveLink, FolderID: strings.TrimSpace(in.FolderID), Score: in.Score, Fallback: in.Fallback,
			StartMs: in.StartMs, EndMs: in.EndMs, DurationMs: in.EndMs - in.StartMs,
		}
		scene.Kind = scriptpkg.SceneStock
	}
	return &PostProcessResult{Changed: true, UpdatedSpecScene: input.SpecScene}, nil
}

// normalizeScenesForExplicitSegments creates the exact scene slots declared
// by the caller. Existing generated scenes are retained by position; missing
// slots are grounded in the caller-provided segment source text/topic. This
// keeps direct stock bindings deterministic without inventing empty scenes.
func normalizeScenesForExplicitSegments(existing []scriptpkg.SpecScene, segments []scriptpkg.ScriptSegment) []scriptpkg.SpecScene {
	if len(segments) == 0 {
		return existing
	}
	scenes := make([]scriptpkg.SpecScene, len(segments))
	for i, segment := range segments {
		if i < len(existing) {
			scenes[i] = existing[i]
		}
		scenes[i].Index = i
		// Explicit segments define the canonical scene slots. Re-key the
		// retained model scenes by slot so stale/generated IDs cannot leave
		// gaps (for example scene-0, scene-1, scene-3 for index 2).
		scenes[i].ID = fmt.Sprintf("scene-%d", i)
		if strings.TrimSpace(segment.ID) != "" {
			scenes[i].SegmentID = segment.ID
		} else if strings.TrimSpace(scenes[i].SegmentID) == "" {
			scenes[i].SegmentID = fmt.Sprintf("segment-%d", i+1)
		}
		if strings.TrimSpace(scenes[i].Text) == "" {
			scenes[i].Text = strings.TrimSpace(segment.SourceText)
			if scenes[i].Text == "" {
				scenes[i].Text = strings.TrimSpace(segment.Topic)
			}
		}
		if !scenes[i].Kind.Valid() {
			scenes[i].Kind = scriptpkg.SceneClip
		}
	}
	return scenes
}
