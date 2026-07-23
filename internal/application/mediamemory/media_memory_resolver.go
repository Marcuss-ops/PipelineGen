// Package mediamemory — media_memory_resolver.go is the canonical
// resolver that delegates every federated search to the Brain. It is
// the single surface that routes visual-memory resolution through the
// brain capability, so no package outside the brain performs its own
// search fan-out.
//
// godlike/06 SSOT: this resolver does NOT call Qdrant, SQLite, Drive,
// FFmpeg or yt-dlp directly. All search decisions (exact memory,
// local catalog, semantic, external providers) are owned by the Brain,
// which uses the CandidateSearcher port backed by the canonical
// search.SearchFanOut.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil brain dependency panics at
// construction time (fail-fast). Per-scene errors are surfaced as
// typed warnings and processing continues with the remaining scenes.
package mediamemory

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// MediaMemoryResolver is the canonical production implementation of
// Resolver. It wraps a brain.Brain and projects the brain's canonical
// types into the mediamemory capability's types.
type MediaMemoryResolver struct {
	brain brain.Brain
}

// NewMediaMemoryResolver constructs a resolver that delegates every
// search to the provided Brain. Passing nil panics at construction
// time so a mis-wired composition root is caught at boot.
func NewMediaMemoryResolver(b brain.Brain) *MediaMemoryResolver {
	if b == nil {
		panic("mediamemory: brain is required for MediaMemoryResolver")
	}
	return &MediaMemoryResolver{brain: b}
}

// Compile-time assertion: MediaMemoryResolver satisfies Resolver.
var _ Resolver = (*MediaMemoryResolver)(nil)

// Resolve converts the mediamemory request into the canonical
// brain.BrainRequest, delegates to the brain, and projects the result
// back into mediamemory.ResolveResult. Every search is performed by
// the brain through its CandidateSearcher port; no additional search
// path exists in this resolver.
func (r *MediaMemoryResolver) Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error) {
	brainReq := toBrainRequest(req)
	brainResult, err := r.brain.Resolve(ctx, brainReq)
	if err != nil {
		return ResolveResult{}, err
	}

	result := ResolveResult{
		ProjectID: req.ProjectID,
		Plans:     make([]SceneVisualPlan, 0, len(brainResult.Scenes)),
		Warnings:  make([]string, 0),
	}

	for _, scene := range brainResult.Scenes {
		plan := toMediaMemorySceneVisualPlan(req, scene)
		if len(plan.Layers) > 0 {
			result.Plans = append(result.Plans, plan)
		}
		if scene.Status == "error" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("scene_id=%q: brain resolution returned error status", scene.SceneID))
		}

		// Surface backend calls and reasons from the per-scene
		// brain trace as diagnostic warnings. They are not failures
		// — the brain has already made its selection — but they
		// help operators understand which backends contributed.
		if scene.Trace.Reasons != nil {
			for _, reason := range scene.Trace.Reasons {
				result.Warnings = append(result.Warnings, fmt.Sprintf("scene_id=%q: brain: %s", scene.SceneID, reason))
			}
		}
		for _, call := range scene.Trace.BackendCalls {
			if call.Error != "" {
				result.Warnings = append(result.Warnings, fmt.Sprintf("scene_id=%q: brain backend=%q error=%s", scene.SceneID, call.Backend, call.Error))
			} else {
				result.Warnings = append(result.Warnings, fmt.Sprintf("scene_id=%q: brain backend=%q hits=%d", scene.SceneID, call.Backend, call.Hits))
			}
		}
	}

	return result, nil
}

func toBrainRequest(req ResolveRequest) brain.BrainRequest {
	scenes := make([]brain.SceneRequest, 0, len(req.Scenes))
	for _, s := range req.Scenes {
		scenes = append(scenes, brain.SceneRequest{
			ID:         s.ID,
			Text:       s.Text,
			DurationMS: s.DurationMs,
			Slots:      toBrainSlotKinds(s.Slots),
		})
	}

	return brain.BrainRequest{
		ProjectID: req.ProjectID,
		Language:  req.Language,
		Scenes:    scenes,
		Policy: brain.ResolutionPolicy{
			PreferApprovedBindings: req.Policy.PreferApprovedBindings,
			AllowExternalSearch:    req.Policy.AllowExternalSearch,
			MaxCandidatesPerSlot:   req.Policy.MaxCandidatesPerSlot,
			AvoidRecentAssets:      req.Policy.AvoidRecentAssets,
		},
	}
}

func toBrainSlotKinds(in []SlotKind) []media.SlotKind {
	out := make([]media.SlotKind, 0, len(in))
	for _, s := range in {
		out = append(out, media.SlotKind(s))
	}
	return out
}

func toMediaMemorySceneVisualPlan(req ResolveRequest, scene brain.SceneVisualPlan) SceneVisualPlan {
	// Look up the original scene to preserve text / language / duration.
	var text, language string
	var durationMs int64
	for _, s := range req.Scenes {
		if s.ID == scene.SceneID {
			text = s.Text
			language = s.Language
			durationMs = s.DurationMs
			break
		}
	}
	if language == "" {
		language = req.Language
	}

	plan := SceneVisualPlan{
		ProjectID:  req.ProjectID,
		SceneID:    scene.SceneID,
		Text:       text,
		Language:   language,
		DurationMs: durationMs,
		Layers:     make([]Layer, 0, len(scene.Layers)),
	}

	sourceSet := make(map[string]struct{})
	for _, l := range scene.Layers {
		plan.Layers = append(plan.Layers, Layer{
			Slot:           SlotKind(l.Slot),
			AssetID:        l.AssetID,
			BindingID:      l.BindingID,
			StartMs:        l.StartMs,
			EndMs:          l.EndMs,
			Provider:       l.Provider,
			CandidateScore: l.Score,
		})
		if l.Provider != "" {
			sourceSet[l.Provider] = struct{}{}
		}
	}

	plan.Source = deriveSource(sourceSet)

	// Surface the brain's understanding and decision trace for
	// diagnostics. These fields are optional for backward
	// compatibility; zero values are valid for legacy resolvers.
	plan.Intent = SceneIntent{
		Entities: scene.Intent.Entities,
		Concepts: scene.Intent.Concepts,
		Actions:  scene.Intent.Actions,
		Keywords: scene.Intent.Keywords,
	}
	backendCalls := make([]SceneBackendCall, 0, len(scene.Trace.BackendCalls))
	for _, call := range scene.Trace.BackendCalls {
		backendCalls = append(backendCalls, SceneBackendCall{
			Backend: call.Backend,
			Hits:    call.Hits,
			Error:   call.Error,
		})
	}
	plan.Trace = SceneResolutionTrace{
		NormalizedText: scene.Trace.NormalizedText,
		BackendCalls:   backendCalls,
		Reasons:        scene.Trace.Reasons,
	}
	plan.DecisionFingerprint = scene.DecisionFingerprint

	return plan
}

func deriveSource(sourceSet map[string]struct{}) string {
	if len(sourceSet) == 0 {
		return ""
	}
	if len(sourceSet) == 1 {
		for s := range sourceSet {
			return s
		}
	}
	return "mixed"
}
