// Package core is the canonical orchestrator of the Brain capability.
//
// core.go wires the four brain services (normalizer, intent
// resolver, ranker, planner) with the CandidateSearcher port to
// implement the Brain port. It performs no IO directly: every
// backend access flows through the CandidateSearcher port.
package core

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/intent"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/normalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/planner"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/ranker"
)

// CanonicalBrain is the canonical implementation of the Brain port.
// It composes the four pure brain services and delegates search to the
// CandidateSearcher port, which is the only place where IO happens.
type CanonicalBrain struct {
	normalizer normalizer.PhraseNormalizer
	resolver   intent.VisualIntentResolver
	searcher   brain.CandidateSearcher
	ranker     ranker.CandidateRanker
	planner    planner.SceneVisualPlanner
	versions   brain.ResolutionVersions
}

// NewCanonicalBrain constructs a CanonicalBrain from the four brain
// services and a CandidateSearcher. Any nil dependency triggers a
// panic at construction time (fail-fast) so a mis-wired composition
// is caught at boot.
func NewCanonicalBrain(
	n normalizer.PhraseNormalizer,
	res intent.VisualIntentResolver,
	searcher brain.CandidateSearcher,
	r ranker.CandidateRanker,
	p planner.SceneVisualPlanner,
) *CanonicalBrain {
	if n == nil {
		panic("brain: normalizer is required")
	}
	if res == nil {
		panic("brain: intent resolver is required")
	}
	if searcher == nil {
		panic("brain: candidate searcher is required")
	}
	if r == nil {
		panic("brain: ranker is required")
	}
	if p == nil {
		panic("brain: planner is required")
	}
	return &CanonicalBrain{
		normalizer: n,
		resolver:   res,
		searcher:   searcher,
		ranker:     r,
		planner:    p,
		versions: brain.ResolutionVersions{
			BrainVersion:          "brain-v1",
			NormalizerVersion:     normalizer.Version,
			IntentResolverVersion: "visual-intent-v1",
			EmbeddingVersion:      "multilingual-e5-v1",
			RankingPolicyVersion:  "media-ranker-v1",
		},
	}
}

// Compile-time assertion: CanonicalBrain satisfies Brain.
var _ brain.Brain = (*CanonicalBrain)(nil)

// Resolve is the single canonical entry point. It processes every
// scene in the request, normalizing text, resolving intent,
// searching candidates, ranking them, and producing a visual plan.
// Errors from any step surface in the per-scene plan status and the
// trace, and processing continues with the remaining scenes.
func (b *CanonicalBrain) Resolve(ctx context.Context, req brain.BrainRequest) (brain.BrainResult, error) {
	result := brain.BrainResult{
		ProjectID: req.ProjectID,
		Trace: brain.ResolutionTrace{
			Versions:     b.versions,
			BackendCalls: []brain.BackendCall{},
		},
	}

	for _, scene := range req.Scenes {
		scenePlan, trace := b.resolveScene(ctx, req.Language, scene, req.Policy)
		result.Scenes = append(result.Scenes, scenePlan)

		if trace != nil {
			result.Trace.NormalizedText += trace.NormalizedText + " "
			result.Trace.BackendCalls = append(result.Trace.BackendCalls, trace.BackendCalls...)
			result.Trace.Selected = append(result.Trace.Selected, trace.Selected...)
		}
	}

	return result, nil
}

func (b *CanonicalBrain) resolveScene(ctx context.Context, language string, scene brain.SceneRequest, policy brain.ResolutionPolicy) (brain.SceneVisualPlan, *brain.ResolutionTrace) {
	trace := &brain.ResolutionTrace{}

	// 1. Normalize phrase.
	normalized, err := b.normalizer.Normalize(ctx, language, scene.Text)
	if err != nil {
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Status:  "error",
		}, trace
	}
	trace.NormalizedText = normalized.Normalized

	// 2. Resolve visual intent.
	intentOut, err := b.resolver.Resolve(ctx, language, normalized.Normalized)
	if err != nil {
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Status:  "error",
		}, trace
	}

	// 3. Search candidates through the canonical port.
	query := brain.SearchQuery{
		Text:       strings.Join(intentOut.Keywords, " "),
		Language:   language,
		MediaTypes: mediaTypesForSlots(scene.Slots),
		Limit:      policy.MaxCandidatesPerSlot,
	}
	searchResult, err := b.searcher.Search(ctx, query)
	if err != nil {
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Intent:  intentOut,
			Status:  "error",
		}, trace
	}
	trace.BackendCalls = append(trace.BackendCalls, brain.BackendCall{
		Backend: "search",
		Hits:    len(searchResult.Candidates),
	})

	// 4. Rank candidates.
	ranked, err := b.ranker.Rank(ctx, scene, intentOut, searchResult.Candidates, policy)
	if err != nil {
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Intent:  intentOut,
			Status:  "error",
		}, trace
	}

	// 5. Plan visual layers.
	plan, err := b.planner.Plan(ctx, scene, intentOut, ranked)
	if err != nil {
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Intent:  intentOut,
			Status:  "error",
		}, trace
	}

	for _, layer := range plan.Layers {
		trace.Selected = append(trace.Selected, brain.SelectedRecord{
			SceneID:     scene.ID,
			Slot:        layer.Slot,
			AssetID:     layer.AssetID,
			CandidateID: layer.CandidateID,
			Score:       layer.Score,
		})
	}

	return plan, trace
}

func mediaTypesForSlots(slots []brain.SlotKind) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, slot := range slots {
		mt := ""
		switch slot {
		case brain.SlotPrimaryVideo:
			mt = "video"
		case brain.SlotSecondaryImage, brain.SlotEvidenceOverlay, brain.SlotMap,
			brain.SlotPortrait, brain.SlotDocument, brain.SlotBackground:
			mt = "image"
		}
		if mt == "" {
			continue
		}
		if _, ok := seen[mt]; ok {
			continue
		}
		seen[mt] = struct{}{}
		out = append(out, mt)
	}
	return out
}
