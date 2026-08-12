package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
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
	if plan != nil && plan.MediaMode == scriptpkg.MediaModeStockOnly {
		for i := range input.SpecScene.Scenes {
			input.SpecScene.Scenes[i].Bindings.Clip = nil
		}
	}
	// Explicit segment payloads define the canonical scene cardinality for
	// direct stock bindings. The LLM may legally return prose grouped into a
	// different number of scenes; accepting that shape would make a valid
	// binding for a later segment fail as "outside SpecScene". Normalize only
	// this explicit contract, leaving model-emitted scenes untouched for all
	// other jobs.
	if plan != nil && len(plan.Segments) > 0 {
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
			if strings.TrimSpace(in.FolderLink) != "" {
				// A folder-only stock binding is valid without a file asset.
			} else {
				return nil, fmt.Errorf("%w: stock binding index %d requires asset_id or drive_link", scriptpkg.ErrPostprocessFailed, in.Index)
			}
		}
		folderID := strings.TrimSpace(in.FolderID)
		folderLink := strings.TrimSpace(in.FolderLink)
		if folderLink != "" && urlutil.FolderIDFromDriveLink(folderLink) != folderID {
			return nil, fmt.Errorf("%w: stock binding index %d folder_link does not match folder_id", scriptpkg.ErrPostprocessFailed, in.Index)
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
		if folderLink == "" && in.Source == "youtube" && assetID != "" && (driveLink == "" || driveLink == assetID) {
			driveLink = "https://drive.google.com/file/d/" + assetID + "/view"
		}
		scene.Bindings.Stock = &scriptpkg.StockBinding{
			AssetID: assetID, Name: in.Name, Source: in.Source,
			DriveLink: driveLink, FolderID: folderID, FolderLink: folderLink, Score: in.Score, Fallback: in.Fallback,
			StartMs: in.StartMs, EndMs: in.EndMs, DurationMs: in.EndMs - in.StartMs,
		}
		// The intro-hook segment keeps its narrative role (intro) even
		// when its visual comes from a direct stock binding; the other
		// scenes are anchored to stock as usual.
		if scene.SegmentID == scriptpkg.IntroHookSegmentID {
			scene.Kind = scriptpkg.SceneIntro
		} else {
			scene.Kind = scriptpkg.SceneStock
		}
	}
	// Explicit segment normalization may create scene slots that were not
	// present when the timeline processor first resolved its assignments.
	// Re-project the now-canonical post-segment clips so every boxer scene
	// exposes both bindings.stock and bindings.clip.
	projectPostSegmentClipBindings(input.SpecScene.Scenes, input.SpecScene.VisualAssignments)
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
		// Explicit source_text is the caller's segment boundary. If the
		// prose synthesizer merged a neighbouring topic, dropped the
		// subject, or produced an unusably short slot, restore that
		// segment's own source text instead of publishing cross-subject
		// narration under the wrong stock binding.
		if explicitSegmentTextNeedsRepair(scenes[i].Text, segment, segments) {
			scenes[i].Text = strings.TrimSpace(segment.SourceText)
		}
		if strings.TrimSpace(scenes[i].Text) == "" {
			scenes[i].Text = strings.TrimSpace(segment.SourceText)
			if scenes[i].Text == "" {
				scenes[i].Text = strings.TrimSpace(segment.Topic)
			}
		}
		if kind := scriptpkg.SceneKind(strings.TrimSpace(segment.Kind)); kind.Valid() {
			scenes[i].Kind = kind
		} else if !scenes[i].Kind.Valid() {
			scenes[i].Kind = scriptpkg.SceneClip
		}
	}
	return scenes
}

func explicitSegmentTextNeedsRepair(text string, segment scriptpkg.ScriptSegment, segments []scriptpkg.ScriptSegment) bool {
	if strings.TrimSpace(segment.SourceText) == "" {
		return strings.TrimSpace(text) == ""
	}
	body := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if len(strings.Fields(body)) < 100 {
		return true
	}
	primary := strings.ToLower(strings.TrimSpace(segment.Topic))
	if strings.Contains(segment.ID, "boxer-") && primary != "" && !strings.Contains(body, primary) {
		return true
	}
	for _, other := range segments {
		if other.ID == segment.ID || !strings.Contains(other.ID, "boxer-") {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(other.Topic))
		if name != "" && strings.Contains(body, name) {
			return true
		}
	}
	return false
}
